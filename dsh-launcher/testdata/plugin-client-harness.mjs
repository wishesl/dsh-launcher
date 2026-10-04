/**
 * plugin-client-harness.mjs — 用**真实的** lib/client.js 跑一遍页面侧契约：
 *
 *   1) 模块契约：window.__ModuleLoader__.load 的注册形状（id 必须是包名）+ apply/inject 导出；
 *   2) 设置面板（1.1 起）：settings.section 的注册形态 + inject 业务面（readStatus/subscribeStatus/
 *      reconnect）+ 组件能真渲染（react-dom/server 冒烟，取不到 react 就跳过）；
 *   3) fail-open：没有桥接坐标、slots 服务形状不认识、slots.inject 不回调 —— 都只是"没有面板"，
 *      apply 绝不抛错（主题通道不该被面板拖死）；
 *   4) 真 WebSocket 连 launcher 桥：page-hello 握手 + 收到 page-set-theme 就调 theme.setTheme
 *      + page-result 回报 + theme/change 立即回报 page-theme + 面板状态跟着变成 online。
 *
 * 结论全部打到 stdout（Go 侧落临时文件读，沙箱不允许管道捕获子进程输出）。
 * 桥接坐标由 Go 侧经 env CLIENT_HARNESS_BRIDGE（JSON）传入，这里塞进 window 全局，
 * 等价于服务端插件 webserver/index-inject 注入的那段脚本。
 */
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";

const CODE = readFileSync(new URL("../embed/dsh-launcher-plugin/lib/client.js", import.meta.url), "utf8");
const bridge = JSON.parse(process.env.CLIENT_HARNESS_BRIDGE ?? "{}");
/** "gate" = 只跑内嵌门禁那一段（见该段末尾的 process.exit）。 */
const ONLY = process.env.CLIENT_HARNESS_ONLY ?? "";

function fail(message) {
	console.log(`HARNESS FAIL: ${message}`);
	process.exit(1);
}

function ok(message) {
	console.log(`HARNESS ${message}`);
}

/**
 * 载入一份**独立的** client.js 模块实例（每次调用都是一套新的模块级状态）。
 * @param bridgeCoordinates - 注入页面的桥接坐标（缺省 = 没有即时通道）。
 * @param options - `framed: true` = 模拟"被启动器内嵌视图的 iframe 打开"（`self !== top`）。
 *                  **缺省 = 顶层窗口**（外部标签页）—— 0.2.13 的内嵌门禁会拦住它，所以需要
 *                  真连桥接的用例必须显式写 `{ framed: true }`，忘了写就是连不上（安全方向）。
 * @returns factory（模块导出工厂）。
 */
function loadModule(bridgeCoordinates, options = {}) {
	const win = {};
	// 内嵌门禁按 `self !== top` 判断：顶层窗口两者是同一个对象，iframe 里 top 是父窗口。
	win.self = win;
	win.top = options.framed === true ? {} : win;
	if (bridgeCoordinates !== void 0) win.__DSH_LAUNCHER_BRIDGE__ = bridgeCoordinates;
	let captured = null;
	win.__ModuleLoader__ = {
		load(spec) {
			captured = spec;
		},
	};
	new Function("window", CODE)(win);
	if (captured === null) fail("client.js 没有调用 __ModuleLoader__.load");
	if (captured.id !== "dsh-launcher-plugin") fail(`模块 id 必须是包名，得到 ${captured.id}`);
	return captured.factory;
}

/** 真 react / react-dom（来自前端 node_modules）；取不到返回 null（对应断言可跳过）。 */
const nodeRequire = createRequire(import.meta.url);
function loadReact() {
	try {
		const react = nodeRequire("../frontend/node_modules/react");
		const server = nodeRequire("../frontend/node_modules/react-dom/server.node.js");
		if (typeof react.createElement !== "function" || typeof server.renderToStaticMarkup !== "function") return null;
		return { react, renderToStaticMarkup: server.renderToStaticMarkup };
	} catch {
		return null;
	}
}
const reactEnv = loadReact();

