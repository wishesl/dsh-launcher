/**
 * 端到端契约测试用的最小 ctx —— 专测最要命的那条链路：**插件侧取不到 appExit 时，
 * dsh-restart 也必须真的把进程退掉**。
 *
 * 由 launcher_bridge_plugin_test.go 的 TestBridgeRestartEndToEndWithRealPlugin 启动：
 * 它故意让三条取法全部失败（inject 不回调、get("appExit") 返回 undefined、没有 appExit
 * 属性），复现 2026-10-01 真机事故 —— launcher 已经 ack 了，插件却静默 no-op，进程一直
 * 活着，用户看到的就是"点了重启没反应"。
 *
 * 跑的是**真正被内嵌的插件代码**；假 ctx 只覆盖契约必要的表面，不模拟 DSH 内部实现。
 * 输出直接写 stdout（子进程 stdout 落临时文件，进程 exit 前同步落盘），Go 侧据此断言。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

const logs = [];
const record = (level) => (msg) => {
	logs.push(`${level}: ${msg}`);
	console.log(`${level}: ${msg}`);
};

/** 插件注册的 dsh-restart 工具规格（apply 之后从这里手动执行一遍）。 */
let registered = null;

const ctx = {
	logger: {
		info: record("info"),
		warn: record("warn"),
		error: record("error"),
		debug: record("debug"),
	},
	timeout: (fn, ms) => setTimeout(fn, ms),
	effect: (fn) => fn(),
	// 故意取不到 appExit：这条路径就是真机上失败的那条。
	get: () => undefined,
	tools: {
		register: (spec) => {
			registered = spec;
			return { dispose() {} };
		},
	},
	// 故意不回调任何服务（appExit / settings / connection 全部拿不到）。
	inject: () => undefined,
};

apply(ctx);

setTimeout(async () => {
	if (registered === null) {
		console.error("RESTART HARNESS FAIL: dsh-restart 工具没注册上");
		process.exit(1);
		return;
	}
	try {
		const result = await registered.execute(
			{ confirm: "restart-dsh", reason: "harness" },
			{ agent: { session: { id: "s-1", header: {} } }, callId: "c-1" }
		);
		// 能打印出 restarting 就说明 restart-result{ok:true} 已经回来了。
		console.log(`RESTART TOOL RESULT ${JSON.stringify(result)}`);
	} catch (error) {
		console.error(`RESTART HARNESS FAIL: execute 抛错 ${error.message}`);
		process.exit(1);
		return;
	}
	// 之后进程该由插件自己退掉（本次故意拿不到 appExit，所以必须走 process.exit 兜底）。
	// 这个看门狗定时器只在"插件没退"时开火：退出码 1 让 Go 侧直接看出失败。
	setTimeout(() => {
		console.error(`RESTART HARNESS FAIL: 5s 内进程没有退出\n${logs.join("\n")}`);
		process.exit(1);
	}, 5000);
}, 600);
