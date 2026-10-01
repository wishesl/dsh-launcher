/**
 * dsh-launcher-plugin — launcher ↔ 实例的单线桥接插件。
 *
 * 一条 loopback WebSocket 线（dsh-launcher 启动的 127.0.0.1 临时端口 + 随机 Bearer token，
 * 经 env DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN 注入；插件把基地址换成 ws://…/ws，
 * token 放在握手头里）承载三件事：
 *
 *   1. 主题同步（双向）：初读共享 profile 的 ui-theme 设置，变化即推 theme 帧，launcher 转成
 *      dsh:theme 事件驱动自身换肤；launcher 里的主题控件下发 set-theme 帧，插件把新值写回
 *      ui-theme（DSH 网页端订阅同一个 ns，打开的页面会当场换肤）；
 *      0.2.6 起还有一条**即时通道**：本插件把桥接坐标注入 DSH 页面（webserver/index-inject），
 *      页面里的客户端半边 lib/client.js 直接连桥接 —— 点启动器的主题按钮，页面当场换肤、
 *      启动器当场跟上，而"把配置写进 profile"（实测 330–350ms）交给 DSH 自己在后台完成。
 *      页面没连上/没加载客户端入口时自动退回服务端写入（fail-open，绝不假装成功）；
 *   2. dsh-restart：确认词 restart-dsh；发 restart 帧**拿到 ack 才退出**，拿不到 ack 就报错不退出
 *      （实例不会白死一次）。退出动作 500ms 后开始：三条计时通道（timer 服务 ctx、apply 的 ctx、
 *      全局 setTimeout）同时挂上，先 ctx.appExit(0) 优雅退出（DSH 自己还有 5s 强制退出上限），
 *      3s 内进程还在就 process.exit / reallyExit / 进程信号逐个兜底，全程留痕、绝不静默卡死；
 *   3. 能力握手：连上后第一帧 hello 是全量快照（能力 + 主题），之后能力变化增量重发；
 *      断线按 RETRY_DELAYS_MS 退避重连，重连成功即重发快照（自愈）。
 *
 * 重启完成的续跑负载也不落盘：重启前随 restart 帧提交，新进程连上后 launcher 主动下发
 * pending 帧，注入「重启完成」成功后回 pending-consumed 确认（确认前负载留在 launcher，
 * 下次启动会重新下发 —— 旧版 pending.json 的语义原样保留）。
 *
 * 零文件：本插件不在实例目录写任何状态文件。缺 env（非 launcher 拉起）时静默降级：
 * 不拦启动、不注册副作用，右栏能力面板按"没有握手"中性展示（fail-open）。
 */
import { randomUUID, timingSafeEqual } from "node:crypto";
import { readFileSync } from "node:fs";
import z from "@deepseek-ai/schemastery";

/** 稳定 Cordis 插件名。 */
const name = "dsh-launcher-plugin";
/** 硬依赖：工具注册表 + timer 服务（`ctx.timeout`，退出与交付都要延时）。 */
const inject = ["tools", "timer"];
/** 无配置：装载与否完全由 launcher 的挂载覆盖层决定。 */
const Config = z.object({});

const CONFIRM_WORD = "restart-dsh";
/** 连接重连 / 交付重试退避（对齐 dsh-self-mcp 的成熟节奏）。 */
const RETRY_DELAYS_MS = [500, 1500, 4000, 10000, 20000, 40000];
/** WebSocket.readyState 的 OPEN。 */
const WS_OPEN = 1;
/** 等 launcher 应答（restart ack）的上限。 */
const RESTART_ACK_TIMEOUT_MS = 5000;
/** 重启：ack 之后多久开始退出（让 dsh-restart 的工具结果先落盘）。 */
const RESTART_EXIT_DELAY_MS = 500;
/** 优雅退出没生效时的观察窗（DSH 自己的强制退出上限是 5s，这里更早动手）。 */
const RESTART_EXIT_VERIFY_MS = 3000;

/** DSH 主题设置命名空间（dsh-client-ui-theme 写进共享 profile 的 ui-theme）。 */
const THEME_NS = "ui-theme";
const THEME_PREFERENCES = new Set(["light", "dark", "system"]);

// —— 桥接端点（dsh-launcher 经 env 注入；缺任一项 = 非 launcher 拉起，静默降级）——
const bridgeURL = process.env.DSH_LAUNCHER_EVENTS ?? "";
const bridgeToken = process.env.DSH_LAUNCHER_TOKEN ?? "";
const instanceId = process.env.DSH_INSTANCE_ID ?? "";
const launchId = process.env.DSH_LAUNCH_ID ?? "";
const supervised = bridgeURL !== "" && bridgeToken !== "";

//#region 桥接连接管理（一条 WebSocket，见 launcher_bridge.go）
const capabilityState = new Map();
/** apply() 的 ctx：日志 / timer / appExit。 */
let ctxRef = null;
/** DSH 的退出入口（dsh-cmdline 在 host ctx 上 provide 的 appExit），经 inject 缓存。
 *  真机 0.2.2 打点显示 `ctx.get("appExit")` 本来就能取到（来源：ctx.get），缓存只是兜底；
 *  取用顺序见 resolveAppExit。 */
let appExitRef = null;
/** timer 服务注入后的 ctx：`ctx.timeout` 只在这个 ctx 上保证可调用
 *  （真机上工具里直接用 apply 的 ctx 调 `ctx.timeout` 会抛
 *  `cannot get property "timeout" without inject`）。 */
let timerCtxRef = null;
/** 退出流程是否已开始：三条计时通道都可能触发回调，这里只放行一次。 */
let exitStarted = false;
/** 当前连接（null = 未连接）。 */
let socket = null;
/** 已发起升级、握手还没完成。 */
let connecting = false;
/** 是否曾成功连上（只影响日志与退避节奏）。 */
let online = false;
let retryIdx = 0;
let retryScheduled = false;
let syncScheduled = false;
/** 已读到的 ui-theme preference（'' = 尚未读到）。 */
let themePreference = "";
/** settings 服务（主题读写的唯一入口；未就绪为 null）。 */
let settingsService = null;
/** 我们自己的 ui-theme 写入进行中：这期间的 document-updated 是自己写出来的，
 *  监听器不必再 describe 一次（那次 describe 会把回程时间再抬一截）。 */