/**
 * 假 ctx：slots / locale / theme 三个服务 + effect/on/get。
 * @param mode - "panel"（slots.inject 立即回调）/ "silent"（记录但不回调）/ "none"（连 slots 都没有）。
 * @param navLabel - 假 locale 里 `nav` 这条文案（导航行认领按它匹配；传 "" 模拟语言还没解析出来）。
 * @returns 假 ctx、注册记录与 effect disposer。
 */
function makeCtx(mode, navLabel = "启动器") {
	const registrations = [];
	const listeners = [];
	const applied = [];
	const disposers = [];
	const theme = {
		getTheme: () => ({ preference: "dark" }),
		setTheme(id) {
			applied.push(id);
			for (const listener of listeners) listener({ preference: id });
		},
	};
	const ctx = {
		theme,
		get(name) {
			if (name === "theme") return theme;
			return void 0;
		},
		on(_event, listener) {
			listeners.push(listener);
			return () => {};
		},
		effect(fn) {
			const disposer = fn();
			if (typeof disposer === "function") disposers.push(disposer);
			return disposer;
		},
		logger: { info() {}, warn() {} },
	};
	if (mode !== "none") {
		ctx.slots = {
			inject(_key, callback) {
				if (mode === "panel") return callback();
				return () => {};
			},
			register(options, component) {
				registrations.push({ options, component });
				return () => {};
			},
		};
		ctx.locale = {
			// `nav` 这条要跟真环境一致（导航行认领按行文本比对），其余键给可辨认的桩值。
			bind: () => (key) => (key === "nav" ? navLabel : `T(${key})`),
			register: () => () => {},
		};
	}
	return { ctx, registrations, listeners, applied, disposers };
}

/**
 * 假 DOM：3 条导航行 + 一个 style 标签记录器（只覆盖客户端半边用到的那几个 API）。
 * @param rowTexts - 三条导航行的可见文本。
 * @returns 假 document、行对象与 style 标签数组。
 */
function makeFakeDom(rowTexts) {
	const styleTags = [];
	const rows = rowTexts.map((text) => {
		const attrs = new Map();
		return {
			textContent: text,
			attrs,
			setAttribute: (name, value) => attrs.set(name, value),
			removeAttribute: (name) => attrs.delete(name),
			hasAttribute: (name) => attrs.has(name),
		};
	});
	const doc = {
		body: { tagName: "BODY" },
		head: {
			appendChild(tag) {
				styleTags.push(tag);
			},
		},
		createElement(tagName) {
			return {
				tagName,
				dataset: {},
				textContent: "",
				removed: false,
				remove() {
					this.removed = true;
				},
			};
		},
		querySelector() {
			return null; // 只被 installStyles 的"按 tag 去重"用到：一开始没有
		},
		querySelectorAll(selector) {
			if (selector === '[role="dialog"] nav button') return rows;
			if (selector === "[data-dsh-launcher-nav-icon]") return rows.filter((r) => r.hasAttribute("data-dsh-launcher-nav-icon"));
			return [];
		},
	};
	return { doc, rows, styleTags };
}

/** 假 MutationObserver：记录回调与 observe 参数，暴露 disconnect。 */
function makeFakeObserver() {
	const instances = [];
	class FakeMutationObserver {
		constructor(callback) {
			this.callback = callback;
			this.disconnected = false;
			instances.push(this);
		}
		observe(target, options) {
			this.target = target;
			this.options = options;
		}
		disconnect() {
			this.disconnected = true;
		}
	}
	return { FakeMutationObserver, instances };
}

