/**
 * dsh-self-mcp — DSH 自管理重启插件。
 *
 * 提供唯一工具 `dsh-restart`：
 *   1. 写 `<cwd>/.dsh-self-mcp/pending.json`（交付意图）与 `restart-request.json`（launcher 契约）；
 *   2. 请求进程干净退出（ctx.appExit(0)）；由 dsh-launcher 的 exit-reconcile 检测请求文件后自动重新拉起。
 *
 * 重启完成后（新进程 boot，本插件重新挂载）：
 *   读取 pending.json，通过本地 web API `POST /api/session.prompt` 向发起会话注入
 *   「重启完成」消息并唤醒该会话继续——完全复用产品的消息发送路径，不手工改事件日志。
 *
 * 装配与残留：由 dsh-launcher 按「项目 opt-in 标记 + 插件已装」双重门控生成
 * 项目级 --patch 覆盖层装载；不用本项目启动时本插件不被任何行引用 → 工具不存在、状态不碰。
 */
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

/** 本地 web 端口：webStartup 服务 → DSH_WEB_URL → 默认 3080。 */
function webPort(ctx) {
	const startup = ctx.get("webStartup");
	if (startup !== void 0 && typeof startup.port === "number" && startup.port > 0) return startup.port;
	const m = /:(\d{2,5})/.exec(process.env.DSH_WEB_URL ?? "");
	if (m !== null) return Number(m[1]);
	return 3080;
}

/** 通过官方 /api/session.prompt 路径向目标会话注入一条 user 消息并唤醒（与用户发消息同路径）。 */
async function deliverRestartComplete(ctx, pending) {
	const port = webPort(ctx);
	const body = {
		type: "client-request",
		rpcId: `self-restart-${pending.sessionId}-${Date.now()}`,
		method: "session.prompt",
		payload: {
			sessionId: pending.sessionId,
			mode: "queue",
			content: [{
				type: "text",
				text: `[dsh-restart 完成] 上次调用 dsh-restart 后进程已重启完成。`
					+ `原因：${pending.reason ?? "(未说明)"}；发起会话：${pending.sessionId}；`
					+ `重启请求时间：${new Date(pending.requestedAt).toISOString()}。`
					+ `请继续刚才的调试任务；若有插件需要重启才生效，请据此继续验证。`,
			}],
		},
	};
	const res = await fetch(`http://127.0.0.1:${port}/api/session.prompt`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify(body),
	});
	if (!res.ok) throw new Error(`prompt 端点返回 HTTP ${res.status}`);
	const json = await res.json().catch(() => null);
	if (json === null || json.result?.ok !== true) {
		throw new Error(`prompt 被拒: ${JSON.stringify(json?.result ?? json)}`);
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