let themeWriteInFlight = false;
/** 主题诊断锚点：收到 set-theme 的时刻，只用于把各段耗时打进实例日志。 */
let themeAnchor = 0;
/** 待交付的重启完成负载（null = 无）。 */
let pendingRestart = null;
/** 本次启动是否已收到 launcher 下发的 pending（挡住重复交付）。 */
let pendingHandled = false;
/** settings 服务是否就绪（用于 5s 后给 themeReport 一个可见结论）。 */
let settingsReady = false;
/** themeReport 是否已给出结论（未给出 = 面板不显示该行，fail-open）。 */
let themeReportSettled = false;
/** 已发出、等 launcher 回应的请求：id → { resolve, reject }。 */
const inflight = new Map();

/** 记一条能力结论。reason 只在 ok=false 时有意义，写人话（原样显示在面板上）。
 *  结论随 hello 快照发给 launcher —— 握手即报告，没有文件、没有陈旧判定。 */
function setCapability(id, ok, reason = "") {
	capabilityState.set(id, { id, ok, reason });
	scheduleSync();
}

/** 插件自身版本（读不到就留空，不影响握手本身）。 */
function pluginVersion() {
	try {
		const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));
		return typeof pkg.version === "string" ? pkg.version : "";
	} catch {
		return "";
	}
}

/** 统一信封（见实现方案 §2.2）。 */
function envelope(type, payload) {
	return { type, instanceId, launchId, payload };
}

/** 桥接的 WebSocket 地址：env 给的是 http://127.0.0.1:<port>，换成 ws://…/ws。 */
function bridgeSocketURL() {
	const url = new URL("/ws", bridgeURL);
	url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
	return url.toString();
}

/** 发一帧给 launcher。未连接返回 false（调用方决定退避/重连后补齐）。 */
function sendFrame(type, payload) {
	if (socket === null || socket.readyState !== WS_OPEN) return false;
	try {
		socket.send(JSON.stringify(envelope(type, payload)));
		return true;
	} catch (error) {
		ctxRef?.logger?.warn?.(`[dsh-launcher-plugin] 帧发送失败（${type}）：${error.message}`);
		return false;
	}
}

/** 全量快照：能力 + 主题。hello 幂等（launcher 直接替换内存里的最近握手），
 *  每次（重）连都发 —— launcher 的内存态随它自己的启动清零。 */
function sendSnapshot() {
	return sendFrame("hello", {
		plugin: name,
		pluginVersion: pluginVersion(),
		reportedAt: new Date().toISOString(),
		capabilities: [...capabilityState.values()],
		theme: themePreference ? { preference: themePreference } : undefined,
	});
}

/**
 * 把桥接连接坐标注入 DSH 页面（`webserver/index-inject` 钩子，dsh-client-ui-theme 用的是同
 * 一个钩子、同一批行形状）。页面里的客户端半边（lib/client.js）靠它连上 launcher 的 loopback
 * 桥，走主题即时通道：点启动器的主题按钮，页面当场换肤、启动器当场跟上，而"把 ui-theme 写进
 * 共享 profile"（实测 330–350ms）放到后台跑。
 *
 * 注入的是一段 body 脚本，token 因此出现在页面里。这不新增暴露面：token 本来就随 env 进了实例
 * 进程（页面脚本与实例同源），且 launcher 侧对 page 角色做了门控 —— 只认主题帧 + Origin 白名单
 * + launch 校验（见 launcher_bridge.go 的 handlePageHello）。
 *
 * 只在 launcher 拉起的实例里注入：没有桥接坐标的页面连不上，注入只是噪音。
 */
function injectPageCoordinates(ctx) {
	if (!supervised || !instanceId || !launchId) return;
	const coordinates = {
		url: bridgeSocketURL(),
		token: bridgeToken,
		instanceId,
		launchId,
		plugin: name,
		pluginVersion: pluginVersion(),
	};
	ctx.on("webserver/index-inject", (table) => {
		table.push({
			kind: "script",
			placement: "body",
			text: `window.__DSH_LAUNCHER_BRIDGE__ = ${JSON.stringify(coordinates)};`,
		});
	}, { prepend: true });
}

/** 防抖的全连：能力变化 → 150ms 后合并成一次快照。 */
function scheduleSync(delay = 150) {
	if (!supervised || ctxRef === null) return;
	if (syncScheduled) return;
	syncScheduled = true;
	ctxRef.timeout(() => {
		syncScheduled = false;
		maintain();
	}, delay);
}

/** 退避重连（封顶 RETRY_DELAYS_MS 末位，永不停止：主题同步需要长期在线）。 */
function scheduleRetry() {
	if (retryScheduled || !supervised || ctxRef === null) return;
	retryScheduled = true;
	const delay = RETRY_DELAYS_MS[Math.min(retryIdx, RETRY_DELAYS_MS.length - 1)];
	retryIdx += 1;
	ctxRef.timeout(() => {
		retryScheduled = false;
		maintain();
	}, delay);
}

/** 保证连接 + 重发快照。连接失败/断开统一由 onclose → scheduleRetry 接管。 */
function maintain() {
	if (!supervised) return;
	if (socket !== null) {
		sendSnapshot();
		return;
	}
	connect();
}

/** 建立 WebSocket：Bearer 放握手头（Node 的全局 WebSocket 支持自定义头，token 不进 URL）。
 *  没有全局 WebSocket（老 Node）→ 静默降级，不重试、不加副作用。 */
