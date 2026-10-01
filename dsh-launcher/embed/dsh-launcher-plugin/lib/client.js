/**
 * dsh-launcher-plugin — 客户端半边：DSH 网页端 ↔ launcher 的**主题即时通道**。
 *
 * 为什么要有它：DSH 的主题偏好是服务端的一个配置项（ui-theme）。从 launcher 改主题，页面要等
 * DSH 自己把配置写进共享 profile（抢文件锁 + 与 HMR 互斥 + 原子写 + 重算插件补丁 + 全量
 * describe），真机实测 330–350ms 才变色；反过来在页面里点主题，launcher 也要等这次写盘完成才
 * 知道。而页面里的 `theme` 服务本来就有一条乐观路径（setTheme 先换肤、写盘放后台），只是外部
 * 调用者用不上它。
 *
 * 所以：服务端插件（lib/index.js）把桥接坐标注入页面（webserver/index-inject），这里直接连
 * launcher 的 loopback 桥（page 角色，只认主题帧），做两件事 ——
 *
 *   launcher → 页面：收到 page-set-theme 就调 `theme.setTheme()`，页面当场换肤，写盘交给 DSH
 *                    在后台完成（不重复写：launcher 侧对这个实例不再发服务端 set-theme）；
 *   页面 → launcher：`theme/change` 一响就回报 page-theme，launcher 当场跟上（省掉那 ~180ms）。
 *
 * 全部 fail-open：坐标没注入、连不上、被 launcher 拒绝、theme 服务取不到 —— 都只是退回服务端
 * 写入那条慢路（主题照样同步），并且 launcher 会按 page-result 里的偏好值自己补一次写入，
 * 所以"点了没反应"不会发生。这里绝不假装成功。
 *
 * 装载方式：DSH 启动时扫 host Loader 的 entries，按 package.json 的 `dsh.client` + `exports["./client"]`
 * 自动发现本文件（模块 id 必须是 package.json 的 name），所以**升级插件后要重启一次 DSH** 才会
 * 加载到它。注入给页面的 `window.__DSH_LAUNCHER_BRIDGE__` 由服务端插件写入。
 */
