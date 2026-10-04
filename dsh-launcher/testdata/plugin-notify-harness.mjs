/**
 * 会话通知的端到端契约测试 —— 跑的是**真正被内嵌的插件代码**
 * （由 launcher_bridge_plugin_test.go 的 TestBridgeNotifyEndToEndWithRealPlugin 启动）。
 *
 * 覆盖：真插件 + 真 WebSocket 桥 + 真帧形状。断言在 Go 侧（stub 掉系统通知后端，收集
 * launcher 实际弹出的通知），这里只负责"按真实会话事件顺序喂事件"：
 *
 *   S1 顶层会话、turn/end(completed)  → 一条 turn-complete（标题取 sessionTitle 服务）
 *   S2 ask_user_question 工具调用      → 一条 question（正文固定「我有一些问题」）
 *   S3 turn/end(aborted)              → 不发
 *   S4 子代理会话 completed            → 不发
 *   S5 超长回复                        → 正文按 50 码点截断补省略号
 *   S6 标题服务取不到 + 首条真人消息    → 标题退到首条真人消息前 20 字
 *   S7 标题服务取不到 + session/title  → 标题用事件里的标题
 *
 * 假 ctx 只覆盖契约必要的表面（logger / on / inject / effect / tools / timeout），
 * 不模拟 DSH 内部实现。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

const logs = [];
const record = (level) => (msg) => {
	logs.push(`${level}: ${msg}`);
};

/** 当前假会话事件监听器（apply 时注册，见 attachSessionNotify）。 */
let sessionListener = null;

const settings = {
	revision: 1,
	value: { preference: "system" },
	describe() {
		return [{ ns: "ui-theme", revision: this.revision, value: this.value }];
	},
	async update() {
		this.revision += 1;
	},
};

/** 会话标题服务：只为带 title 的会话返回快照（其余返回 undefined = 走兜底）。 */
const titles = new Map([["s-1", "会话标题"]]);
const titleService = {
	get(session) {
		const title = titles.get(session?.id);
		return title === undefined ? undefined : { title, messageSeqs: [], source: { kind: "auto" } };
	},
};

const ctx = {
	logger: {
		info: record("info"),
		warn: record("warn"),
		error: record("error"),
		debug: record("debug"),
	},
	timeout: (fn, ms) => setTimeout(fn, ms),
	effect: (fn) => fn(),
	get: () => undefined,
	tools: { register: () => ({ dispose() {} }) },
	on: (event, listener) => {
		if (event === "session/event") {
			sessionListener = listener;
			return () => {};
		}
		if (event === "webserver/index-inject") {
			listener([]);
			return () => {};
		}
		return () => {};
	},
	inject: (deps, cb) => {
		if (!Array.isArray(deps)) return;
		if (deps.includes("sessionTitle")) {
			cb({ get: (n) => (n === "sessionTitle" ? titleService : undefined) });
			return;
		}
		if (deps.includes("settings")) {
			cb({ get: (n) => (n === "settings" ? settings : undefined), on: () => undefined });
		}
		// "connection" / "appExit" / "timer" / "sessionController" 故意不注入：本测试不覆盖它们。
	},
};

apply(ctx);

if (sessionListener === null) {
	console.error("HARNESS FAIL: 没有注册 session/event 监听");
	process.exit(1);
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** 顶层会话（无 origin / 无 delegationDepth）。 */
const sessionOf = (id) => ({ id, header: {} });
/** 子代理会话。 */
const subagentOf = (id) => ({ id, header: { origin: "subagent" } });
const textMessage = (text, source) => ({
	content: [{ type: "text", text }],
	...(source === undefined ? {} : { source }),
});
const assistant = (turn, text) => ({
	type: "assistant/message",
	data: { turn, step: 1, message: textMessage(text), stream: [] },
});
const turnEnd = (turn, kind) => ({ type: "turn/end", data: { turn, reason: { kind } } });

/** 等插件自己连上桥接（onopen 会写这条日志）—— 未连接时 sendFrame 会直接丢帧。 */
async function waitConnected() {
	const deadline = Date.now() + 8000;
	while (Date.now() < deadline) {
		if (logs.some((line) => line.includes("已连接 launcher 桥接"))) return true;
		await sleep(50);
	}
	return false;
}

const connected = await waitConnected();
if (!connected) {
	console.error(`HARNESS FAIL: 8s 内插件没有连上桥接\n${logs.join("\n")}`);
	process.exit(1);
}

// S1：回答正常结束 → turn-complete（标题取 sessionTitle 服务）。
sessionListener(sessionOf("s-1"), assistant(7, "回答正文\n第二行"));
sessionListener(sessionOf("s-1"), turnEnd(7, "completed"));
await sleep(60);

// S2：模型提问（回合被阻塞，不会有 turn/end）→ question。
sessionListener(sessionOf("s-1"), {
	type: "tool/call",
	data: { turn: 8, step: 1, callId: "call-1", name: "ask_user_question", arguments: "{}" },
});
await sleep(60);

// S3：被取消的回合 → 不发（用户就在跟前）。
sessionListener(sessionOf("s-aborted"), assistant(1, "取消前的内容"));
sessionListener(sessionOf("s-aborted"), turnEnd(1, "aborted"));
await sleep(60);

// S4：子代理会话正常结束 → 不发（用户看的是顶层会话）。
sessionListener(subagentOf("s-sub"), assistant(1, "子代理的内容"));
sessionListener(subagentOf("s-sub"), turnEnd(1, "completed"));
await sleep(60);

// S5：超长回复 → 正文按 50 码点截断。
sessionListener(sessionOf("s-1"), assistant(9, "A".repeat(60)));
sessionListener(sessionOf("s-1"), turnEnd(9, "completed"));
await sleep(60);

// S6：标题服务取不到 → 标题退到首条真人消息前 20 字（注入的上下文不算）。
const firstUserText = "FALLBACK-USER-TEXT-0123456789";
sessionListener(sessionOf("s-fallback"), {
	type: "user/message",
	data: { turn: 1, ...textMessage(firstUserText, { kind: "user" }) },
});
sessionListener(sessionOf("s-fallback"), assistant(1, "兜底正文"));
sessionListener(sessionOf("s-fallback"), turnEnd(1, "completed"));
await sleep(60);

// S7：标题服务取不到，但见过 session/title 事件 → 用事件里的标题。
sessionListener(sessionOf("s-title-ev"), { type: "session/title", data: { title: "事件标题" } });
sessionListener(sessionOf("s-title-ev"), assistant(1, "缓存正文"));
sessionListener(sessionOf("s-title-ev"), turnEnd(1, "completed"));
await sleep(200);

console.log("HARNESS SENT");
console.log(logs.join("\n"));
process.exit(0);
