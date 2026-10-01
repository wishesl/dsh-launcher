/**
 * dsh-launcher-plugin — launcher ↔ 实例的单线桥接插件。
 *
 * 一条 loopback HTTP 线（dsh-launcher 启动的 127.0.0.1 临时端口 + 随机 Bearer token，
 * 经 env DSH_LAUNCHER_EVENTS / DSH_LAUNCHER_TOKEN 注入）承载三件事：
 *
 *   1. 主题同步：监听共享 profile 的 ui-theme 设置（settings 服务），变化即 POST /theme，
 *      launcher 转成 dsh:theme 事件驱动自身界面换肤；
 *   2. dsh-restart：确认词 restart-dsh；POST /restart 拿到 ack 才 ctx.appExit(0)，
 *      POST 失败就报错不退出（实例不会白死一次，比旧版"写完文件碰运气"更稳）；
 *   3. 能力握手：apply 后 POST /connect 全量快照（能力 + 主题），之后能力/主题变化
 *      增量同步；断线按 RETRY_DELAYS_MS 退避重连，重连成功即重发快照（自愈）。
 *
 * 重启完成的续跑负载也不落盘：重启前随 /restart 提交，新进程 GET /pending 回取，
 * 注入「重启完成」成功后 POST /pending-consumed 确认（确认前负载留在 launcher，
 * 下次启动会重新回取 —— 旧版 pending.json 的语义原样保留）。
 *
 * 零文件：本插件不在实例目录写任何状态文件。缺 env（非 launcher 拉起）时静默降级：
 * 不拦启动、不注册副作用，右栏能力面板按"没有握手"中性展示（fail-open）。
 */
import { randomUUID, timingSafeEqual } from "node:crypto";
import { readFileSync } from "node:fs";
import z from "@deepseek-ai/schemastery";

/** 稳定 Cordis 插件名。 */
const name = "dsh-launcher-plugin";
/** 硬依赖：工具注册表 + Host 计时器（ctx.timeout）。 */
const inject = ["tools", "timer"];
/** 无配置：装载与否完全由 launcher 的挂载覆盖层决定。 */
const Config = z.object({});

const CONFIRM_WORD = "restart-dsh";
/** 连接重连 / 交付重试退避（对齐 dsh-self-mcp 的成熟节奏）。 */
const RETRY_DELAYS_MS = [500, 1500, 4000, 10000, 20000, 40000];

/** DSH 主题设置命名空间（dsh-client-ui-theme 写进共享 profile 的 ui-theme）。 */
const THEME_NS = "ui-theme";
const THEME_PREFERENCES = new Set(["light", "dark", "system"]);

// —— 桥接端点（dsh-launcher 经 env 注入；缺任一项 = 非 launcher 拉起，静默降级）——
const bridgeURL = process.env.DSH_LAUNCHER_EVENTS ?? "";
const bridgeToken = process.env.DSH_LAUNCHER_TOKEN ?? "";
const instanceId = process.env.DSH_INSTANCE_ID ?? "";
const launchId = process.env.DSH_LAUNCH_ID ?? "";
const supervised = bridgeURL !== "" && bridgeToken !== "";

//#region 桥接连接管理
const capabilityState = new Map();
/** apply() 的 ctx：日志 / timer / appExit。 */
let ctxRef = null;
/** 最近一次 /connect 成功与否（决定重连退避与主题推送的补握手）。 */
let online = false;
let retryIdx = 0;
let retryScheduled = false;
let syncScheduled = false;
/** 已读到的 ui-theme preference（'' = 尚未读到）。 */
let themePreference = "";
/** 待交付的重启完成负载（null = 无）。 */
let pendingRestart = null;
/** 本次启动是否已回取过 /pending（失败会复位，留到下次 maintain 重试）。 */
let pendingHandled = false;
/** settings 服务是否就绪（用于 5s 后给 themeReport 一个可见结论）。 */
let settingsReady = false;
/** themeReport 是否已给出结论（未给出 = 面板不显示该行，fail-open）。 */
let themeReportSettled = false;

