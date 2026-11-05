/**
 * 内嵌授权放宽（embedRelax）的契约测试：跑**真插件代码**（不是复刻逻辑），
 * 用假 connection service 复现上游各种请求形状，断言：
 *   1) 放宽判据命中的请求（内嵌文档自己调的 /api、启动器页面发的白名单来源）能拿到 peer；
 *   2) 陌生来源（evil.example）仍然被拒 —— 放宽只免"认证"，不放宽来源判定；
 *   3) 翻车形状（admit 自带逻辑、且拿不到 browserPeer）必须**判负**（能力置 false）；
 *   4) index 换成 Fetch `Request`（0.2.2-rc.1）时，跨站 + 有效 token 也必须被放行，
 *      且错误 token / 同站请求不得放宽。
 *
 * 为什么需要这个 harness：0.2.1-alpha.5 起上游把 /api 的准入从
 * `admit() → requestRejection()` 反成 `requestRejection() → admit()`，只覆盖
 * `requestRejection` 就成了死代码，症状是内嵌视图每条 /api 401、web boot 报
 * `1 entry did not activate`（0.2.1-rc.1 实测）；0.2.2-rc.1 又把 index 请求包成
 * Fetch `Request`（头是 `Headers` 实例），按普通对象取头恒为 undefined → 整页 401
 * `dsh web authentication required`。两条回归都必须被测试钉住。
 *
 * 用法：node --import ./testdata/plugin-test-register.mjs testdata/plugin-embed-harness.mjs <new|old|fetch|nopeer>
 * 由 launcher_bridge_plugin_test.go 的 TestEmbedRelaxCapabilityAcrossUpstreamShapes 驱动（带桥接 env，
 * 所以能力会随握手上报给 Go 侧断言）。
 */
import { apply } from "../embed/dsh-launcher-plugin/lib/index.js";

const mode = process.argv[2] ?? "new";
const logs = [];
const record = (level) => (msg) => {
	logs.push(`${level}: ${msg}`);
};

/** 三类请求：内嵌文档自身（Origin 与 Host 同源）/ 启动器页面（白名单来源）/ 陌生来源。 */
const SAME_HOST_REQUEST = {
	method: "POST",
	url: "/api",
	headers: { host: "127.0.0.1:3080", origin: "http://127.0.0.1:3080" },
};
const LAUNCHER_PAGE_REQUEST = {
	method: "POST",
	url: "/api",
	headers: { host: "127.0.0.1:3080", origin: "http://wails.localhost", "sec-fetch-site": "cross-site" },
};
const STRANGER_REQUEST = {
	method: "POST",
	url: "/api",
	headers: { host: "127.0.0.1:3080", origin: "http://evil.example" },
};

/**
 * 0.2.1-alpha.5 / 0.2.1-rc.1 的形状（实测源码 dsh-client-connection/lib/index.js）：
 * admit 自己查 cookie 并返回 browserPeer，requestRejection 只是它的包装。
 */
function newShapeConnection() {
	const browserPeer = { id: "browser-peer" };
	return {
		browserPeer,
		browserAuth: {
			launchToken: "LAUNCH",
			/** 跨站 iframe 里 SameSite=Strict 会话 cookie 永远不来 —— 恒 false。 */
			isAuthenticated: () => false,
			authenticateApi: () => void 0,
		},
		authorizeIndex: () => false,
		requestRejection(request) {
			const admission = this.admit(request);
			return "rejection" in admission ? admission.rejection : void 0;
		},
		admit(request) {
			const host = request.headers.host ?? "";
			if (!host.startsWith("127.0.0.1")) return { rejection: 403 };
			if (this.browserAuth.isAuthenticated(request)) return { peer: browserPeer };
			return { rejection: 401 };
		},
	};
}

/**
 * 0.2.0-rc.2 / 0.2.1-alpha.2 的形状：admit 委托 requestRejection（覆盖后者即生效），
 * 老版本必须继续能用 —— 补丁不能只为新形状服务。
 */
function oldShapeConnection() {
	const operatorPeer = { id: "operator-peer" };
	return {
		browserAuth: {
			launchToken: "LAUNCH",
			isAuthenticated: () => false,
		},
		authorizeIndex: () => false,
		requestRejection(request) {
			const host = request.headers.host ?? "";
			if (!host.startsWith("127.0.0.1")) return 403;
			return this.browserAuth.isAuthenticated(request) ? void 0 : 401;
		},
		admit(request) {
			const rejection = this.requestRejection(request);
			return rejection === void 0 ? { peer: operatorPeer } : { rejection };
		},
	};
}

/** 第三种：admit 自带逻辑却不给 peer（模拟上游再改一次）→ 行为探测必须判负。 */
function noPeerConnection() {
	return {
		browserAuth: {
			launchToken: "LAUNCH",
			isAuthenticated: () => false,
		},
		authorizeIndex: () => false,
		requestRejection: () => 401,
		admit: () => ({ rejection: 401 }),
	};
}

