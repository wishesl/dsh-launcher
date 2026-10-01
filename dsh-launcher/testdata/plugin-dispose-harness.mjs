/**
 * 崩溃回归 harness（0.2.7 修复的锁）：复现 2026-10-02 00:28 的真机事故时序 ——
 *
 *   插件装载 → 它的 fiber 失效（DSH 会重复 apply，旧 fiber 随之销毁）
 *   → 但 `ctx.timeout()` 排下的 5s 主题轮询**照跑**
 *     （cordis-plugin-timer 的 TimerService.timeout 用 `this.ctx.effect(...)`，
 *      计时器登记在 timer 服务自己的 ctx 上，调用方 fiber 销毁不会取消它）
 *   → 回调里再调 `ctx.timeout` 抛 `cannot get required service "timer" in inactive context`
 *   → 这个异常发生在 timer 回调里，没人接 → 整个 DSH 进程 exit 1。
 *
 * 用假 ctx 把同一时序做出来（假 ctx 忠实还原"失效后 timeout 抛错"这一条）：
 *
 *   A) apply(ctxA) → 让 ctxA 失效、**不**跑 effect 清理（= timer 活着、fiber 没了）
 *      → 等 5s 轮询到期 → 断言进程还活着，且护栏留了痕。
 *      （没有 0.2.7 的护栏时，这里就是一个未捕获异常 → 进程 exit 1，测试直接失败。）
 *   B) 再 apply(ctxB) → 跑 effect 清理（= 正常的 fiber 销毁）→ 断言会话被收掉、
 *      定时器被主动取消（timer 服务不替我们取消，所以必须自己挂 effect 收）。
 *
 * 不设 DSH_LAUNCHER* 环境变量：supervised=false，不碰真桥接、不发任何网络请求，
 * 只跑"装载 → 定时器 → 失效"这条崩溃路径。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

/** 捕获 console.error（插件所有留痕都走它），同时原样透传到 stderr 供测试抓取。 */
const traces = [];
const realError = console.error;
console.error = (...args) => {
	traces.push(args.map((a) => (typeof a === "string" ? a : String(a))).join(" "));
	realError(...args);
};

/** 记一行测试自己的结论。 */
function say(line) {
	console.log(`[harness] ${line}`);
}

function makeSettings(preference) {
	return {
		describe: () => [{ ns: "ui-theme", value: { preference } }],
		update: async () => {},
	};
}

/**
 * 假 cordis ctx。只实现插件用到的面：logger / get / on / effect / inject / timeout。
 * `timeout` 在 ctx 失效后**抛真机原文的那个错** —— 这是本 harness 的核心保真点。
 */
function makeCtx(label) {
	const state = { label, inactive: false, cleanups: [], cancelled: 0, live: new Set(), settings: makeSettings("light") };
	const logger = { info: () => {}, warn: () => {}, error: () => {}, debug: () => {} };
	const child = {
		label,
		logger,
		get: (name) => (name === "settings" ? state.settings : undefined),
		on: () => () => {},
		effect: () => () => {},
	};
	const ctx = {
		label,
		logger,
		get: (name) => (name === "settings" ? state.settings : undefined),
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
			if (state.inactive) {
				throw new Error('cannot get required service "timer" in inactive context');
			}
			const timer = setTimeout(() => {
				state.live.delete(timer);
				callback();
			}, delay);
			state.live.add(timer);
			return () => {
				state.cancelled += 1;
				clearTimeout(timer);
				state.live.delete(timer);
			};
		},
	};
	return { ctx, state };
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const A = makeCtx("A");
const B = makeCtx("B");

// ── A：装载 → fiber 失效（清理没跑）→ 等 5s 轮询到期 ────────────────────────────
apply(A.ctx);
say("A 已装载（会话 #1）");
await sleep(150);

A.state.inactive = true; // fiber 没了：之后任何 ctx.timeout 都会抛
let throwsWhenInactive = false;
try {
	A.ctx.timeout(() => {}, 1);
} catch (error) {
	throwsWhenInactive = error.message.includes("inactive context");
}
if (!throwsWhenInactive) {
	console.log("HARNESS FAIL 假 ctx 失效后没有抛错（harness 失去保真度）");
	process.exit(1);
}
say("假 ctx 失效后确实抛 inactive context（保真）");

await sleep(5400); // 轮询在 5s 到期，这里必须还活着
const staleTraced = traces.some((line) => line.includes("定时器 主题轮询 排不了"));
if (!staleTraced) {
	console.log("HARNESS FAIL 失效 ctx 上的轮询没有留下护栏痕（护栏没生效？）");
	process.exit(1);
}
console.log("HARNESS STALE POLL OK");

// ── B：再装载 → 正常 fiber 销毁（清理跑）→ 定时器必须被主动取消 ──────────────────
apply(B.ctx);
say("B 已装载（会话 #2）");
await sleep(300);
for (const cleanup of B.state.cleanups) {
	try {
		cleanup();
	} catch (error) {
		realError(`[harness] 清理回调抛错：${error.message}`);
	}
}
await sleep(200);
const sessionEnded = traces.some((line) => line.includes("装载会话 #2 结束（fiber 销毁）"));
if (!sessionEnded) {
	console.log("HARNESS FAIL 会话没有被 fiber 销毁收掉（effect 清理没挂上？）");
	process.exit(1);
}
if (B.state.cancelled < 1) {
	console.log("HARNESS FAIL fiber 销毁后定时器没有被主动取消（timer 服务不会替我们取消）");
	process.exit(1);
}
console.log("HARNESS SESSION END OK");
console.log("HARNESS OK");
process.exit(0);