function connect() {
	if (!supervised || connecting || socket !== null) return;
	if (typeof WebSocket !== "function") {
		ctxRef?.logger?.warn?.("[dsh-launcher-plugin] 当前 Node 没有全局 WebSocket，桥接不可用（静默降级）");
		return;
	}
	connecting = true;
	let ws;
	try {
		ws = new WebSocket(bridgeSocketURL(), { headers: { authorization: `Bearer ${bridgeToken}` } });
	} catch (error) {
		connecting = false;
		ctxRef?.logger?.warn?.(`[dsh-launcher-plugin] WebSocket 构造失败：${error.message}`);
		scheduleRetry();
		return;
	}
	ws.onopen = () => {
		connecting = false;
		socket = ws;
		retryIdx = 0;
		if (!online) {
			online = true;
			ctxRef?.logger?.info?.("[dsh-launcher-plugin] 已连接 launcher 桥接");
		}
		// 重连后的第一件事就是重发全量快照（launcher 只认内存里的最近一次握手）。
		sendSnapshot();
	};
	ws.onmessage = (event) => handleFrame(event.data);
	ws.onclose = () => {
		connecting = false;
		if (socket === ws) socket = null;
		failInflight("桥接断开");
		if (online) {
			online = false;
			ctxRef?.logger?.warn?.("[dsh-launcher-plugin] 与 launcher 的桥接断开，按退避重连");
		}
		scheduleRetry();
	};
	// 错误细节交给随之而来的 onclose 统一处理（Node 会把 error 后接一个 close）。
	ws.onerror = () => undefined;
}

/** 收帧分发。未知类型忽略（协议只增不改，老新两侧不会互相打死）。 */
function handleFrame(raw) {
	let frame;
	try {
		frame = JSON.parse(typeof raw === "string" ? raw : String(raw));
	} catch (error) {
		ctxRef?.logger?.warn?.(`[dsh-launcher-plugin] 收到无法解析的帧：${error.message}`);
		return;
	}
	switch (frame?.type) {
		case "pending":
			receivePending(frame.payload?.pending ?? null);
			return;
		case "set-theme":
			void applyThemeCommand(frame.payload);
			return;
		case "restart-result":
			settleRequest(frame.payload);
			return;
		default:
			return;
	}
}

/** 请求 / 应答：发帧 + 等 launcher 回带同一 id 的结果帧。
 *  断线或超时都 reject —— 调用方据此报错、不退出进程。 */
function request(type, payload, timeoutMs = RESTART_ACK_TIMEOUT_MS) {
	return new Promise((resolve, reject) => {
		const id = randomUUID();
		if (!sendFrame(type, { ...payload, id })) {
			reject(new Error("launcher 桥接未连接"));
			return;
		}
		inflight.set(id, { resolve, reject });
		ctxRef?.timeout?.(() => {
			const entry = inflight.get(id);
			if (entry === undefined) return; // 已应答
			inflight.delete(id);
			entry.reject(new Error("等待 launcher 应答超时"));
		}, timeoutMs);
	});
}

/** 结算一条请求应答（restart-result）。 */
function settleRequest(payload) {
	const id = typeof payload?.id === "string" ? payload.id : "";
	const entry = inflight.get(id);
	if (entry === undefined) return;
	inflight.delete(id);
	if (payload?.ok === true) entry.resolve(payload);
	else entry.reject(new Error(payload?.error ?? "launcher 拒绝"));
}

/** 断线时把所有等待中的请求一次性拒掉（否则工具调用会一直挂着）。 */
function failInflight(reason) {
	for (const [id, entry] of inflight) {
		inflight.delete(id);
		entry.reject(new Error(reason));
	}
}

/** 主题增量推送（幂等状态）。未连接就跳过：重连后的快照会带上当前值。 */
function pushTheme(preference) {
	if (!supervised) return;
	if (sendFrame("theme", { preference })) {
		themeTrace(`推送 theme=${preference}（自收到 ${themeSince()}）`);
	} else {
		ctxRef?.logger?.debug?.("[dsh-launcher-plugin] 桥接未连接，主题推送跳过（重连后随快照补齐）");
	}
}
//#endregion

//#region 重启完成交付（复用 dsh-self-mcp 的成熟套路：agent.followup 优先、prompt 回退）
/** 折叠行上的一行摘要（`notice` 形态的 `summary`）。 */
const NOTICE_SUMMARY = "DSH 已重启完成，继续执行";
/** 一行摘要上限，对齐 @deepseek-ai/dsh-llm 的 CONTEXT_SUMMARY_MAX_CHARS。 */
const CONTEXT_SUMMARY_MAX_CHARS = 120;

/** 「重启完成」注入正文。 */
function restartCompleteText(pending) {
	return `[dsh-restart 完成] 上次调用 dsh-restart 后进程已重启完成。`
		+ `原因：${pending.reason ?? "(未说明)"}；发起会话：${pending.sessionId}；`
		+ `重启请求时间：${new Date(pending.requestedAt).toISOString()}。`
		+ `请继续刚才的调试任务；若有插件需要重启才生效，请据此继续验证。`;
}

/** 递归冻结，等价 @deepseek-ai/dsh-util-values 的 deepFreeze。 */
function deepFreeze(value) {
	if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
		Object.freeze(value);
		for (const key of Object.keys(value)) deepFreeze(value[key]);
	}
	return value;
}

/** 截断一行摘要，语义对齐 @deepseek-ai/dsh-llm 的 boundContextSummary。 */
function boundSummary(summary) {
	return summary.length <= CONTEXT_SUMMARY_MAX_CHARS
		? summary
		: `${summary.slice(0, CONTEXT_SUMMARY_MAX_CHARS - 1)}…`;
}

/** 构造一条 plugin 来源的 user 消息（等价 @deepseek-ai/dsh-llm 的 createUserMessage）。
 *
 *  不 import 该包：插件装在 <profile>/.dsh-builtin 下，而 @deepseek-ai/dsh-llm 不是 profile 的
 *  直接依赖，装载期解析失败会连工具一起挂掉。这里只依赖 node:crypto 的 randomUUID——
 *  inbox 投影对该消息用 z.custom() 校验，唯一硬要求是 id 全局唯一。
 *
 *  消息来源形态（v4，producer-owned）：
 *    `source.kind='plugin:dsh-launcher-plugin'` + `form='notice'` 是产品既有的语义通道：
 *    模型看到完整 content，客户端（dsh-client-ui-chat 的 contextProvenance/contextForm）
 *    把该条渲染成 inject 折叠行，并在折叠行上显示 summary —— 不会以「用户气泡」出现。 */
function createPluginNotice(text, summary) {
	return deepFreeze({
		id: randomUUID(),
		role: "user",
		content: [{ type: "text", text }],
		source: { kind: `plugin:${name}`, form: "notice", summary: boundSummary(summary) },
	});
}