/**
 * 0.2.2-rc.1 的形状：`/api` 与 `admit` 同前，但 **index 请求换成 Fetch `Request`**
 * （`frontend-static` 的 `serveResource` 里 `resource(new Request(target, { headers }))`），
 * 头是 `Headers` 实例、响应是 `{ writeHead, end }` 壳。
 *
 * 这一形状的翻车点不在方法在不在：`authorizeIndex` 还在、`sec-fetch-site` 也照发，
 * 但用 `headers["sec-fetch-site"]` 取恒为 undefined → 补丁静默失效 → 303+cookie →
 * 跨站 iframe 带不上 cookie → 整页 401。所以这里也必须真跑一遍 index。
 */
function fetchShapeConnection() {
	return {
		...newShapeConnection(),
		authorizeIndex: () => false, // 上游真实现：token 有效则 303（返回 false），否则 401
	};
}

const CONNECTIONS = {
	new: newShapeConnection,
	old: oldShapeConnection,
	nopeer: noPeerConnection,
	fetch: fetchShapeConnection,
};
const makeConnection = CONNECTIONS[mode];
if (makeConnection === void 0) {
	console.error(`HARNESS FAIL: 未知 mode ${mode}（可选 ${Object.keys(CONNECTIONS).join(" / ")}）`);
	process.exit(2);
}

const conn = makeConnection();
const ctx = {
	logger: {
		info: record("info"),
		warn: record("warn"),
		error: record("error"),
		debug: record("debug"),
	},
	timeout: (fn, ms) => setTimeout(fn, ms),
	effect: (fn) => fn(),
	get: () => void 0,
	tools: { register: () => ({ dispose() {} }) },
	on: () => () => {},
	inject: (deps, cb) => {
		// 只有 connection 需要注入；其余（settings / appExit / timer…）本测试不覆盖。
		// 注入进来的 ctx 在真 cordis 里是完整 Context（带 logger），这里照着给。
		if (Array.isArray(deps) && deps.includes("connection")) {
			cb({ get: () => conn, logger: ctx.logger });
		}
	},
};

apply(ctx);

const failures = [];
const admits = (request) => {
	const admission = conn.admit(request);
	if (admission === void 0) return { kind: "void" };
	return "rejection" in admission ? { kind: "rejection", code: admission.rejection } : { kind: "peer" };
};
const expectPeer = (label, request) => {
	const got = admits(request);
	if (got.kind !== "peer") failures.push(`${label}: 期望拿到 peer，实际 ${JSON.stringify(got)}`);
};
const expectRejected = (label, request) => {
	const got = admits(request);
	if (got.kind !== "rejection") failures.push(`${label}: 期望被拒，实际 ${JSON.stringify(got)}`);
};
const expectLog = (label, needle) => {
	if (!logs.some((line) => line.includes(needle))) {
		failures.push(`${label}: 日志里没有「${needle}」\n${logs.join("\n")}`);
	}
};

expectRejected("陌生来源必须仍被拒", STRANGER_REQUEST);

if (mode === "fetch") {
	// index 必须按 Fetch Request 的形状真跑：跨站 + 有效 token → 直接放行（返回 true）。
	const shim = { writeHead() {}, end() {} };
	const indexRequest = (search, site) =>
		new Request(`http://127.0.0.1:3080/${search}`, {
			method: "GET",
			headers: new Headers({ host: "127.0.0.1:3080", "sec-fetch-site": site }),
		});
	const served = conn.authorizeIndex(indexRequest(`?token=${conn.browserAuth.launchToken}`, "cross-site"), shim);
	if (served !== true) {
		failures.push("跨站 iframe 的 index 请求（Fetch Request 形状）没被放行 —— 内嵌视图会整页 401");
	}
	const wrongToken = conn.authorizeIndex(indexRequest("?token=WRONG", "cross-site"), shim);
	if (wrongToken !== false) failures.push("错误 token 的 index 请求不该被放行");
	const sameSite = conn.authorizeIndex(indexRequest(`?token=${conn.browserAuth.launchToken}`, "same-origin"), shim);
	if (sameSite !== false) failures.push("同站 index 请求不该走跨站放宽（仍走原 303+cookie）");
	expectLog("index 放宽成功", "已放宽浏览器会话校验");
} else if (mode === "nopeer") {
	// 放宽装不上：请求拿不到 peer，插件必须把能力判负（否则内嵌入口不置灰，用户撞 401）。
	expectRejected("判负形状不得放行", SAME_HOST_REQUEST);
	expectLog("判负形状", "行为探测未通过");
} else {
	expectPeer("内嵌文档自身请求（Origin 与 Host 同源）", SAME_HOST_REQUEST);
	expectPeer("启动器页面请求（白名单来源）", LAUNCHER_PAGE_REQUEST);
	expectLog("放宽成功", "已放宽浏览器会话校验");
}

if (failures.length > 0) {
	console.error(`HARNESS FAIL (${mode}):\n- ${failures.join("\n- ")}\n--- logs ---\n${logs.join("\n")}`);
	process.exit(1);
}

console.log(`HARNESS OK embed-relax mode=${mode}`);
console.log(logs.join("\n"));

// 带桥接 env 时（Go 侧驱动），留一点时间让握手把 embedRelax 能力发出去再退出。
if (process.env.DSH_LAUNCHER_EVENTS) {
	setTimeout(() => process.exit(0), 1500);
} else {
	process.exit(0);
}