/** 记一条能力结论。reason 只在 ok=false 时有意义，写人话（原样显示在面板上）。
 *  结论随 /connect 快照发给 launcher —— 握手即报告，没有文件、没有陈旧判定。 */
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

/** 一次桥接调用。非 2xx 一律抛错 —— 由调用方决定重试还是报给用户。 */
async function bridgeCall(method, path, payload, query) {
	if (!supervised) {
		throw new Error("非 dsh-launcher 拉起（缺少 DSH_LAUNCHER_EVENTS）");
	}
	const url = new URL(path, bridgeURL);
	if (query) {
		for (const [k, v] of Object.entries(query)) url.searchParams.set(k, String(v));
	}
	const res = await fetch(url, {
		method,
		headers: {
			authorization: `Bearer ${bridgeToken}`,
			...(payload !== undefined ? { "content-type": "application/json" } : {}),
		},
		body: payload !== undefined ? JSON.stringify(payload) : undefined,
	});
	if (!res.ok) {
		throw new Error(`${method} ${path} → HTTP ${res.status}`);
	}
	try {
		return await res.json();
	} catch {
		return null;
	}
}

/** 全量快照：能力 + 主题。/connect 幂等（launcher 直接替换内存里的最近握手）。 */
async function syncSnapshot() {
	await bridgeCall("POST", "/connect", envelope("connect", {
		plugin: name,
		pluginVersion: pluginVersion(),
		reportedAt: new Date().toISOString(),
		capabilities: [...capabilityState.values()],
		theme: themePreference ? { preference: themePreference } : undefined,
	}));
}

/** 防抖的全连：能力变化 → 150ms 后合并成一次快照。 */
function scheduleSync(delay = 150) {
	if (!supervised || ctxRef === null) return;
	if (syncScheduled) return;
	syncScheduled = true;
	ctxRef.timeout(() => {
		syncScheduled = false;
		void maintain();
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
		void maintain();
	}, delay);
}

/** 连接 + 快照 + 回取续跑负载。任何一步失败都进入退避重试。 */
async function maintain() {
	if (!supervised) return;
	try {
		await syncSnapshot();
		if (!online) {
			online = true;
			retryIdx = 0;
			ctxRef?.logger?.info?.("[dsh-launcher-plugin] 已连接 launcher 桥接");
		}
		await deliverPendingOnce();
	} catch (error) {
		if (online) {
			ctxRef?.logger?.warn?.(`[dsh-launcher-plugin] 桥接断开（${error.message}），按退避重连`);
		}
		online = false;
		scheduleRetry();
	}
}