/** 首选通道：agent 级 followup + plugin/notice 来源（绕开 sessionController.prompt 的
 *  user 消息铸造，仍走产品自己的消息路径，不手工改事件日志）。 */
async function deliverAsPluginNotice(controller, pending, text) {
	if (typeof controller.resolveAgent !== "function") {
		throw new Error("sessionController.resolveAgent 不可用");
	}
	const resolved = await controller.resolveAgent(pending.sessionId);
	if (resolved === null || typeof resolved !== "object") {
		throw new Error(`resolveAgent 返回异常: ${JSON.stringify(resolved ?? null)}`);
	}
	if (resolved.error !== void 0) {
		throw new Error(`resolveAgent 失败: ${JSON.stringify(resolved.error)}`);
	}
	const agent = resolved.agent;
	if (agent === void 0 || typeof agent.followup !== "function") {
		throw new Error("Agent 驱动接口不可用（缺少 followup）");
	}
	agent.followup(createPluginNotice(text, NOTICE_SUMMARY));
}

/** 投递「重启完成」并唤醒发起会话。首选 plugin/notice；失败回退 prompt() 保证送达。 */
async function deliverRestartComplete(ctx, pending) {
	const controller = ctx.get("sessionController");
	if (controller === void 0) {
		throw new Error("sessionController 服务不可用（该 profile 未挂载 Web 会话控制器）");
	}
	const text = restartCompleteText(pending);

	try {
		await deliverAsPluginNotice(controller, pending, text);
		setCapability("restartDelivery", true);
		return;
	} catch (error) {
		ctx.logger.warn(`[dsh-launcher-plugin] plugin 通道投递失败（${error.message}），回退到 prompt() 可见通道`);
		setCapability("restartDelivery", false, `已回退 sessionController.prompt()：${error.message}`);
	}

	if (typeof controller.prompt !== "function") {
		throw new Error("sessionController.prompt 不可用");
	}
	const result = await controller.prompt({
		requestId: `launcher-restart-${pending.sessionId}-${Date.now()}`,
		sessionId: pending.sessionId,
		mode: "queue",
		content: [{ type: "text", text }],
	}, new AbortController().signal);
	if (result === null || typeof result !== "object" || result.accepted !== true) {
		throw new Error(`prompt 未被接受: ${JSON.stringify(result ?? null)}`);
	}
}

/** 收 launcher 下发的续跑负载（替代旧版 GET /pending）。只处理第一条：
 *  交付成功或用尽之前不接受第二条，避免重复注入。 */
function receivePending(pending) {
	if (pendingHandled) return;
	pendingHandled = true;
	if (pending === null || pending === undefined) return;
	if (typeof pending !== "object" || !pending.sessionId) return;
	pendingRestart = pending;
	scheduleDelivery(ctxRef, pending);
}

/** 带退避的交付：成功即回 pending-consumed 帧；进程内不无限重试，
 *  用尽后负载留在 launcher（下次启动重新下发），同时挡住新的 dsh-restart 调用。 */
function scheduleDelivery(ctx, pending) {
	let attempts = 0;
	const attempt = () => {
		deliverRestartComplete(ctx, pending)
			.then(() => {
				pendingRestart = null;
				ctx.logger.info(`[dsh-launcher-plugin] 已向会话 ${pending.sessionId} 注入重启完成消息`);
				if (!sendFrame("pending-consumed")) {
					ctx.logger.warn("[dsh-launcher-plugin] 桥接未连接，pending-consumed 未发出，下次启动可能重复注入一次");
				}
			})
			.catch((error) => {
				attempts += 1;
				if (attempts <= RETRY_DELAYS_MS.length) {
					const delay = RETRY_DELAYS_MS[attempts - 1];
					ctx.logger.warn(`[dsh-launcher-plugin] 交付未就绪（${error.message}），${delay}ms 后重试 (${attempts}/${RETRY_DELAYS_MS.length})`);
					ctx.timeout(attempt, delay);
				} else {
					ctx.logger.error(`[dsh-launcher-plugin] 重启完成消息交付失败，负载保留在 launcher 待下次启动重试: ${error.message}`);
					setCapability("restartDelivery", false, `交付失败：${error.message}`);
				}
			});
	};
	attempt();
}
//#endregion

//#region 主题（ui-theme settings，双向）
/** 取 ui-theme 那一行的描述符（describe() 是进程内同步读）；没有/读失败 → null。 */
function themeRow(settings) {
	try {
		const rows = settings.describe();
		if (!Array.isArray(rows)) return null;
		return rows.find((r) => r?.ns === THEME_NS) ?? null;
	} catch {
		return null;
	}
}

/** 读取一次 ui-theme preference。没有条目/读失败 → null（不下发，保持现状）。 */
function readThemePreference(settings) {
	try {
		const row = themeRow(settings);
		if (row === null) return null;
		const pref = row?.value?.preference;
		return THEME_PREFERENCES.has(pref) ? pref : "system";
	} catch {
		return null;
	}
}

/**
 * 主题诊断留痕：只写 stderr（console.error → launcher 捕获的实例日志），不发桥接帧 ——
 * 主题每次点击都会走一遍，回报帧会把启动器的日志面板刷满。
 */
function themeTrace(line) {
	try {
		console.error(`[dsh-launcher-plugin] 主题：${line}`);
	} catch {
		// 日志写不出去不能影响主题切换
	}
}

/** 距收到 set-theme 过了多少毫秒（没有锚点就写 ?）。 */
function themeSince() {
	return themeAnchor === 0 ? "?" : `${Date.now() - themeAnchor}ms`;
}

/**
 * 写 ui-theme。**故意不带期望 revision**。
 *
 * dsh-settings 的 `update` 一次调用内部本来就要跑两遍全量 `describe()`
 * （`write` 的 edit 回调里一次、写完再一次），而 describe 要把每个命名空间的
 * schema 序列化成 JSON 快照——这是点击路径里最可疑的一段开销（0.2.5 的
 * 诊断留痕会把每段毫秒数打出来）。
 * 我们再补第三次只为 CAS（"preference 被并发改过就报冲突"），收益很小：
 * update 的合并语义（mergeLayers）本身就保住别人改的其它字段。
 * 失败重试保留（文件锁超时之类的瞬时错误）。
 */
