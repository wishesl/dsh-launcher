/**
 * dsh-self-mcp — DSH 自管理重启插件。
 *
 * 提供唯一工具 `dsh-restart`：
 *   1. 写 `<cwd>/.dsh-self-mcp/pending.json`（交付意图）与 `restart-request.json`（launcher 契约）；
 *   2. 请求进程干净退出（ctx.appExit(0)）；由 dsh-launcher 的 exit-reconcile 检测请求文件后自动重新拉起。
 *
 * 重启完成后（新进程 boot，本插件重新挂载）：
 *   读取 pending.json，向发起会话投递「重启完成」消息并唤醒该会话继续。
 *   首选 agent 级 `agent.followup()` + `source.kind='plugin'` + `form='notice'`：模型看到完整
 *   正文，界面把该条渲染成 inject 折叠行（不是用户气泡）；该通道不可用时回退旧的
 *   `sessionController.prompt()`（界面是用户气泡）以保证消息一定送到。
 *   两条通道都复用产品自己的消息路径，不手工改事件日志。
 *
 * 装配与残留：由 dsh-launcher 按「项目 opt-in 标记 + 插件已装」双重门控生成
 * 项目级 --patch 覆盖层装载；不用本项目启动时本插件不被任何行引用 → 工具不存在、状态不碰。
 */
import { randomUUID } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import z from "@deepseek-ai/schemastery";

/** 稳定 Cordis 插件名。 */
const name = "dsh-self-mcp";
/** 硬依赖：工具注册表 + Host 计时器。 */
const inject = ["tools", "timer"];

/** 无配置：装载与否完全由 launcher 的门控覆盖层决定。 */
const Config = z.object({});

const STATE_DIR = ".dsh-self-mcp";
const PENDING_FILE = "pending.json";
const REQUEST_FILE = "restart-request.json";
const CONFIRM_WORD = "restart-dsh";
/** 交付重试退避（覆盖新进程 web server 绑定前的窗口）。 */
const RETRY_DELAYS_MS = [500, 1500, 4000, 10000, 20000, 40000];

function stateDir() {
	return path.join(process.cwd(), STATE_DIR);
}

function pendingPath() {
	return path.join(stateDir(), PENDING_FILE);
}

function requestPath() {
	return path.join(stateDir(), REQUEST_FILE);
}

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
 *  `source.kind='plugin'` + `form='notice'` 是产品既有的语义通道：模型看到完整 content，
 *  客户端（dsh-client-ui-chat 的 contextProvenance/contextForm）把该条渲染成 inject 折叠行，
 *  并在折叠行上显示 summary——因此不会以「用户气泡」出现在转录里。 */
function createPluginNotice(text, summary) {
	return deepFreeze({
		id: randomUUID(),
		role: "user",
		content: [{ type: "text", text }],
		source: { kind: "plugin", plugin: name, form: "notice", summary: boundSummary(summary) },
	});
}

/** 首选通道：agent 级 followup + plugin/notice 来源。
 *
 *  绕过 sessionController.prompt() 的原因：prompt() 是浏览器「发送消息」那条路径，
 *  必然铸出 source.kind==='user' 的 durable user 消息，界面上就是一个用户气泡。
 *  agent 驱动接口允许自带 source，因此能走 inject 折叠行；同时它仍在进程内、
 *  不经过 /api 的浏览器认证与 typert 网关封装。 */
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

/** 投递「重启完成」并唤醒发起会话。
 *
 *  首选 plugin/notice 通道（界面为 inject 折叠行）；任何失败都回退到旧的
 *  sessionController.prompt() 通道（界面为用户气泡），保证消息一定送到——
 *  否则 pending.json 会一直留着，护栏 4 将拒绝后续所有 dsh-restart 调用。 */
async function deliverRestartComplete(ctx, pending) {
	const controller = ctx.get("sessionController");
	if (controller === void 0) {
		throw new Error("sessionController 服务不可用（该 profile 未挂载 Web 会话控制器）");
	}
	const text = restartCompleteText(pending);

	try {
		await deliverAsPluginNotice(controller, pending, text);
		return;
	} catch (error) {
		ctx.logger.warn(`[dsh-self-mcp] plugin 通道投递失败（${error.message}），回退到 prompt() 可见通道`);
	}

	if (typeof controller.prompt !== "function") {
		throw new Error("sessionController.prompt 不可用");
	}
	const result = await controller.prompt({
		requestId: `self-restart-${pending.sessionId}-${Date.now()}`,
		sessionId: pending.sessionId,
		mode: "queue",
		content: [{ type: "text", text }],
	}, new AbortController().signal);
	if (result === null || typeof result !== "object" || result.accepted !== true) {
		throw new Error(`prompt 未被接受: ${JSON.stringify(result ?? null)}`);
	}
}

