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

/** webserver/index-inject 钩子：0.2.6 起服务端插件该往页面注入桥接坐标（即时通道）。 */
let injected = null;

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
	on: (event, listener, options) => {
		if (event !== "webserver/index-inject") return () => {};
		const table = [];
		listener(table);
		injected = { rows: table, prepend: options?.prepend === true };
		return () => {};
	},
	inject: (deps, cb) => {
		if (Array.isArray(deps) && deps.includes("settings")) {
			cb({ get: (n) => (n === "settings" ? settings : undefined), on: () => undefined });
		}
		// "connection" 故意不注入：模拟非 web 组合（本测试不覆盖 embed 放宽）。
	},
};

apply(ctx);

// 0.2.6 即时通道的注入契约：body 脚本 + prepend，脚本里坐标要齐（页面半边靠它连桥）。
if (injected === null) {
	console.error("HARNESS FAIL: 没有注册 webserver/index-inject 注入");
	process.exit(1);
}
const injectedRow = injected.rows.find((row) => row.kind === "script" && row.placement === "body");
if (injectedRow === void 0 || injected.prepend !== true) {
	console.error(`HARNESS FAIL: 注入行形状不对 ${JSON.stringify(injected)}`);
	process.exit(1);
}
if (!injectedRow.text.startsWith("window.__DSH_LAUNCHER_BRIDGE__ = ")) {
	console.error(`HARNESS FAIL: 注入脚本不是桥接坐标 ${injectedRow.text}`);
	process.exit(1);
}
const injectedCoordinates = JSON.parse(
	injectedRow.text.slice("window.__DSH_LAUNCHER_BRIDGE__ = ".length, injectedRow.text.lastIndexOf(";")),
);
for (const key of ["url", "token", "instanceId", "launchId"]) {
	if (typeof injectedCoordinates[key] !== "string" || injectedCoordinates[key] === "") {
		console.error(`HARNESS FAIL: 注入坐标缺 ${key}: ${injectedRow.text}`);
		process.exit(1);
	}
}
console.log(`HARNESS INJECT OK (${injectedCoordinates.url})`);

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