/** 面板注册的契约断言（两种 ctx 模式共用）。 */
function assertPanelRegistration(registrations) {
	if (registrations.length !== 1) fail(`设置分区该注册 1 次，得到 ${registrations.length}`);
	const { options, component } = registrations[0];
	if (options.name !== "settings.section") fail(`注册的槽该是 settings.section，得到 ${options.name}`);
	if (options.id !== "dsh-launcher") fail(`分区 id 该是 dsh-launcher，得到 ${options.id}`);
	if (typeof options.order !== "number") fail("分区 order 必须是数字");
	if (typeof options.label !== "function" || options.label() === "") fail("分区 label 必须返回非空文案");
	if (options.locale !== "dsh-launcher-bridge") fail(`分区 locale 该是 dsh-launcher-bridge，得到 ${options.locale}`);
	if (typeof options.inject !== "function") fail("分区 inject 必须是业务面工厂");
	if (typeof component !== "function") fail("分区组件必须是个函数");
	const face = options.inject();
	for (const name of ["readStatus", "subscribeStatus", "reconnect"]) {
		if (typeof face[name] !== "function") fail(`inject 业务面缺 ${name}`);
	}
	if (typeof face.readStatus() !== "object") fail("readStatus 必须返回状态对象");
	if (typeof face.subscribeStatus(() => {}) !== "function") fail("subscribeStatus 必须返回退订函数");
	return face;
}

// ---------------------------------------------------------------------------
// 1) 模块契约
// ---------------------------------------------------------------------------
{
	let factory = null;
	try {
		factory = loadModule(bridge);
	} catch (error) {
		fail(`载入模块失败：${error?.message ?? error}`);
	}
	const exports = factory((name) => {
		throw new Error(`客户端半边不该 require 计划外的包：${name}`);
	});
	ok(`ID OK (${JSON.stringify(exports.inject)})`);
	if (typeof exports.apply !== "function") fail("没有导出 apply");
	if (JSON.stringify(exports.inject) !== JSON.stringify(["theme", "slots", "locale"])) {
		fail(`inject 该是 ["theme","slots","locale"]，得到 ${JSON.stringify(exports.inject)}`);
	}
}

// ---------------------------------------------------------------------------
// 2) fail-open：没有坐标 / slots 形状不认识 / slots.inject 不回调
// ---------------------------------------------------------------------------
{
	const cases = [
		{ mode: "panel", coordinates: void 0, marker: "PANEL ABSENT OK" },
		{ mode: "silent", coordinates: void 0, marker: "SLOTS SILENT OK" },
		{ mode: "none", coordinates: void 0, marker: "NO-SLOTS OK" },
	];
	for (const item of cases) {
		const exports = loadModule(item.coordinates)((name) => {
			throw new Error(`客户端半边不该 require 计划外的包：${name}`);
		});
		const { ctx, registrations } = makeCtx(item.mode);
		try {
			exports.apply(ctx);
		} catch (error) {
			fail(`${item.marker}：apply 抛错了 ${error?.message ?? error}`);
		}
		if (item.mode === "panel") {
			const face = assertPanelRegistration(registrations);
			const status = face.readStatus();
			if (status.transport !== "absent") fail(`没有坐标时通道该是 absent，得到 ${status.transport}`);
			if (typeof status.reason !== "string" || status.reason === "") fail("没有坐标时必须给一句原因");
		} else if (registrations.length !== 0) {
			fail(`${item.marker}：不该注册分区，却注册了 ${registrations.length} 次`);
		}
		ok(item.marker);
	}
}