async function writeThemePreference(settings, preference) {
	let lastError;
	for (let attempt = 0; attempt < 2; attempt += 1) {
		try {
			await settings.update(THEME_NS, { preference });
			return;
		} catch (error) {
			lastError = error;
		}
	}
	throw lastError;
}

/**
 * 应用 launcher 下发的主题命令（set-theme）：写共享 profile 的 ui-theme。
 *
 * 为什么写设置而不是只改本机：DSH 网页端订阅了同一个 ns
 * （dsh-client-ui-theme 的 settings scope adoption），写进去打开的页面会当场换肤。
 * 写成功后显式回一帧 theme —— 即使值没变（update 不产生 document-updated）也让
 * launcher 拿到确认，启动器前端据此清掉「待同步」状态。
 */
async function applyThemeCommand(payload) {
	const id = typeof payload?.id === "string" ? payload.id : "";
	const preference = payload?.preference;
	try {
		if (!THEME_PREFERENCES.has(preference)) {
			throw new Error(`非法主题 ${JSON.stringify(preference ?? null)}`);
		}
		if (settingsService === null || typeof settingsService.update !== "function") {
			throw new Error("settings 服务没有 update（该组合改不了主题）");
		}
		themeAnchor = Date.now();
		themeTrace(`收到 set-theme=${preference}（id=${id || "-"}）`);
		themeWriteInFlight = true;
		try {
			await writeThemePreference(settingsService, preference);
		} finally {
			themeWriteInFlight = false;
		}
		themeTrace(`写入 ui-theme=${preference} 完成（自收到 ${themeSince()}）`);
		themePreference = preference; // 自己写的值：watcher 之后读到同值不会再推一次
		setCapability("themeSet", true);
		sendFrame("command-result", { id, ok: true });
		sendFrame("theme", { preference });
		ctxRef?.logger?.info?.(`[dsh-launcher-plugin] 已按启动器请求写入 ui-theme = ${preference}`);
	} catch (error) {
		themeTrace(`写入失败：${error.message}（自收到 ${themeSince()}）`);
		setCapability("themeSet", false, `写入 ui-theme 失败：${error.message}`);
		sendFrame("command-result", { id, ok: false, error: error.message });
		ctxRef?.logger?.warn?.(`[dsh-launcher-plugin] 写入 ui-theme 失败：${error.message}`);
	}
}

/**
 * 主题监听：初读 + settings/document-updated 事件 + 1s 轮询兜底。
 *
 * 事件冒泡（ownerContext.emit 到插件子上下文）在不同 DSH 版本上不保证，
 * 所以必须有轮询兜底 —— describe() 是进程内同步读，1s 一次的成本可忽略；
 * 事件在的话就是毫秒级，事件不在最迟 1s 跟上。
 */
function watchTheme(settingsCtx, settings) {
	const check = (source) => {
		const started = Date.now();
		const pref = readThemePreference(settings);
		if (source !== "poll") {
			themeTrace(`${source}：读 ui-theme 用时 ${Date.now() - started}ms，值=${pref ?? "null"}`);
		}
		if (pref === null || pref === themePreference) return;
		themePreference = pref;
		pushTheme(pref);
	};
	const initial = readThemePreference(settings);
	if (initial !== null && initial !== themePreference) {
		themePreference = initial;
		// 初读也推一次：hello 快照可能已经发出（或随后发出），幂等无害。
		pushTheme(initial);
	}
	// 事件挂在上下文上（settings/forms 是 service 实例，本身不发这个事件）。
	settingsCtx.on?.("settings/document-updated", (ns) => {
		if (ns !== THEME_NS && ns !== undefined) return;
		if (themeWriteInFlight) {
			// 这是我们自己刚写出来的那次变更：值已知，写完 applyThemeCommand 会推帧。
			// 在这里再 describe 一次只会白白拖慢启动器收到回音的时间。
			themeTrace(`事件命中但自身写入进行中，跳过读取（自收到 ${themeSince()}）`);
			return;
		}
		themeTrace(`事件命中（自收到 ${themeSince()}）`);
		check("事件");
	});
	// 兜底轮询：真机实测（2026-10-01 23:33）事件路径 77–90ms 就到，
	// 所以把兜底从 1s 放宽到 5s——只有事件真的不通时才用得上；
	// 每次轮询都要跑一遍全量 describe（同步阻塞），频率越低越不容易挤到点击上。
	const poll = () => {
		check("poll");
		ctxRef?.timeout?.(poll, 5000);
	};
	ctxRef?.timeout?.(poll, 5000);
}
//#endregion

//#region 内嵌支持（embed）：放宽 DSH 的浏览器会话校验，供启动器跨源 iframe 内嵌
/** 允许内嵌的来源（跨源 iframe 请求里的 `Origin`）。默认只放行启动器页面源
 *  `http://wails.localhost`（Wails v2 在 Windows 上固定用它承载前端）。要换源改这里。 */
const EMBED_ALLOWED_ORIGINS = new Set(["http://wails.localhost"]);

/** 读取一个请求头（Node 已小写化，这里兼容原始大小写与数组形态）。 */
function headerValue(headers, name) {
	const raw = headers?.[name] ?? headers?.[name.toLowerCase()];
	if (typeof raw === "string") return raw;
	return Array.isArray(raw) && raw.length > 0 ? raw[0] : void 0;
}

/** 一个 URL 的 host 是否等于给定 Host 头（判断"请求来自本机同源的文档"）。 */
function sameHost(url, host) {
	if (typeof url !== "string" || typeof host !== "string" || host.length === 0) return false;
	try {
		return new URL(url).host === host;
	} catch {
		return false;
	}
}

/** `GET /?token=<launchToken>`，判定语义与 DSH 内部 tokenMatches 一致（等长 + 定时安全比较）。 */
function hasValidLaunchToken(req, launchToken) {
	if (req?.method !== "GET") return false;
	if (typeof launchToken !== "string" || launchToken.length === 0) return false;
	let url;
	try {
		url = new URL(req.url ?? "/", "http://dsh.invalid");
	} catch {
		return false;
	}
	if (url.pathname !== "/") return false;
	const tokens = url.searchParams.getAll("token");
	if (tokens.length !== 1) return false;
	const actual = Buffer.from(tokens[0], "utf8");
	const expected = Buffer.from(launchToken, "utf8");
	return actual.byteLength === expected.byteLength && timingSafeEqual(actual, expected);
}