window.__ModuleLoader__.load({
	id: "dsh-launcher-plugin",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;

		/** 客户端服务依赖：等 dsh-client-ui-theme 的 `theme` 服务就绪（package.json 的
		 *  dsh.client.inject 保证那个包先装载）。 */
		const inject = ["theme"];
		/** 重连退避（与服务端插件同节奏）。 */
		const RETRY_DELAYS_MS = [500, 1500, 4000, 10000, 20000, 40000];
		/** WebSocket.readyState 的 OPEN。 */
		const WS_OPEN = 1;
		const PREFERENCES = new Set(["light", "dark", "system"]);

		function log(line) {
			try {
				console.debug("[dsh-launcher-plugin/client] " + line);
			} catch {
				/* 页面控制台不可用时静默 */
			}
		}

		/** 读服务端插件注入的桥接坐标；缺项返回 null（= 没有即时通道）。 */
		function readCoordinates() {
			const c = window.__DSH_LAUNCHER_BRIDGE__;
			if (c === void 0 || c === null || typeof c !== "object") return null;
			if (typeof c.url !== "string" || c.url === "") return null;
			if (typeof c.token !== "string" || c.token === "") return null;
			if (typeof c.instanceId !== "string" || c.instanceId === "") return null;
			if (typeof c.launchId !== "string" || c.launchId === "") return null;
			return c;
		}

		function apply(ctx) {
			const coordinates = readCoordinates();
			if (coordinates !== null) {
				start(ctx, coordinates);
				return;
			}
			// 注入脚本与客户端模块的先后属于 DSH 页面装配的内部细节：短暂重试几次，还拿不到就
			// 当没有即时通道（fail-open —— 主题仍会同步，只是走服务端写入那条慢路）。
			let tries = 0;
			const timer = setInterval(() => {
				const found = readCoordinates();
				if (found !== null) {
					clearInterval(timer);
					start(ctx, found);
					return;
				}
				if (++tries >= 20) {
					clearInterval(timer);
					log("页面里没有桥接坐标（服务端插件未注入 / 非 launcher 拉起），即时通道关闭");
				}
			}, 100);
			ctx.effect(() => () => clearInterval(timer), "dsh-launcher-plugin: 坐标等待");
		}

		function start(ctx, coordinates) {
			let socket = null;
			let connecting = false;
			let retryIdx = 0;
			let retryTimer = null;
			let disabled = false;
			/** 刚由 launcher 指定、正在等的偏好值：它引起的 theme/change 不必回声给 launcher。 */
			let commanded = "";
			/** 已经回报过的偏好值（去重，避免同值反复上报）。 */
			let reported = "";

			function themeService() {
				let service = null;
				try {
					service = typeof ctx.get === "function" ? ctx.get("theme") : ctx.theme;
				} catch {
					service = ctx.theme;
				}
				if (service === void 0 || service === null) return null;
				return typeof service.setTheme === "function" ? service : null;
			}

			function send(type, payload) {
				if (socket === null || socket.readyState !== WS_OPEN) return false;
				try {
					socket.send(JSON.stringify({
						type,
						instanceId: coordinates.instanceId,
						launchId: coordinates.launchId,
						payload,
					}));
					return true;
				} catch (error) {
					log("帧发送失败（" + type + "）：" + (error?.message ?? String(error)));
					return false;
				}
			}

			function scheduleRetry() {
				if (disabled || retryTimer !== null) return;
				const delay = RETRY_DELAYS_MS[Math.min(retryIdx, RETRY_DELAYS_MS.length - 1)];
				retryIdx += 1;
				retryTimer = setTimeout(() => {
					retryTimer = null;
					connect();
				}, delay);
			}

			function connect() {
				if (disabled || connecting) return;
				if (socket !== null && socket.readyState === WS_OPEN) return;
				connecting = true;
				const url = coordinates.url
					+ (coordinates.url.includes("?") ? "&" : "?")
					+ "token=" + encodeURIComponent(coordinates.token);
				let ws;
				try {
					// 浏览器的 WebSocket 不能自定义握手头，token 只能走 query（launcher 侧 page 角色
					// 另有 Origin 白名单 + launch 校验兜着）。
					ws = new WebSocket(url);
				} catch (error) {
					connecting = false;
					log("WebSocket 构造失败：" + (error?.message ?? String(error)));
					scheduleRetry();
					return;
				}
				socket = ws;
				ws.onopen = () => {
					connecting = false;
					retryIdx = 0;
					send("page-hello", {
						plugin: coordinates.plugin,
						pluginVersion: coordinates.pluginVersion,
					});
				};
				ws.onmessage = (event) => handleFrame(event.data);
				ws.onclose = () => {
					connecting = false;
					if (socket === ws) socket = null;
					scheduleRetry();
				};
				ws.onerror = () => {
					/* 出错后浏览器一定会跟一个 close：退避重连统一放在 onclose 里 */
				};
			}

			function handleFrame(raw) {
				let env = null;
				try {
					env = JSON.parse(typeof raw === "string" ? raw : "");
				} catch {
					return;
				}
				if (env === null || typeof env !== "object") return;
				if (env.type === "page-set-theme") {
					applyCommand(env.payload ?? {});
					return;
				}
				if (env.type === "page-result") {
					const payload = env.payload ?? {};
					// 没有 id 的 page-result 是对 page-hello 的应答：被拒说明这个页面不属于当前
					// 进程（旧标签页），重连也没意义 —— 停下，主题退回服务端写入。
					if (!payload.id && payload.ok === false) {
						disabled = true;
						if (retryTimer !== null) clearTimeout(retryTimer);
						retryTimer = null;
						log("握手被拒（" + (payload.error ?? "unknown") + "），即时通道关闭（主题退回服务端写入）");
					}
				}
			}

			function applyCommand(payload) {
				const id = typeof payload.id === "string" ? payload.id : "";
				const preference = payload.preference;
				if (!PREFERENCES.has(preference)) {
					send("page-result", {
						id,
						ok: false,
						error: "unsupported preference",
						preference: typeof preference === "string" ? preference : "",
					});
					return;
				}
				const theme = themeService();
				if (theme === null) {
					send("page-result", { id, ok: false, error: "theme service unavailable", preference });
					return;
				}
				try {
					// 乐观换肤：setTheme 内部先 publish() 再后台写盘（写盘成功与否由 DSH 自己负责，
					// 失败时服务端插件的 settings 监听会把真实值推回 launcher，界面不会说谎）。
					// 已经是这个主题时 setTheme 会直接早退、不发 theme/change，所以别留下等待回声的
					// commanded（否则页面里下一次同值变化会被误当成回声吞掉）。
					const current = typeof theme.getTheme === "function"
						? theme.getTheme()?.preference
						: void 0;
					if (current === preference) {
						reported = preference;
						commanded = "";
					} else {
						commanded = preference;
					}
					theme.setTheme(preference);
					send("page-result", { id, ok: true, preference });
				} catch (error) {
					send("page-result", { id, ok: false, error: error?.message ?? String(error), preference });
				}
			}

			// 页面里自己换的主题：立刻回报，launcher 当场跟上（否则要等 DSH 把配置写一遍）。
			ctx.effect(() => ctx.on("theme/change", (snapshot) => {
				const preference = snapshot?.preference;
				if (typeof preference !== "string" || preference === "") return;
				if (preference === commanded) {
					// 这次变化就是 launcher 自己下发的：它已经知道，不必回声。
					commanded = "";
					reported = preference;
					return;
				}
				if (preference === reported) return;
				reported = preference;
				send("page-theme", { preference });
			}), "dsh-launcher-plugin: 主题回报");

			ctx.effect(() => () => {
				disabled = true;
				if (retryTimer !== null) clearTimeout(retryTimer);
				retryTimer = null;
				try {
					socket?.close();
				} catch {
					/* 已断开 */
				}
			}, "dsh-launcher-plugin: 即时通道清理");

			connect();
		}

		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});