// ---------------------------------------------------------------------------
// 4) 设置导航行认领（假 DOM + 假 MutationObserver）：官方没有图标位，只能自己认领
// ---------------------------------------------------------------------------
{
	const install = (navLabel, rowTexts) => {
		const { doc, rows, styleTags } = makeFakeDom(rowTexts);
		const { FakeMutationObserver, instances } = makeFakeObserver();
		globalThis.document = doc;
		globalThis.MutationObserver = FakeMutationObserver;
		const exports = loadModule(void 0)((name) => {
			throw new Error(`客户端半边不该 require 计划外的包：${name}`);
		});
		const { ctx, disposers } = makeCtx("panel", navLabel);
		exports.apply(ctx);
		return { rows, styleTags, observers: instances, disposers };
	};

	try {
		// ① 只认领文本等于我们 label 的那一行，并注入藏齿轮 + 画 logo 的 CSS
		const a = install("启动器", ["通用设置", "启动器", "插件市场"]);
		const marked = a.rows.filter((r) => r.hasAttribute("data-dsh-launcher-nav-icon"));
		if (marked.length !== 1) fail(`该只认领 1 行，得到 ${marked.length}`);
		if (marked[0].textContent !== "启动器") fail(`认领错了行：${marked[0].textContent}`);
		if (a.styleTags.length !== 2) fail(`该注入 2 个 style 标签（面板 + 导航图标），得到 ${a.styleTags.length}`);
		const navTag = a.styleTags.find((t) => t.dataset.pluginCss === "dsh-launcher-plugin/nav-icon.css");
		const panelTag = a.styleTags.find((t) => t.dataset.pluginCss === "dsh-launcher-plugin/section.css");
		if (navTag === void 0) fail("没有注入导航图标那段 style");
		if (panelTag === void 0) fail("没有注入面板那段 style");
		const css = navTag.textContent;
		if (!css.includes("[data-dsh-launcher-nav-icon] > svg{display:none}")) fail("CSS 该藏掉官方那个兜底齿轮");
		if (!css.includes('background-image:url("data:image/png;base64,')) fail("CSS 该用内联 logo 做 ::before 背景");
		if (a.observers.length !== 1) fail(`该挂 1 个 MutationObserver，得到 ${a.observers.length}`);
		const observer = a.observers[0];
		if (observer.options?.subtree !== true || observer.options?.characterData !== true) fail("观察者参数不对");
		ok("NAV CLAIM OK");

		// ② 重渲染 / 切语言（行文本变了）→ 观察者回调后重新认领
		a.rows[0].textContent = "启动器";
		a.rows[1].textContent = "Launcher";
		observer.callback();
		await new Promise((resolve) => setTimeout(resolve, 0)); // 让 queueMicrotask 的合并落地
		const reclaimed = a.rows.filter((r) => r.hasAttribute("data-dsh-launcher-nav-icon"));
		if (reclaimed.length !== 1 || reclaimed[0].textContent !== "启动器") fail("重渲染后没有重新认领");
		ok("NAV RECLAIM OK");

		// ③ fiber 销毁：marker 摘干净、导航图标那段 style 移除、观察者断开
		//（面板样式是模块级、随插件存活，所以它不随这次 effect 消失。）
		for (const dispose of a.disposers) dispose();
		if (a.rows.some((r) => r.hasAttribute("data-dsh-launcher-nav-icon"))) fail("清理后 marker 没摘干净");
		if (navTag.removed !== true) fail("清理后导航图标那段 style 该被移除");
		if (panelTag.removed === true) fail("面板样式不该随导航图标一起移除");
		if (observer.disconnected !== true) fail("清理后观察者该断开");
		ok("NAV CLEANUP OK");

		// ④ 语言还没解析出来（label 为空）时一条都不标 —— 不能把整个 nav 认成自己的
		const b = install("", ["通用设置", "启动器", "插件市场"]);
		if (b.rows.some((r) => r.hasAttribute("data-dsh-launcher-nav-icon"))) fail("label 为空时不该认领任何行");
		ok("NAV EMPTY-LABEL OK");
	} finally {
		delete globalThis.document;
		delete globalThis.MutationObserver;
	}
}