/**
 * 放宽 DSH 的浏览器会话校验，让启动器能把界面嵌进自己的跨源 iframe。
 *
 * 为什么必须放宽：DSH 的会话 cookie 是 `SameSite=Strict`（见 client-connection 的
 * sessionCookie），跨站 iframe 里的请求一律不带它。于是 `/?token=…` 虽然能过，
 * 但它返回的 303→`/` 那一步拿不到 cookie，直接 401 —— 界面永远打不开。
 *
 * 只做两处**最小**放宽，其余请求（其它站点、没带 token 的）行为完全不变：
 *   1) `authorizeIndex`：`GET /?token=<有效 launch token>` 直接返回 true（渲染 index）；
 *   2) `requestRejection`：只对 `Origin` 命中白名单的请求跳过 cookie 校验。
 *
 * 两处都是「通过 service 对象、每请求现查」的方法，替换实例上的方法即生效。
 * 结论写进能力（embedRelax）随握手上报：内嵌入口按它置灰，而不是让用户撞 401。
 */
function relaxEmbedAuth(ctx) {
	const conn = ctx.get("connection");
	if (conn === void 0 || conn.browserAuth === void 0) {
		ctx.logger.info("[dsh-launcher-plugin] embed: 没有 connection service（非 web 组合），跳过");
		setCapability("embedRelax", false, "该组合没有 connection 服务（非 web 组合），内嵌不适用");
		return;
	}
	if (conn.__dshLauncherPluginEmbedPatched === true) {
		return; // 幂等：重复 apply 不叠加包装
	}
	if (typeof conn.authorizeIndex !== "function" || typeof conn.requestRejection !== "function") {
		const why = "connection 上没有 authorizeIndex / requestRejection（DSH 内部接口变了？）";
		ctx.logger.warn("[dsh-launcher-plugin] embed: connection 上没有预期的认证方法，跳过（DSH 版本变了？）");
		setCapability("embedRelax", false, why);
		return;
	}

	const auth = conn.browserAuth;
	const originalAuthorizeIndex = conn.authorizeIndex.bind(conn);
	const originalRequestRejection = conn.requestRejection.bind(conn);

	conn.authorizeIndex = (req, res) => {
		const site = headerValue(req?.headers, "sec-fetch-site");
		// 只在**真的跨站 iframe** 里走直出：普通浏览器仍走原来的 303+cookie，
		// 否则它拿不到 cookie，后续 /api 会 401。
		if (site === "cross-site" && hasValidLaunchToken(req, auth.launchToken)) {
			ctx.logger.info("[dsh-launcher-plugin] embed: 跨站 iframe 携带有效 token → 直接渲染 index（跳过 cookie 往返）");
			return true;
		}
		return originalAuthorizeIndex(req, res);
	};

	conn.requestRejection = (req) => {
		const rejection = originalRequestRejection(req);
		if (rejection === void 0) return void 0;
		const host = headerValue(req?.headers, "host");
		const origin = headerValue(req?.headers, "origin");
		const referer = headerValue(req?.headers, "referer");
		// ① 启动器页面直接发起的请求（跨源 → 带 Origin，命中白名单）
		if (typeof origin === "string" && EMBED_ALLOWED_ORIGINS.has(origin.toLowerCase())) {
			ctx.logger.info(`[dsh-launcher-plugin] embed: 放行 ${origin} 的 ${req.method} ${req.url}`);
			return void 0;
		}
		// ② 内嵌文档自身发起的请求：Origin（或 Referer）与请求 Host 同源。
		//    流式端点 /api/remote.mux 只带 Origin、没有 Referer，所以必须用 Origin 判定。
		if (sameHost(origin, host) || sameHost(referer, host)) {
			return void 0;
		}
		return rejection;
	};

	conn.__dshLauncherPluginEmbedPatched = true;
	setCapability("embedRelax", true);
	ctx.logger.info(
		`[dsh-launcher-plugin] embed: 已放宽浏览器会话校验（白名单来源: ${[...EMBED_ALLOWED_ORIGINS].join(", ")}）`
	);
}
//#endregion

/** 注册工具的包装：把"注册成功 / 失败"记进能力（随握手上报）。 */
function trackRestartTool(register) {
	try {
		const disposable = register();
		setCapability("restartTool", true);
		return disposable;
	} catch (error) {
		setCapability("restartTool", false, `工具注册失败：${error.message}`);
		throw error;
	}
}

/**
 * 取 DSH 的退出入口。返回 { exit, via }，exit 为 null 表示三条路都没拿到。
 *
 * 为什么要三条：官方消费者（dsh-cmdline / dsh-headless）用的都是裸 `ctx.get("appExit")`，
 * 所以它排第一；但真机上这一段没生效（重启请求 ack 之后进程一直活着），而 cordis 的
 * 服务解析在"祖先后 provide"这种次序下实测三种取法都能拿到（见 .dsh-tmp 的两个探针），
 * 说明失败可能与具体挂载分支有关。与其赌，不如三条都试，并把用了哪条写进日志。
 */
function resolveAppExit(ctx) {
	if (typeof appExitRef === "function") {
		return { exit: appExitRef, via: "inject 缓存" };
	}
	try {
		const direct = ctx.get("appExit");
		if (typeof direct === "function") {
			return { exit: direct, via: "ctx.get" };
		}
	} catch (error) {
		// 没注册 / 未激活时 cordis 会抛，继续试下一条
	}
	try {
		const prop = ctx.appExit;
		if (typeof prop === "function") {
			return { exit: prop, via: "ctx.appExit" };
		}
	} catch (error) {
		// 同上
	}
	return { exit: null, via: "" };
}