/** 主题增量推送。成功但此前掉线 → 顺路补一次握手（重连自愈）。 */
async function pushTheme(preference) {
	if (!supervised) return;
	try {
		await bridgeCall("POST", "/theme", envelope("theme", { preference }));
		online = true;
	} catch (error) {
		online = false;
		scheduleRetry();
		ctxRef?.logger?.debug?.(`[dsh-launcher-plugin] 主题推送失败: ${error.message}`);
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

/** 回取一次 /pending（仅一次；桥接不可达时复位，留到下次 maintain 重试）。 */
async function deliverPendingOnce() {
	if (pendingHandled) return;
	pendingHandled = true;
	try {
		const data = await bridgeCall("GET", "/pending", undefined, { instanceId, launchId });
		const pending = data?.pending;
		if (pending === null || pending === undefined) return;
		if (typeof pending !== "object" || !pending.sessionId) return;
		pendingRestart = pending;
		pendingHandled = true;
		scheduleDelivery(ctxRef, pending);
	} catch (error) {
		pendingHandled = false;
		throw error;
	}
}

/** 带退避的交付：成功即 POST /pending-consumed；进程内不无限重试，
 *  用尽后负载留在 launcher（下次启动重新回取），同时挡住新的 dsh-restart 调用。 */
function scheduleDelivery(ctx, pending) {
	let attempts = 0;
	const attempt = () => {
		deliverRestartComplete(ctx, pending)
			.then(async () => {
				pendingRestart = null;
				ctx.logger.info(`[dsh-launcher-plugin] 已向会话 ${pending.sessionId} 注入重启完成消息`);
				try {
					await bridgeCall("POST", "/pending-consumed", envelope("pending-consumed"));
				} catch (error) {
					ctx.logger.warn(`[dsh-launcher-plugin] /pending-consumed 确认失败（${error.message}），下次启动可能重复注入一次`);
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

//#region 主题（ui-theme settings）
/** 读取一次 ui-theme preference。没有条目/读失败 → null（不下发，保持现状）。 */
function readThemePreference(settings) {
	try {
		const rows = settings.describe();
		if (!Array.isArray(rows)) return null;
		const row = rows.find((r) => r?.ns === THEME_NS);
		const pref = row?.value?.preference;
		return THEME_PREFERENCES.has(pref) ? pref : "system";
	} catch {
		return null;
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
	const check = () => {
		const pref = readThemePreference(settings);
		if (pref === null || pref === themePreference) return;
		themePreference = pref;
		void pushTheme(pref);
	};
	const initial = readThemePreference(settings);
	if (initial !== null && initial !== themePreference) {
		themePreference = initial;
		// 初读也推一次：/connect 快照可能已经发出（或随后发出），幂等无害。
		void pushTheme(initial);
	}
	// 事件挂在上下文上（settings/forms 是 service 实例，本身不发这个事件）。
	settingsCtx.on?.("settings/document-updated", (ns) => {
		if (ns === THEME_NS || ns === undefined) check();
	});
	const poll = () => {
		check();
		ctxRef?.timeout?.(poll, 1000);
	};
	ctxRef?.timeout?.(poll, 1000);
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

function apply(ctx) {
	ctxRef = ctx;
	// 能执行到这里就说明插件确实装载了 —— 随握手上报，是面板的第一条。
	setCapability("pluginLoaded", true);
	ctx.logger.info(`[dsh-launcher-plugin] 已装载：桥接=${supervised ? bridgeURL : "未配置"}（launcher=${process.env.DSH_LAUNCHER === "1" ? "是" : "否"}）`);

	// 0) 内嵌支持：connection service 就绪后放宽浏览器会话校验（只有 web 组合才有它）
	ctx.inject(["connection"], (connectionCtx) => {
		relaxEmbedAuth(connectionCtx);
	});

	// 1) 主题：settings 就绪 → 初读 + 事件 + 轮询；5s 没就绪给一个可见结论
	ctx.inject(["settings"], (settingsCtx) => {
		const settings = settingsCtx.get("settings");
		if (!settings || typeof settings.describe !== "function") {
			themeReportSettled = true;
			setCapability("themeReport", false, "settings 服务没有 describe（DSH 内部接口变了？），主题不会跟随");
			return;
		}
		settingsReady = true;
		themeReportSettled = true;
		setCapability("themeReport", true);
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

				// launcher 契约：先拿 ack，收到才退出。POST 失败 = launcher 不可达 → 实例存活。
				try {
					await bridgeCall("POST", "/restart", envelope("restart", { reason, pending }));
				} catch (error) {
					return { status: `rejected: launcher 不可达（${error.message}），实例不退出` };
				}

				// 延迟一拍再请求退出：先让 execute 的返回结果落盘
				setTimeout(() => {
					const exit = ctx.get("appExit");
					if (typeof exit === "function") exit(0);
				}, 500);

				return { status: "restarting" };
			},
		})), "dsh-launcher-plugin.tool");
	} else {
		ctx.logger.info("[dsh-launcher-plugin] 未由 dsh-launcher 拉起，跳过 dsh-restart 注册（静默降级）");
	}

	// 3) 连接桥接（首连 + 回取续跑负载）；断线由 maintain 的退避重连接管
	if (supervised) {
		void maintain();
	}
}

export { Config, apply, inject, name };
