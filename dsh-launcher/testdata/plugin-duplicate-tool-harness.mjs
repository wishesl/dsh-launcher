/**
 * 双半包装载回归 harness（0.2.7+ 的第二道锁）—— 锁的是真机 00:58:33 这条日志：
 *
 *   dsh: warning: 1 entry did not activate
 *   mkt-client-dsh-launcher-plugin (file:///…/node_modules/dsh-launcher-plugin/lib/index.js):
 *     Error: tool "dsh-restart" is already registered
 *       at trackRestartTool (…/lib/index.js:871:22)
 *       at Object.apply (…/@deepseek-ai/cordis/lib/index.js:120:36)
 *
 * 起因：`dsh.client` 声明让宿主 Loader 给同一个包**多挂一行**（双半包，见 README
 * 「装载会话与定时器护栏」），两行跑的是同一份 apply，所以第二次
 * `ctx.tools.register("dsh-restart")` 必然撞名。0.2.7 之前的 trackRestartTool 会把这个错
 * **原样抛出** → 这一行装载失败 → DSH 销毁它的 fiber；而它恰好是最后建会话的那一行
 * （beginSession 已经把上一行收掉了）→ 插件转入静默：桥接不连、主题不再跟随。
 *
 * 假 ctx 只还原这一条：`tools.register` 抛真机原文的撞名错。
 *
 *   C) apply(ctxC) 必须**不抛错**，且会话还活着。
 *      （把 trackRestartTool 里"撞名就 rethrow"的旧行为放回去，这里必然 FAIL。）
 *
 * 设了 DSH_LAUNCHER_EVENTS/TOKEN ⇒ supervised=true，于是它会真的去连一个不存在的桥接
 * （127.0.0.1:9，立刻被拒）—— 这正是要覆盖的路径：注册失败也绝不许把这一行弄死。
 * 断言完立刻 process.exit(0)，不等退避重试。
 */
process.env.DSH_LAUNCHER = "1";
process.env.DSH_INSTANCE_ID = "inst-a";
process.env.DSH_LAUNCH_ID = "L1";
process.env.DSH_LAUNCHER_EVENTS = "ws://127.0.0.1:9/ws";
process.env.DSH_LAUNCHER_TOKEN = "harness-token";

const { apply } = await import("../embed/dsh-launcher-plugin/lib/index.js");

const traces = [];
const realError = console.error;
console.error = (...args) => {
	traces.push(args.map((a) => (typeof a === "string" ? a : String(a))).join(" "));
	realError(...args);
};

function say(line) {
	console.log(`[harness] ${line}`);
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** 假 cordis ctx：只实现 apply 在 supervised 下用到的面。 */
function makeCtx() {
	const state = { cleanups: [], toolError: null };
	const logger = { info: () => {}, warn: () => {}, error: () => {}, debug: () => {} };
	const settings = {
		describe: () => [{ ns: "ui-theme", value: { preference: "light" } }],
		update: async () => {},
	};
	const child = {
		logger,
		get: (name) => (name === "settings" ? settings : undefined),
		on: () => () => {},
		effect: () => () => {},
	};
	const ctx = {
		logger,
		get: (name) => (name === "settings" ? settings : undefined),
		on: () => () => {},
		effect(fn) {
			const cleanup = fn();
			if (typeof cleanup === "function") state.cleanups.push(cleanup);
			return () => {};
		},
		inject(names, callback) {
			try {
				callback(child);
			} catch (error) {
				realError(`[harness] inject(${JSON.stringify(names)}) 回调抛错：${error.message}`);
			}
			return () => {};
		},
		timeout(callback, delay) {
			const timer = setTimeout(callback, delay);
			return () => clearTimeout(timer);
		},
		tools: {
			register() {
				// 真机原文（dsh-tools/lib/index.js:2634 duplicateError）
				throw new Error(
					'tool "dsh-restart" is already registered (for a per-agent variant, register through that agent\'s `agent.ctx` instead)',
				);
			},
		},
	};
	return { ctx, state };
}

// ── C：撞名注册 → apply 不许抛 ────────────────────────────────────────────────
const C = makeCtx();
let applyThrew = null;
try {
	apply(C.ctx);
} catch (error) {
	applyThrew = error;
}
say(applyThrew ? `apply 抛错：${applyThrew.message}` : "apply 没有抛错");

// 模拟 DSH：apply 抛出 ⇒ 这一行装载失败 ⇒ fiber 销毁（跑 effect 清理）
if (applyThrew) {
	for (const cleanup of C.state.cleanups) {
		try {
			cleanup();
		} catch (error) {
			realError(`[harness] 清理回调抛错：${error.message}`);
		}
	}
}

await sleep(300);

const dupTraced = traces.some((line) => line.includes("已由另一行装载注册（双半包）"));
const sessionEnded = traces.some((line) => line.includes("装载会话 #1 结束"));

if (applyThrew) {
	console.log(`HARNESS FAIL apply 把撞名注册抛了出来（真机上这一行会装载失败）：${applyThrew.message}`);
	process.exit(1);
}
if (!dupTraced) {
	console.log("HARNESS FAIL 撞名注册没有留下「已由另一行装载注册」的痕迹");
	process.exit(1);
}
if (sessionEnded) {
	console.log("HARNESS FAIL 撞名注册之后会话被收掉了（插件会静默）");
	process.exit(1);
}
console.log("HARNESS DUP TOOL OK");
console.log("HARNESS OK");
process.exit(0);