/** 带退避的交付：成功即删除 pending；失败保留 pending.json（下次启动重试），进程内不无限重试。 */
function scheduleDelivery(ctx, pending) {
	let attempts = 0;
	const attempt = () => {
		deliverRestartComplete(ctx, pending)
			.then(() => {
				try { rmSync(pendingPath(), { force: true }); } catch { /* 忽略 */ }
				ctx.logger.info(`[dsh-self-mcp] 已向会话 ${pending.sessionId} 注入重启完成消息`);
			})
			.catch((error) => {
				attempts += 1;
				if (attempts <= RETRY_DELAYS_MS.length) {
					const delay = RETRY_DELAYS_MS[attempts - 1];
					ctx.logger.warn(`[dsh-self-mcp] 交付未就绪（${error.message}），${delay}ms 后重试 (${attempts}/${RETRY_DELAYS_MS.length})`);
					ctx.timeout(attempt, delay);
				} else {
					ctx.logger.error(`[dsh-self-mcp] 重启完成消息交付失败，保留 pending.json 待下次启动重试: ${error.message}`);
				}
			});
	};
	attempt();
}

function apply(ctx) {
	ctx.logger.info(`[dsh-self-mcp] 已装载：dsh-restart 工具可用（launcher=${process.env.DSH_LAUNCHER === "1" ? "是" : "否"}）`);

	// 1) 重启完成交付（新进程 boot 时）
	if (existsSync(pendingPath())) {
		try {
			const pending = JSON.parse(readFileSync(pendingPath(), "utf8"));
			if (pending !== null && typeof pending === "object" && pending.sessionId && pending.delivered !== true) {
				scheduleDelivery(ctx, pending);
			}
		} catch (error) {
			ctx.logger.error(`[dsh-self-mcp] pending.json 解析失败: ${error.message}`);
		}
	}

	// 2) 注册唯一工具 dsh-restart
	ctx.effect(() => ctx.tools.register({
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
			// 护栏 4：幂等——已有未交付的重启请求则拒绝
			if (existsSync(pendingPath())) {
				return { status: "rejected: 已有待交付的重启请求，请等待其完成" };
			}

			const sessionId = String(session.id);
			const requestedAt = Date.now();
			const reason = typeof args.reason === "string" ? args.reason : "";
			const pending = {
				sessionId,
				callId: String(exec.callId ?? ""),
				reason,
				requestedAt,
				launchedByLauncher: process.env.DSH_LAUNCHER === "1",
			};
			try {
				mkdirSync(stateDir(), { recursive: true });
				writeFileSync(pendingPath(), JSON.stringify(pending, null, 2), "utf8");
				if (process.env.DSH_LAUNCHER === "1") {
					// launcher 契约：重启请求文件，干净退出后由 launcher 自动重新拉起
					writeFileSync(requestPath(), JSON.stringify({
						instanceId: process.env.DSH_INSTANCE_ID ?? "",
						requestedAt,
						reason,
					}, null, 2), "utf8");
				}
			} catch (error) {
				try { rmSync(pendingPath(), { force: true }); } catch { /* 忽略 */ }
				return { status: `rejected: 写入重启状态失败: ${error.message}` };
			}

			// 非 launcher 托管（控制台直启等）兜底：自 spawn 替换进程
			if (process.env.DSH_LAUNCHER !== "1") {
				try {
					spawn(process.execPath, process.argv.slice(1), {
						detached: true,
						stdio: "ignore",
						cwd: process.cwd(),
						env: { ...process.env, DSH_SELF_RESTART: "1" },
					}).unref();
				} catch (error) {
					return { status: `rejected: 自重启拉起失败: ${error.message}` };
				}
			}

			// 延迟一拍再请求退出：先让 execute 的返回结果落盘（普通 setTimeout 不随 fiber dispose 被清）
			setTimeout(() => {
				const exit = ctx.get("appExit");
				if (typeof exit === "function") exit(0);
			}, 500);

			return { status: "restarting" };
		},
	}), "dsh-self-mcp.tool");
}

export { Config, apply, inject, name };