/**
 * 重启相关的诊断轨迹：同时走两个"一定看得见"的通道。
 *
 * 1. `console.error` → DSH 进程的 stderr → launcher 捕获的实例日志；
 * 2. 桥接 `command-result` 帧（id 留空 = 纯回报）→ launcher 的 app.log 记「回报：…」。
 *
 * 刻意不用 `ctx.logger`：真机实测（2026-10-01 22:16）它的输出既不进实例日志、也不进 app.log，
 * 排障时等于没写。事故的教训就是"三个通道都看不见"。
 */
function restartTrace(line) {
	try {
		console.error(`[dsh-launcher-plugin] ${line}`);
	} catch {
		// 日志写不出去不能影响退出
	}
	sendFrame("command-result", { id: "", ok: true, detail: line });
}

/** 最后一招：确保进程真的死掉。逐个尝试，任何一个成功后面的都执行不到。 */
function forceExit(reason) {
	restartTrace(`兜底硬退：${reason}`);
	const attempts = [
		["process.exit", () => process.exit(0)],
		["process.reallyExit", () => process.reallyExit(0)],
		["process.kill(SIGTERM)", () => process.kill(process.pid, "SIGTERM")],
		["process.kill(SIGKILL)", () => process.kill(process.pid, "SIGKILL")],
	];
	for (const [label, run] of attempts) {
		try {
			run();
		} catch (error) {
			restartTrace(`${label} 失败：${error?.message ?? String(error)}`);
		}
	}
}

/** 读一个可能被 cordis 属性拦截器挡下来的成员类型（自身不抛错）。 */
function memberKind(target, prop) {
	try {
		return typeof target?.[prop];
	} catch (error) {
		return `抛错：${error?.message ?? String(error)}`;
	}
}

/**
 * 安排一次延时动作 —— **三条通道全都挂上**，谁先响算谁（回调必须幂等）。
 *
 * 真机事故（2026-10-01 22:16）：ack 之后进程 20s 不退，既没有异常、也没有任何"退出被调用"的
 * 痕迹 —— 而 DSH 只要真的走到 `appExit`，自己就有 5s 强制退出上限，不可能 20s 还活着。
 * 也就是说"ack 之后退出"那段延时代码没生效。
 *
 * 通道清单（真机 0.2.3 探针实测：装载时三条都能触发）：
 *   ① timer 服务注入后的 ctx（`timerCtxRef.timeout`）
 *   ② apply 的 ctx（`ctx.timeout`；在工具上下文里会抛 `cannot get property "timeout" without inject`）
 *   ③ 全局 `setTimeout`（普通插件里就是真计时器；cordis 动态包 sandbox 才有替换）
 *
 * 为什么**不能只挂第一条**：0.2.3 只挂第一条就出事 —— DSH 一旦开始关闭，`ctx.timeout` 排进去的
 * 回调不再触发（`appExit(0)` 之后那个 3s 硬退兜底整个没响，进程又活满 20s 等 launcher 看门狗），
 * 而全局 `setTimeout` 那条照样会响。所以这里一次把三条都挂上，宁可重复触发。
 */
function scheduleExit(ctx, run, delay) {
	const armed = [];
	for (const [label, target] of [
		["timerCtx.timeout", timerCtxRef],
		["ctx.timeout", ctx],
	]) {
		if (!target) continue;
		try {
			target.timeout(run, delay);
			armed.push(label);
		} catch (error) {
			restartTrace(`${label} 不可用：${error?.message ?? String(error)}`);
		}
	}
	try {
		setTimeout(run, delay);
		armed.push("setTimeout");
	} catch (error) {
		restartTrace(`setTimeout 不可用：${error?.message ?? String(error)}`);
	}
	return armed.join("+");
}

