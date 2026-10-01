/**
 * plugin-client-harness.mjs — 用**真实的** lib/client.js 跑一遍页面侧契约：
 *   window.__ModuleLoader__.load 的注册形状 + apply/inject 导出 + 真 WebSocket 连 launcher 桥
 *   + page-hello 握手 + 收到 page-set-theme 就调 theme.setTheme + page-result 回报
 *   + theme/change 立即回报 page-theme。
 *
 * 结论全部打到 stdout（Go 侧落临时文件读，沙箱不允许管道捕获子进程输出）。
 * 桥接坐标由 Go 侧经 env CLIENT_HARNESS_BRIDGE（JSON）传入，这里塞进 window 全局，
 * 等价于服务端插件 webserver/index-inject 注入的那段脚本。
 */
import { readFileSync } from "node:fs";

const bridge = JSON.parse(process.env.CLIENT_HARNESS_BRIDGE ?? "{}");
const window = { __DSH_LAUNCHER_BRIDGE__: bridge };

let factory = null;
window.__ModuleLoader__ = {
	load(spec) {
		if (spec.id !== "dsh-launcher-plugin") {
			console.log(`HARNESS FAIL: 模块 id 必须是包名，得到 ${spec.id}`);
			process.exit(1);
		}
		factory = spec.factory;
	},
};

const code = readFileSync(new URL("../embed/dsh-launcher-plugin/lib/client.js", import.meta.url), "utf8");
new Function("window", code)(window);
if (factory === null) {
	console.log("HARNESS FAIL: client.js 没有调用 __ModuleLoader__.load");
	process.exit(1);
}

const exports = factory((name) => {
	throw new Error(`客户端半边不该 require 外部包：${name}`);
});
console.log(`HARNESS ID OK (${JSON.stringify(exports.inject)})`);
if (typeof exports.apply !== "function") {
	console.log("HARNESS FAIL: 没有导出 apply");
	process.exit(1);
}
if (JSON.stringify(exports.inject) !== JSON.stringify(["theme"])) {
	console.log(`HARNESS FAIL: inject 该是 ["theme"]，得到 ${JSON.stringify(exports.inject)}`);
	process.exit(1);
}

const listeners = [];
const applied = [];
const ctx = {
	theme: {
		setTheme(id) {
			applied.push(id);
			for (const listener of listeners) listener({ preference: id });
		},
	},
	get(name) {
		return name === "theme" ? ctx.theme : void 0;
	},
	on(_event, listener) {
		listeners.push(listener);
		return () => {};
	},
	effect(fn) {
		return fn();
	},
	logger: { info() {}, warn() {} },
};

exports.apply(ctx);
console.log("HARNESS APPLY OK");

// 等 launcher 下发 page-set-theme（Go 侧在握手后调 SetThemePreference）。
const deadline = Date.now() + 8000;
while (applied.length === 0 && Date.now() < deadline) {
	await new Promise((resolve) => setTimeout(resolve, 20));
}
if (applied.length === 0) {
	console.log("HARNESS FAIL: 8s 内没有收到 page-set-theme");
	process.exit(1);
}
console.log(`HARNESS SET THEME ${applied.join(",")}`);

// 模拟"用户在 DSH 页面里点了另一个主题"：客户端半边该立刻回报 page-theme。
await new Promise((resolve) => setTimeout(resolve, 100));
for (const listener of listeners) listener({ preference: "light" });
console.log("HARNESS REPORTED light");
await new Promise((resolve) => setTimeout(resolve, 200));
console.log("HARNESS OK");
process.exit(0);