// ---------------------------------------------------------------------------
// 2.5) 内嵌门禁（0.2.13）：外部标签页（顶层窗口）即使拿到坐标也不连桥接
// ---------------------------------------------------------------------------
{
	const exports = loadModule(bridge)((name) => {
		throw new Error(`客户端半边不该 require 计划外的包：${name}`);
	});
	// 断言"到底有没有建 socket"最直接：把 WebSocket 换成一个只记数的桩。
	const RealWebSocket = globalThis.WebSocket;
	let constructed = 0;
	globalThis.WebSocket = class {
		constructor() {
			constructed += 1;
		}
	};
	try {
		const { ctx, registrations } = makeCtx("panel");
		try {
			exports.apply(ctx);
		} catch (error) {
			fail(`EXTERNAL：apply 抛错了 ${error?.message ?? error}`);
		}
		const face = assertPanelRegistration(registrations);
		const status = face.readStatus();
		if (status.transport !== "absent") fail(`外部标签页该停在 absent，得到 ${status.transport}`);
		if (status.external !== true) fail("外部标签页必须在状态里标出 external");
		if (typeof status.reason !== "string" || status.reason === "") fail("外部标签页必须给一句原因");
		// 坐标仍是注入过的：证明是**门禁**拦下的，不是"没有坐标"那条路。
		if (status.coord === null || status.coord.instanceId !== bridge.instanceId) {
			fail("外部标签页仍该展示注入的坐标");
		}
		if (constructed !== 0) fail(`外部标签页不该建 WebSocket，却建了 ${constructed} 个`);
		// 门禁不是"先连上再断开"：没有重连定时器，等一会儿也不该冒出连接。
		await new Promise((resolve) => setTimeout(resolve, 700));
		if (face.readStatus().transport !== "absent") fail("外部标签页过一会儿也不该转成 connecting/online");
		if (constructed !== 0) fail(`外部标签页不该重连，却建了 ${constructed} 个 WebSocket`);
		ok("EXTERNAL SKIPPED OK");
		// gate 模式：只跑这一段就收工（Go 侧用它单独盯"桥接上从头到尾没有 page 连接"）。
		if (ONLY === "gate") {
			ok("OK");
			process.exit(0);
		}
	} finally {
		globalThis.WebSocket = RealWebSocket;
	}
}