function apply(ctx) {
	ctxRef = ctx;
	// 能执行到这里就说明插件确实装载了 —— 随握手上报，是面板的第一条。
	setCapability("pluginLoaded", true);
	ctx.logger.info(`[dsh-launcher-plugin] 已装载：桥接=${supervised ? bridgeURL : "未配置"}（launcher=${process.env.DSH_LAUNCHER === "1" ? "是" : "否"}）`);
	// 装载打点：apply 可能被调用多次（真机上出现过 2~3 次 hello），pid 便于和进程树对账。
	restartTrace(`已装载（plugin ${pluginVersion() || "?"}，pid ${process.pid}）`);

	// 0) 内嵌支持：connection service 就绪后放宽浏览器会话校验（只有 web 组合才有它）
	ctx.inject(["connection"], (connectionCtx) => {
		relaxEmbedAuth(connectionCtx);
	});

	// 0.5) 退出入口：DSH 的 appExit 由 dsh-cmdline 在 host ctx 上 provide（web 组合里
	//      排在插件挂载之后）。服务就绪就拿一份缓存起来，dsh-restart 用。
	ctx.inject(["appExit"], (exitCtx) => {
		const { exit, via } = resolveAppExit(exitCtx);
		if (typeof exit === "function") {
			appExitRef = exit;
			restartTrace(`已取得 appExit（来源：${via}），重启走优雅退出`);
		} else {
			restartTrace("拿不到 appExit（重启时走硬退兜底）");
		}
	});

	// 0.6) 计时器：退出与交付都靠它延时。timer 注入后的 ctx 才保证 `timeout` 可调用
	//      （真机在工具里用 apply 的 ctx 调会抛 `cannot get property "timeout" without inject`）。
	ctx.inject(["timer"], (timerCtx) => {
		timerCtxRef = timerCtx;
		restartTrace(`已取得 timer（timerCtx.timeout=${memberKind(timerCtx, "timeout")}，ctx.timeout=${memberKind(ctx, "timeout")}）`);
	});

	// 1) 主题：settings 就绪 → 初读 + 事件 + 轮询；5s 没就绪给一个可见结论
	ctx.inject(["settings"], (settingsCtx) => {
		const settings = settingsCtx.get("settings");
		if (!settings || typeof settings.describe !== "function") {
			themeReportSettled = true;
			setCapability("themeReport", false, "settings 服务没有 describe（DSH 内部接口变了？），主题不会跟随");
			return;
		}
		settingsService = settings;
		settingsReady = true;
		themeReportSettled = true;
		setCapability("themeReport", true);
		// 能接收 set-theme 的前提是 update 在（真正的写路径见 applyThemeCommand）。
		const canWrite = typeof settings.update === "function";
		setCapability("themeSet", canWrite, canWrite ? "" : "settings 服务没有 update，启动器里切主题改不到 DSH");
		watchTheme(settingsCtx, settings);
	});
	if (supervised) {
		ctx.timeout(() => {
			if (!settingsReady && !themeReportSettled) {
				setCapability("themeReport", false, "settings 服务不可用（该组合没有设置服务），主题不会跟随");
			}
		}, 5000);
	}

	// 2) 注册唯一工具 dsh-restart（仅 launcher 拉起时 —— 没有桥接的工具只会撞死）
	if (supervised) {
		ctx.effect(() => trackRestartTool(() => ctx.tools.register({
			name: "dsh-restart",
			description:
				"重启整个 DSH 进程。由 dsh-launcher 监督自动重新拉起；重启完成后本插件会自动向发起会话注入"
				+ "「重启完成」消息并继续该会话。适用于需要重启才生效的插件调试。必须传 confirm=\"restart-dsh\"。",
			parameters: {
				type: "object",
				properties: {
					reason: { type: "string", description: "重启原因（用于审计与重启完成消息）" },
					confirm: { type: "string", description: "确认串，必须为 restart-dsh" },
				},
				required: ["confirm"],
				additionalProperties: false,
			},
			output: {
				schema: {
					type: "object",
					properties: { status: { type: "string" } },
					required: ["status"],
					additionalProperties: false,
				},
				render(_args, value) {
					return [{ type: "text", text: `dsh-restart: ${value.status}` }];
				},
			},
			async execute(args, exec) {
				// 护栏 1：强制确认串
				if (args.confirm !== CONFIRM_WORD) {
					return { status: "rejected: confirm 必须为 restart-dsh" };
				}
				// 护栏 2：必须有发起会话
				const session = exec.agent?.session;
				if (exec.agent === void 0 || session === void 0) {
					return { status: "rejected: 无发起会话上下文" };
				}
				// 护栏 3：子代理 / 工作流子代理不允许触发进程级重启
				const header = session.header;
				if (header?.origin === "subagent" || (header?.delegationDepth ?? 0) > 0) {
					return { status: "rejected: 子代理不允许触发 DSH 重启" };
				}
				// 护栏 4：幂等 —— 上一次重启完成的消息还没交付，先不放行
				if (pendingRestart !== null) {
					return { status: "rejected: 已有待交付的重启请求，请等待其完成" };
				}
				if (!supervised) {
					return { status: "rejected: 本实例非 dsh-launcher 拉起（缺少 DSH_LAUNCHER_EVENTS），不支持自重启" };
				}

				const requestedAt = Date.now();
				const reason = typeof args.reason === "string" ? args.reason : "";
				const pending = {
					sessionId: String(session.id),
					callId: String(exec.callId ?? ""),
					reason,
					requestedAt,
					launchedByLauncher: true,
				};

				// launcher 契约：先拿 ack，收到才退出。请求失败/超时 = launcher 不可达 → 实例存活。
				try {
					await request("restart", { reason, pending });
				} catch (error) {
					return { status: `rejected: launcher 未确认重启（${error.message}），实例不退出` };
				}

				// 延迟一拍再请求退出：先让 execute 的返回结果落盘。
				// 三条计时通道全挂上（见 scheduleExit），是否重复由 exitStarted 挡。
				const armed = scheduleExit(ctx, () => {
					if (exitStarted) {
						restartTrace("退出已在进行中，忽略重复触发");
						return;
					}
					exitStarted = true;
					restartTrace(`重启：ack 已收到，开始退出（plugin ${pluginVersion() || "?"}，pid ${process.pid}）`);
					let exit = null;
					let via = "";
					try {
						({ exit, via } = resolveAppExit(ctxRef ?? ctx));
					} catch (error) {
						restartTrace(`取 appExit 抛错：${error?.message ?? String(error)}`);
					}
					if (typeof exit === "function") {
						restartTrace(`请求 DSH 优雅退出（appExit 来源：${via}）`);
						try {
							exit(0);
						} catch (error) {
							restartTrace(`appExit 调用失败：${error?.message ?? String(error)}`);
						}
						// 优雅退出没生效（DSH 内部最坏 5s 强制退出）→ 3s 观察窗后自己动手。
						// 真机 0.2.3 教训：DSH 一旦开始关闭，`ctx.timeout` 排进去的回调就不再触发
						// （这条兜底整个没响，进程又活满 20s）—— 所以 scheduleExit 三条通道全挂。
						scheduleExit(ctx, () => forceExit("appExit 调用后进程仍在"), RESTART_EXIT_VERIFY_MS);
						return;
					}
					// 兜底：拿不到优雅退出入口也必须真的退出。否则 launcher 只能靠 20s 看门狗救，
					// 用户看到的就是"点了重启要等 20 秒"（2026-10-01 21:29 / 22:16 两次真机事故）。
					// 代价是跳过优雅拆卸（session 内容早已落盘），回报走 restartTrace 的双通道留痕。
					restartTrace("取不到 appExit，直接硬退（跳过优雅拆卸）");
					forceExit("拿不到 appExit");
				}, RESTART_EXIT_DELAY_MS);
				restartTrace(`退出动作已排入计时器（${armed || "无可用计时器"}，延迟 ${RESTART_EXIT_DELAY_MS}ms）`);

				return { status: "restarting" };
			},
		})), "dsh-launcher-plugin.tool");
	} else {
		ctx.logger.info("[dsh-launcher-plugin] 未由 dsh-launcher 拉起，跳过 dsh-restart 注册（静默降级）");
	}

	// 3) 连接桥接（首连 + 快照；续跑负载由 launcher 主动下发）；断线由退避重连接管
	if (supervised) {
		maintain();
	}

	// 4) 网页端即时通道：把桥接坐标注入 DSH 页面，页面里的客户端半边（lib/client.js）据此
	//    连上桥接。客户端入口由 DSH 启动时按 package.json 的 dsh.client 自动装载 —— 升级插件
	//    后要重启一次 DSH 才生效；没生效只是退回服务端写入（慢 ~300ms），不影响正确性。
	injectPageCoordinates(ctx);
}

export { Config, apply, inject, name };
