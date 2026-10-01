/**
 * 回归（0.2.8）：重启完成的续跑负载必须能在**被拆掉的装载会话之后**补交。
 *
 * 真机事故（2026-10-02 01:27）：0.2.6 起 `dsh.client` 让每次启动都出现「装载会话 #1 → 拆掉 → #2」。
 * 负载常落在 #1 的 socket 上；旧实现 `pendingHandled` 一次闩死、重试又绑在 #1 的 ctx 上，
 * 会话一拆，交付再没发生过 —— 用户看到的就是"重启完没有续跑消息"（会话文件里最后一条
 * 注入停在 0.2.4 时代）。
 *
 * 由 launcher_bridge_plugin_test.go 的 TestBridgePendingRedeliveryAfterSessionReload 启动：
 * launcher 侧预置一份 pending，本 harness 复现同一时序：
 *   apply(ctx1) → 负载落到会话 #1 → 交付失败（sessionController 未就绪）→ 立刻 apply(ctx2)
 *   （beginSession 会收掉 #1 的定时器与连接，那一发 500ms 重试就此消失）→ **会话 #2 必须补交**，
 *   且整条链只允许成功注入一次。
 *
 * 跑的是真正被内嵌的插件代码；假 ctx 只覆盖契约必要的表面，不模拟 DSH 内部实现。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

const logs = [];
const record = (level) => (msg) => {
	logs.push(`${level}: ${msg}`);
	console.log(`${level}: ${msg}`);
};

/** 注入记录：整条链只允许成功一次（重复注入就是 bug）。 */
const delivered = [];

const settings = {
	describe() {
		return [{ ns: "ui-theme", revision: 1, value: { preference: "dark" } }];
	},
	async update() {},
};

/** 会话控制器：会话 #1 故意"还没就绪"（真机那一刻服务确实可能没挂齐），会话 #2 正常。 */
function controllerFor(label, fail) {
	return {
		async resolveAgent() {
			if (fail) throw new Error(`${label} 的 sessionController 还没就绪`);
			return {
				agent: {
					followup(message) {
						delivered.push({ label, message });
					},
				},
			};
		},
		async prompt() {
			throw new Error("不该走到 prompt 回退通道");
		},
	};
}

/** 每个装载会话的 fiber 清理函数（模拟 DSH 卸载这一行时触发的 effect dispose）。 */
const fiberDisposers = [];

function makeCtx(label, { fail }) {
	return {
		logger: {
			info: record(`${label}/info`),
			warn: record(`${label}/warn`),
			error: record(`${label}/error`),
			debug: record(`${label}/debug`),
		},
		// cordis 的 ctx.timeout 返回 disposer：插件把它记进装载会话，会话销毁时主动取消。
		timeout: (fn, ms) => {
			const timer = setTimeout(fn, ms);
			return () => clearTimeout(timer);
		},
		effect: (fn) => {
			const dispose = fn();
			if (typeof dispose === "function") fiberDisposers.push({ label, dispose });
			return () => {};
		},
		get: (name) => (name === "sessionController" ? controllerFor(label, fail) : undefined),
		tools: { register: () => ({ dispose() {} }) },
		on: () => () => {},
		inject: (deps, cb) => {
			if (Array.isArray(deps) && deps.includes("settings")) {
				cb({ get: (n) => (n === "settings" ? settings : undefined), on: () => undefined });
			}
		},
	};
}

apply(makeCtx("会话#1", { fail: true }));

// 200ms 后模拟宿主 Loader 的重载：新行装载（beginSession 收掉 #1），随后旧 fiber 销毁。
setTimeout(() => {
	apply(makeCtx("会话#2", { fail: false }));
	// 真机顺序是"新行先激活、旧行再销毁"：旧 fiber 的清理不能误伤新会话。
	for (const { label, dispose } of fiberDisposers) {
		if (label !== "会话#1") continue;
		try {
			dispose();
		} catch (error) {
			console.error(`HARNESS FAIL: 旧 fiber 清理抛错 ${error.message}`);
			process.exit(1);
		}
	}
}, 200);

// 等交付落地，再多等 800ms 抓"重复注入"。
const deadline = Date.now() + 8000;
const tick = setInterval(() => {
	if (delivered.length === 0) {
		if (Date.now() > deadline) {
			clearInterval(tick);
			console.error(`HARNESS FAIL: 8s 内会话 #2 没有补交续跑负载（旧实现就是死在这里）\n${logs.join("\n")}`);
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
		const { label, message } = delivered[0];
		if (label !== "会话#2") {
			console.error(`HARNESS FAIL: 交付落在 ${label}（该落在被拆掉之后的新会话上）`);
			process.exit(1);
		}
		const text = message?.content?.[0]?.text ?? "";
		if (!text.includes("[dsh-restart 完成]") || message?.source?.kind !== "plugin:dsh-launcher-plugin") {
			console.error(`HARNESS FAIL: 注入消息形态不对 ${JSON.stringify(message)}`);
			process.exit(1);
		}
		console.log(`HARNESS DELIVERED ON ${label}`);
		console.log("HARNESS SINGLE DELIVERY OK");
		console.log("HARNESS OK");
		process.exit(0);
	}, 800);
}, 50);
