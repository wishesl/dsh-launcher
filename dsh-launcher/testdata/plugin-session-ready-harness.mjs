/**
 * 回归（0.2.9）：重启后 `sessionController` 比插件晚装配 —— 交付必须**等它就绪**，
 * 而不是把它当失败烧掉重试预算，并打一句误导性的"该 profile 未挂载 Web 会话控制器"。
 *
 * 真机复验（0.2.8，2026-10-02 02:43）：
 *   重启续跑：交付未就绪（sessionController 服务不可用（该 profile 未挂载 Web 会话控制器）），
 *             500ms 后重试（1/6） … 40000ms 后重试（6/6）
 *   重启续跑：已向会话 … 注入重启完成消息（通道 plugin/notice，第 7 次尝试）   ← 靠"重新装载的握手"救回来的
 *
 * 两个问题：①那句话是错的（profile 明明有会话控制器，只是还没装配好）；②6 次重试预算被
 * 必然失败的尝试烧光，真正的就绪事件（cordis 的 `ctx.inject` 回调）反而没被利用。
 *
 * 本 harness 复现同一时序：apply 时 `get("sessionController")` 取不到 → launcher 推来 pending →
 * 第一发只应记"等待就绪、不消耗重试次数" → 500ms 后服务装配完成（inject 回调触发）→
 * 立刻交付成功，且是"第 1 次尝试"。
 *
 * 由 launcher_bridge_plugin_test.go 的 TestBridgePendingDeliveredWhenSessionControllerLate 启动。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

const logs = [];
const record = (level) => (msg) => {
	logs.push(`${level}: ${msg}`);
	console.log(`${level}: ${msg}`);
};

/** 注入记录：整条链只允许成功一次。 */
const delivered = [];

/** 服务装配状态：false = 真机重启后头一两秒的样子（服务取不到）。 */
let ready = false;
/** cordis 的 `ctx.inject(["sessionController"], cb)` 回调：服务就绪时才触发。 */
let sessionReadyCb = null;

const controller = {
	async resolveAgent() {
		return {
			agent: {
				followup(message) {
					delivered.push({ at: Date.now(), message });
				},
			},
		};
	},
	async prompt() {
		throw new Error("不该走到 prompt 回退通道");
	},
};

const settings = {
	describe() {
		return [{ ns: "ui-theme", revision: 1, value: { preference: "dark" } }];
	},
	async update() {},
};

/** 注入回调拿到的子 ctx（服务此刻可解析）。 */
function childCtx() {
	return {
		get: (name) => (name === "sessionController" ? controller : name === "settings" ? settings : undefined),
		timeout: (fn, ms) => {
			const timer = setTimeout(fn, ms);
			return () => clearTimeout(timer);
		},
		effect: (fn) => {
			const dispose = fn();
			return () => {
				if (typeof dispose === "function") dispose();
			};
		},
		on: () => () => {},
	};
}

const ctx = {
	logger: {
		info: record("info"),
		warn: record("warn"),
		error: record("error"),
		debug: record("debug"),
	},
	timeout: (fn, ms) => {
		const timer = setTimeout(fn, ms);
		return () => clearTimeout(timer);
	},
	effect: (fn) => {
		const dispose = fn();
		return () => {
			if (typeof dispose === "function") dispose();
		};
	},
	// 装配中：这一刻取不到 sessionController（真机重启后头 1~2 秒就是这样）。
	get: (name) => (ready && name === "sessionController" ? controller : undefined),
	tools: { register: () => ({ dispose() {} }) },
	on: () => () => {},
	inject: (deps, cb) => {
		if (!Array.isArray(deps)) return;
		if (deps.includes("settings") || deps.includes("timer")) cb(childCtx());
		// sessionController：晚装配，回调留到"就绪"那一刻才触发（cordis 的真实语义）。
		if (deps.includes("sessionController")) sessionReadyCb = cb;
	},
};

apply(ctx);

// 500ms 后服务装配完成 —— 远晚于桥接握手与 pending 下发，保证第一发必然落在"取不到服务"上。
setTimeout(() => {
	ready = true;
	console.log("HARNESS SERVICE READY");
	if (typeof sessionReadyCb === "function") sessionReadyCb(childCtx());
}, 500);

const deadline = Date.now() + 8000;
const tick = setInterval(() => {
	if (delivered.length === 0) {
		if (Date.now() > deadline) {
			clearInterval(tick);
			console.error(`HARNESS FAIL: 8s 内没有交付（等就绪的那条路没走通）\n${logs.join("\n")}`);
			process.exit(1);
		}
		return;
	}
	clearInterval(tick);
	setTimeout(() => {
		if (delivered.length !== 1) {
			console.error(`HARNESS FAIL: 交付了 ${delivered.length} 次（重复注入）\n${JSON.stringify(delivered)}`);
			process.exit(1);
		}
		const message = delivered[0].message;
		const text = message?.content?.[0]?.text ?? "";
		if (!text.includes("[dsh-restart 完成]") || message?.source?.kind !== "plugin:dsh-launcher-plugin") {
			console.error(`HARNESS FAIL: 注入消息形态不对 ${JSON.stringify(message)}`);
			process.exit(1);
		}
		console.log("HARNESS DELIVERED AFTER SERVICE READY");
		console.log("HARNESS SINGLE DELIVERY OK");
		console.log("HARNESS OK");
		process.exit(0);
	}, 600);
}, 50);
