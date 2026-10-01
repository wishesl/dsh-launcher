/**
 * 端到端契约测试用的最小 ctx —— 跑的是**真正被内嵌的插件代码**
 * （由 launcher_bridge_test.go 的 TestBridgeEndToEndWithRealPlugin 启动）。
 *
 * 它做的事：apply() 之后等 launcher 经 WebSocket 下发 set-theme，验证插件确实把值
 * 写进了 settings.update("ui-theme", …)。成功打印 HARNESS OK 并以 0 退出。
 * 假 settings / 假 ctx 只覆盖契约必要的表面，不模拟 DSH 内部实现。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

const logs = [];
const record = (level) => (msg) => {
	logs.push(`${level}: ${msg}`);
};

const settings = {
	revision: 3,
	value: { preference: "dark" },
	describe() {
		return [{ ns: "ui-theme", revision: this.revision, value: this.value }];
	},
	async update(ns, patch, expectedRevision) {
		if (expectedRevision !== undefined && expectedRevision !== this.revision) {
			throw new Error("SettingsConflictError");
		}
		if (ns !== "ui-theme") throw new Error(`unexpected ns ${ns}`);
		this.revision += 1;
		this.value = { ...this.value, ...patch };
		logs.push(`update: ${JSON.stringify(patch)} → ${JSON.stringify(this.value)} (rev=${this.revision})`);
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
	get: (name) => (name === "appExit" ? (code) => logs.push(`appExit(${code})`) : undefined),
	tools: { register: () => ({ dispose() {} }) },
	inject: (deps, cb) => {
		if (Array.isArray(deps) && deps.includes("settings")) {
			cb({ get: (n) => (n === "settings" ? settings : undefined), on: () => undefined });
		}
		// "connection" 故意不注入：模拟非 web 组合（本测试不覆盖 embed 放宽）。
	},
};

apply(ctx);

const deadline = Date.now() + 8000;
const tick = setInterval(() => {
	if (settings.value.preference === "light") {
		clearInterval(tick);
		console.log(`HARNESS OK ${JSON.stringify(settings.value)}`);
		console.log(logs.join("\n"));
		process.exit(0);
	}
	if (Date.now() > deadline) {
		clearInterval(tick);
		console.error(`HARNESS FAIL: set-theme 没有写进 settings\n${logs.join("\n")}`);
		process.exit(1);
	}
}, 50);