// ---------------------------------------------------------------------------
// 3) 真桥接：面板注册 + 组件渲染 + 主题即时通道
// ---------------------------------------------------------------------------
let face = null;
{
	const exports = loadModule(bridge, { framed: true })((name) => {
		if (name === "react") {
			if (reactEnv === null) throw new Error("harness 里没有可用的 react");
			return reactEnv.react;
		}
		throw new Error(`客户端半边不该 require 计划外的包：${name}`);
	});
	const { ctx, registrations, listeners, applied } = makeCtx("panel");
	exports.apply(ctx);
	ok("APPLY OK");
	face = assertPanelRegistration(registrations);
	if (face.readStatus().transport !== "connecting") {
		fail(`坐标在场时通道该先进入 connecting，得到 ${face.readStatus().transport}`);
	}
	ok("PANEL OK");

	// 组件冒烟：真 react-dom 渲染一次（用一份合成快照，避开时序），取不到 react 就跳过。
	if (reactEnv === null) {
		ok("SSR SKIPPED（前端 node_modules 里没有 react/react-dom）");
	} else {
		const synthetic = {
			transport: "offline",
			reason: "测试快照",
			retry: 4,
			connectedAt: 0,
			lastFrameAt: 0,
			bridgeHost: "127.0.0.1:1234",
			theme: "dark",
			coord: { instanceId: "inst-a", launchId: "L123456789", plugin: "dsh-launcher-plugin", pluginVersion: "0.2.11" },
		};
		let html = "";
		try {
			html = reactEnv.renderToStaticMarkup(reactEnv.react.createElement(registrations[0].component, {
				t: (key) => `T(${key})`,
				readStatus: () => synthetic,
				subscribeStatus: () => () => {},
				reconnect: () => false,
			}));
		} catch (error) {
			fail(`组件渲染抛错：${error?.message ?? error}`);
		}
		if (!html.includes("T(title)")) fail("渲染结果里没有标题");
		if (!html.includes("dshl-panel-logo")) fail("面板页头该有启动器 logo");
		if (!html.includes("data:image/png;base64,")) fail("面板页头 logo 该走内联 data URI");
		if (!html.includes("T(feat.pageTheme.title)")) fail("渲染结果里没有功能卡片");
		// 回归锁：0.2.14 新增的会话通知必须出现在「功能展示」清单里（曾经只改了服务端半边，
		// 面板是静态清单，于是用户在 DSH 设置里看不到这项）。
		if (!html.includes("T(feat.sessionNotify.title)")) fail("功能清单里没有「回答完成 / 提问通知」");
		if (!html.includes("T(feat.sessionNotify.need)")) fail("会话通知卡片缺「前提」文案");
		if (!html.includes("127.0.0.1:1234")) fail("渲染结果里没有桥接地址");
		if (!html.includes("T(hint.launcherRestarted)")) fail("离线重连≥3 次时该提示重启实例");
		if (html.includes("disabled")) fail("离线时「重新连接」按钮不该禁用");
		if (html.includes("is-bad")) fail("通道只是断开重连（主题仍走服务端写入）时不该标红");
		// 真在线时该出绿色档（同一份快照换成 online 再渲染一次）。
		const online = { ...synthetic, transport: "online", reason: "", retry: 0, connectedAt: Date.now(), lastFrameAt: Date.now() };
		const onlineHtml = reactEnv.renderToStaticMarkup(reactEnv.react.createElement(registrations[0].component, {
			t: (key) => `T(${key})`,
			readStatus: () => online,
			subscribeStatus: () => () => {},
			reconnect: () => false,
		}));
		if (!onlineHtml.includes("is-ok")) fail("在线时通道该是绿色档");
		// 外部标签页的快照：该出 external 提示，且仍是中性档（不是故障、不标红）。
		const external = { ...synthetic, transport: "absent", reason: "外部标签页", retry: 0, external: true };
		const externalHtml = reactEnv.renderToStaticMarkup(reactEnv.react.createElement(registrations[0].component, {
			t: (key) => `T(${key})`,
			readStatus: () => external,
			subscribeStatus: () => () => {},
			reconnect: () => false,
		}));
		if (!externalHtml.includes("T(hint.externalPage)")) fail("外部标签页快照该给出提示");
		if (externalHtml.includes("is-bad")) fail("外部标签页不接通道是设计，不该标红");
		ok("SSR OK");
	}

	// 等 launcher 下发 page-set-theme（Go 侧在握手后调 SetThemePreference）。
	const deadline = Date.now() + 8000;
	while (applied.length === 0 && Date.now() < deadline) {
		await new Promise((resolve) => setTimeout(resolve, 20));
	}
	if (applied.length === 0) fail("8s 内没有收到 page-set-theme");
	ok(`SET THEME ${applied.join(",")}`);

	// 面板状态：真连上以后该是 online，且桥接地址/坐标/收帧时刻都填上了。
	const status = face.readStatus();
	if (status.transport !== "online") fail(`通道该是 online，得到 ${status.transport}`);
	if (status.lastFrameAt <= 0) fail("online 之后必须记录 lastFrameAt");
	if (status.bridgeHost === "") fail("必须有桥接地址");
	if (status.coord === null || status.coord.instanceId !== bridge.instanceId) fail("坐标里的 instanceId 对不上");
	if (status.theme !== "dark") fail(`主题该跟随 theme 服务（dark），得到 ${status.theme}`);
	if (face.reconnect() !== false) fail("通道健康时 reconnect 该是 no-op");
	ok("STATUS OK");

	// 模拟"用户在 DSH 页面里点了另一个主题"：客户端半边该立刻回报 page-theme。
	await new Promise((resolve) => setTimeout(resolve, 100));
	for (const listener of listeners) listener({ preference: "light" });
	ok("REPORTED light");
	await new Promise((resolve) => setTimeout(resolve, 200));
}

ok("OK");
process.exit(0);
