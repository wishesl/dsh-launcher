/**
 * dsh-launcher-plugin — 客户端半边：DSH 网页端 ↔ launcher 的**主题即时通道** + 设置面板。
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
 * 1.1 起多一个 **「设置 → 启动器」分区**（settings.section 槽）：只展示**本页这条 WebSocket 通道
 * 自己的状态**（连通 / 连接中 / 离线重连 / 被拒 / 未接入、桥接地址、launch、注入的插件版本、
 * 本页主题），外加一份**静态**的功能清单（名称 + 说明 + 前提）。
 *
 *   ⚠️ 面板**不显示**服务端半边（lib/index.js）那条连接的在线状态与能力探测结论
 *   （themeReport / restartTool / embedRelax…）：那些结论只存在于 DSH 的 node 进程与启动器内存里，
 *   页面读不到。要显示它们，得由启动器推一条状态帧、或由服务端半边在注入坐标时带一份快照 ——
 *   本版都不做。面板不猜、不假装，缺证据就不显示。
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

		/** 客户端服务依赖：`theme` 是即时通道的核心；`slots`/`locale` 只给设置分区用。
		 *  为什么敢把 slots/locale 写进 inject：`theme` 由 dsh-client-ui-theme 提供，而它自己的
		 *  inject 就含 slots/locale —— 只要本插件跑得起来，这两项必然已就绪，不引入新的失败面。 */
		const inject = ["theme", "slots", "locale"];
		/** 重连退避（与服务端插件同节奏）。 */
		const RETRY_DELAYS_MS = [500, 1500, 4000, 10000, 20000, 40000];
		/** WebSocket.readyState 的 OPEN。 */
		const WS_OPEN = 1;
		const PREFERENCES = new Set(["light", "dark", "system"]);
		/** 设置面板的字典命名空间（客户端半边自己的，不落任何设置）。 */
		const NS = "dsh-launcher-bridge";
		/** 设置分区 id：稳定契约键（用户按它选中分区）。 */
		const SECTION_ID = "dsh-launcher";
		/** 设置分区顺序：官方 general(0) / models(10) / plugins(15) 之后。 */
		const SECTION_ORDER = 30;

		function log(line) {
			try {
				console.debug("[dsh-launcher-plugin/client] " + line);
			} catch {
				/* 页面控制台不可用时静默 */
			}
		}

		function messageOf(error) {
			return error?.message ?? String(error);
		}

		//#region 设置面板样式（只用 --dsw-* token，明暗主题自动跟随）
		/** 面板样式：容器与控件一律继承宿主 token，不写死任何颜色。 */
		const SECTION_CSS = `
.dshl-section{max-width:760px;color:var(--dsw-alias-label-primary);flex-direction:column;gap:16px;display:flex}
.dshl-title{margin:0;font-size:18px;font-weight:600}
.dshl-intro{margin:0;color:var(--dsw-alias-label-tertiary);font-size:13px;line-height:20px}
.dshl-group{flex-direction:column;gap:8px;display:flex}
.dshl-group-head{align-items:center;justify-content:space-between;gap:12px;display:flex}
.dshl-subtitle{margin:0;font-size:15px;font-weight:600;line-height:22px}
.dshl-btn{font:inherit;font-size:13px;line-height:20px;padding:6px 12px;cursor:pointer;background:0 0;border:.5px solid var(--dsw-alias-border-l4);border-radius:var(--dsw-radius-md);color:var(--dsw-alias-label-primary)}
.dshl-btn:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover)}
.dshl-btn:disabled{color:var(--dsw-alias-label-caption);cursor:default}
.dshl-btn:focus-visible{outline:var(--dsw-focus-ring-width) solid var(--dsw-focus-ring-color,var(--dsw-alias-state-business-primary));outline-offset:2px}
.dshl-rows{margin:0;padding:0;list-style:none;flex-direction:column;display:flex}
.dshl-row{align-items:baseline;gap:12px;padding:7px 0;border-bottom:.5px solid var(--dsw-alias-border-l2);display:flex;font-size:13px;line-height:20px}
.dshl-row:last-child{border-bottom:0}
.dshl-row-label{color:var(--dsw-alias-label-tertiary);flex:0 0 132px}
.dshl-row-value{flex:1;min-width:0;word-break:break-word}
.dshl-row-value.is-ok{color:var(--dsw-alias-state-success-primary)}
.dshl-row-value.is-bad{color:var(--dsw-alias-state-error-primary)}
.dshl-hint{margin:0;color:var(--dsw-alias-state-warn-label,var(--dsw-alias-label-secondary));font-size:12px;line-height:18px}
.dshl-cards{margin:0;padding:0;list-style:none;flex-direction:column;gap:10px;display:flex}
.dshl-card{flex-direction:column;gap:4px;display:flex;padding:12px 14px;background:var(--dsw-alias-bg-module-platform);border:.5px solid var(--dsw-alias-border-l2);border-radius:var(--dsw-radius-xl)}
.dshl-card-title{font-size:13px;font-weight:600;line-height:20px}
.dshl-card-desc{color:var(--dsw-alias-label-secondary);font-size:12px;line-height:18px}
.dshl-card-need{color:var(--dsw-alias-label-tertiary);font-size:12px;line-height:18px}
`;
		const CSS_TAG_ID = "dsh-launcher-plugin/section.css";

		/** 插一次面板样式（按 tag 去重；node 环境下没有 document 就直接跳过）。 */
		function installStyles() {
			try {
				if (typeof document === "undefined") return;
				if (document.querySelector("style[data-plugin-css=" + JSON.stringify(CSS_TAG_ID) + "]") !== null) return;
				const tag = document.createElement("style");
				tag.dataset.plugin = "dsh-launcher-plugin";
				tag.dataset.pluginCss = CSS_TAG_ID;
				tag.textContent = SECTION_CSS;
				document.head.appendChild(tag);
			} catch {
				/* 样式装不上不影响功能 */
			}
		}
		//#endregion

		//#region 状态仓库（只装本页自己就能看到的东西）
		/**
		 * 面板读到的**只读快照**。形状：
		 *   { transport: "absent"|"connecting"|"online"|"offline"|"disabled",
		 *     reason, retry, connectedAt, lastFrameAt, bridgeHost, theme, coord }
		 * publish 每次都换一个新对象 —— React 靠引用变化重渲染，原地改字段会被它跳过。
		 */
		let snapshot = {
			transport: "absent",
			reason: "",
			retry: 0,
			connectedAt: 0,
			lastFrameAt: 0,
			bridgeHost: "",
			theme: "",
			coord: null,
		};
		const statusListeners = new Set();

		/** 面板读状态（同一个快照对象，直到下次 publish）。 */
		function readStatus() {
			return snapshot;
		}

		/** 订阅状态变化，返回退订函数。 */
		function subscribeStatus(listener) {
			statusListeners.add(listener);
			return () => {
				statusListeners.delete(listener);
			};
		}

		/** 记一次状态变化并通知面板。订阅者自己抛错不影响通道。 */
		function publish(patch) {
			snapshot = { ...snapshot, ...patch };
			for (const listener of [...statusListeners]) {
				try {
					listener(snapshot);
				} catch {
					/* 面板自己炸了不该影响主题通道 */
				}
			}
		}

		/** 当前会话的「立即重连」入口（没有会话时为 null）。 */
		let reconnectRef = null;

		/** 面板按钮用：跳过重连退避，立即重试。没有会话 / 通道健康时什么都不做。 */
		function reconnect() {
			if (reconnectRef === null) return false;
			try {
				return reconnectRef();
			} catch (error) {
				log("重新连接失败：" + messageOf(error));
				return false;
			}
		}

		/** 桥接地址 → host:port（token 不进任何展示面）。 */
		function bridgeHostOf(raw) {
			try {
				return new URL(raw).host;
			} catch {
				return "";
			}
		}

		/** 坐标摘要（面板展示用）。 */
		function coordOf(coordinates) {
			return {
				instanceId: typeof coordinates.instanceId === "string" ? coordinates.instanceId : "",
				launchId: typeof coordinates.launchId === "string" ? coordinates.launchId : "",
				plugin: typeof coordinates.plugin === "string" ? coordinates.plugin : "",
				pluginVersion: typeof coordinates.pluginVersion === "string" ? coordinates.pluginVersion : "",
			};
		}

		/** 本页当前主题偏好（只作展示）；取不到留空，不猜。 */
		function themePreferenceOf(ctx) {
			try {
				const service = typeof ctx.get === "function" ? ctx.get("theme") : ctx.theme;
				const value = typeof service?.getTheme === "function" ? service.getTheme()?.preference : void 0;
				return typeof value === "string" ? value : "";
			} catch {
				return "";
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
		//#endregion

		//#region 面板文案与功能清单
		/** 功能展示清单（静态）：只说插件与启动器之间提供了什么、各自的前提是什么。
		 *  ⚠️ 这里刻意不带实时状态：网页半边看不到服务端半边的探测结论，而"没有证据"不能当"失败"
		 *  也不能当"正常"（见 AGENTS.md §9 的三态纪律）。 */
		const FEATURES = ["pageTheme", "themeReport", "themeSet", "restartTool", "restartDelivery", "embedRelax", "capsReport"];

		const zh = {
			nav: "启动器",
			title: "DSH 启动器桥接",
			intro: "本实例通过一条本机 WebSocket（127.0.0.1 的临时端口）与 DSH 启动器通信：换主题、自重启、重启后继续原来的会话都走它。下面显示这个网页与该通道的连接状态，以及插件提供的功能。",
			"status.title": "链接状态",
			"status.reconnect": "重新连接",
			"status.reconnectIdle": "通道已连接，无需重连",
			"status.reconnectHint": "跳过重连退避，立即重试一次",
			"row.channel": "网页通道",
			"row.reason": "说明",
			"row.bridge": "桥接地址",
			"row.instance": "实例 / launch",
			"row.plugin": "插件版本",
			"row.theme": "页面主题",
			"row.frames": "已连接 / 上次收帧",
			"channel.online": "已连接",
			"channel.connecting": "连接中",
			"channel.offline": "离线（自动重连中）",
			"channel.retry": "第 {n} 次",
			"channel.disabled": "已关闭",
			"channel.absent": "未接入",
			"hint.launcherRestarted": "启动器可能已重启（桥接端口与 token 会变）：重启本实例即可恢复。",
			"hint.refreshPage": "本页记录的 launch 与当前进程不一致（旧标签页）：刷新页面即可恢复。",
			"features.title": "功能展示",
			"features.need": "前提",
			"feat.pageTheme.title": "主题即时同步",
			"feat.pageTheme.desc": "在启动器里点主题，本页当场换肤；写盘交给 DSH 自己在后台完成。",
			"feat.pageTheme.need": "插件 0.2.6+，且 DSH 重启过一次（客户端半边在启动时装载）",
			"feat.themeReport.title": "主题变化上报",
			"feat.themeReport.desc": "在本页或 DSH 里改主题，启动器立刻跟上，不必等配置写完。",
			"feat.themeReport.need": "由启动器拉起的实例",
			"feat.themeSet.title": "主题写入",
			"feat.themeSet.desc": "启动器里切主题会写进共享 profile 的 ui-theme；本页不在场时走这条慢路（实测约 300ms）。",
			"feat.themeSet.need": "插件已装载，且 settings 服务可写",
			"feat.restartTool.title": "dsh-restart 自重启",
			"feat.restartTool.desc": "让模型调用 dsh-restart 重启本实例，确认词 restart-dsh。",
			"feat.restartTool.need": "拿到启动器的 ack 才退出，实例不会白死一次",
			"feat.restartDelivery.title": "重启完成续跑",
			"feat.restartDelivery.desc": "重启完成后向原来的会话注入「重启完成」消息，接着做没做完的事。",
			"feat.restartDelivery.need": "走 plugin/notice 通道，不会显示成用户气泡",
			"feat.embedRelax.title": "内嵌视图授权放宽",
			"feat.embedRelax.desc": "DSH 被启动器内嵌打开时不再 401 / 一直「自动重连中」。",
			"feat.embedRelax.need": "从启动器的内嵌视图入口打开",
			"feat.capsReport.title": "能力探测上报",
			"feat.capsReport.desc": "插件把各项能力的探测结论上报给启动器。",
			"feat.capsReport.need": "结论在启动器右栏「兼容性」标签里查看",
		};

		const en = {
			nav: "Launcher",
			title: "DSH launcher bridge",
			intro: "This instance talks to the DSH launcher over one loopback WebSocket (a temporary port on 127.0.0.1): theme switching, self-restart and restart hand-off all ride on it. Below is this page's link state and the features the plugin provides.",
			"status.title": "Link status",
			"status.reconnect": "Reconnect",
			"status.reconnectIdle": "Channel is connected; nothing to reconnect",
			"status.reconnectHint": "Skip the reconnect backoff and retry now",
			"row.channel": "Page channel",
			"row.reason": "Detail",
			"row.bridge": "Bridge address",
			"row.instance": "Instance / launch",
			"row.plugin": "Plugin version",
			"row.theme": "Page theme",
			"row.frames": "Connected / last frame",
			"channel.online": "Connected",
			"channel.connecting": "Connecting",
			"channel.offline": "Offline (reconnecting)",
			"channel.retry": "attempt {n}",
			"channel.disabled": "Closed",
			"channel.absent": "Not attached",
			"hint.launcherRestarted": "The launcher may have restarted (its bridge port and token change): restart this instance to recover.",
			"hint.refreshPage": "This page carries a launch id that no longer matches the running process (stale tab): refresh the page to recover.",
			"features.title": "Features",
			"features.need": "Requires",
			"feat.pageTheme.title": "Instant theme sync",
			"feat.pageTheme.desc": "Pick a theme in the launcher and this page repaints immediately; writing the setting is left to DSH in the background.",
			"feat.pageTheme.need": "plugin 0.2.6+, and one DSH restart (the client half loads at startup)",
			"feat.themeReport.title": "Theme change reporting",
			"feat.themeReport.desc": "Switch the theme here or in DSH and the launcher follows at once, without waiting for the write.",
			"feat.themeReport.need": "an instance started by the launcher",
			"feat.themeSet.title": "Theme write-back",
			"feat.themeSet.desc": "Switching the theme in the launcher writes ui-theme into the shared profile; without this page it takes the slow path (~300ms).",
			"feat.themeSet.need": "the plugin is loaded and the settings service is writable",
			"feat.restartTool.title": "dsh-restart",
			"feat.restartTool.desc": "Lets the model restart this instance with the dsh-restart tool; the confirmation word is restart-dsh.",
			"feat.restartTool.need": "it exits only after the launcher acknowledges, so an instance never dies for nothing",
			"feat.restartDelivery.title": "Restart hand-off",
			"feat.restartDelivery.desc": "After a restart it injects a \"restart complete\" message into the original session so work continues.",
			"feat.restartDelivery.need": "delivered over the plugin/notice channel, not as a user bubble",
			"feat.embedRelax.title": "Embedded-view auth relax",
			"feat.embedRelax.desc": "DSH no longer answers 401 / endless \"reconnecting\" when opened inside the launcher.",
			"feat.embedRelax.need": "open it from the launcher's embedded view",
			"feat.capsReport.title": "Capability reporting",
			"feat.capsReport.desc": "The plugin reports each capability's probe result back to the launcher.",
			"feat.capsReport.need": "read the full list in the launcher's right-hand Compatibility tab",
		};
		//#endregion

		//#region 设置分区（settings.section）
		/** 懒取 react（隐式 baseline 模块之一）：取不到只是没有面板，绝不影响主题通道。 */
		let reactRef = null;
		function reactModule() {
			if (reactRef === null) {
				try {
					reactRef = require("react");
				} catch (error) {
					reactRef = void 0;
					log("react 取不到，设置面板关闭：" + messageOf(error));
				}
			}
			return reactRef;
		}

		/** 本地时刻 HH:mm:ss（不引定时器：只在状态变化时渲染一次）。 */
		function clockOf(stamp) {
			if (!Number.isFinite(stamp) || stamp <= 0) return "";
			try {
				const date = new Date(stamp);
				const pad = (value) => String(value).padStart(2, "0");
				return pad(date.getHours()) + ":" + pad(date.getMinutes()) + ":" + pad(date.getSeconds());
			} catch {
				return "";
			}
		}

		/** launch 只显示前 8 位：完整值很长，且面板上没人会去核对它。 */
		function shortID(raw) {
			if (typeof raw !== "string" || raw === "") return "";
			return raw.length <= 8 ? raw : raw.slice(0, 8) + "…";
		}

		/** 通道状态 → 文案 + 配色档（ok / bad / 中性空串）。
		 *  ⚠️ 配色纪律（同 AGENTS.md §9）：**只有"确定不工作"才标红**。通道断开/重连中只是降级 ——
		 *  主题照样同步（退回服务端写盘那条路），所以是中性；只有握手被拒（launch 对不上、不会自愈）
		 *  才是红。 */
		function channelOf(status, t) {
			switch (status.transport) {
				case "online":
					return { text: t("channel.online"), tone: "is-ok" };
				case "connecting":
					return { text: t("channel.connecting"), tone: "" };
				case "offline": {
					const attempt = status.retry > 0 ? " · " + t("channel.retry").replace("{n}", String(status.retry)) : "";
					return { text: t("channel.offline") + attempt, tone: "" };
				}
				case "disabled":
					return { text: t("channel.disabled"), tone: "is-bad" };
				default:
					return { text: t("channel.absent"), tone: "" };
			}
		}

		/** 状态 → 行列表（纯函数，判定逻辑集中在这里，组件里不写）。 */
		function statusRows(status, t) {
			const channel = channelOf(status, t);
			const rows = [{ id: "channel", label: t("row.channel"), value: channel.text, tone: channel.tone }];
			if (status.reason !== "") {
				rows.push({ id: "reason", label: t("row.reason"), value: status.reason, tone: channel.tone });
			}
			rows.push({
				id: "bridge",
				label: t("row.bridge"),
				value: status.bridgeHost === "" ? "—" : "ws://" + status.bridgeHost + "/ws",
				tone: "",
			});
			rows.push({
				id: "instance",
				label: t("row.instance"),
				value: status.coord === null ? "—" : status.coord.instanceId + " · " + shortID(status.coord.launchId),
				tone: "",
			});
			rows.push({
				id: "plugin",
				label: t("row.plugin"),
				value: status.coord === null || status.coord.pluginVersion === ""
					? "—"
					: (status.coord.plugin === "" ? "" : status.coord.plugin + " ") + status.coord.pluginVersion,
				tone: "",
			});
			rows.push({ id: "theme", label: t("row.theme"), value: status.theme === "" ? "—" : status.theme, tone: "" });
			const connected = clockOf(status.connectedAt);
			const lastFrame = clockOf(status.lastFrameAt);
			rows.push({
				id: "frames",
				label: t("row.frames"),
				value: connected === "" ? "—" : connected + " / " + (lastFrame === "" ? "—" : lastFrame),
				tone: "",
			});
			return rows;
		}

		/** 状态 → 提示行（只在有事要说的时候出现）。 */
		function statusHints(status, t) {
			const hints = [];
			if (status.transport === "offline" && status.retry >= 3) hints.push(t("hint.launcherRestarted"));
			if (status.transport === "disabled") hints.push(t("hint.refreshPage"));
			return hints;
		}

		/** 通道健康 / 没有会话时「重新连接」没有意义。 */
		function reconnectDisabled(status) {
			return status.transport === "online" || status.transport === "absent";
		}

		/**
		 * 设置分区组件（「设置 → 启动器」）。只读 props：
		 *   t（locale seat）、readStatus / subscribeStatus + reconnect（注册时 inject 的业务面）。
		 * 注意：这里刻意不引 store / 不引 timer —— 面板只是状态快照的一个视图。
		 */
		function LauncherSection(props) {
			const React = reactModule();
			if (React === null || React === void 0) return null;
			const [status, setStatus] = React.useState(() => props.readStatus());
			React.useEffect(
				() => props.subscribeStatus(() => setStatus(props.readStatus())),
				[props.readStatus, props.subscribeStatus],
			);
			const t = typeof props.t === "function" ? props.t : (key) => key;
			const h = React.createElement;
			const rows = statusRows(status, t);
			const hints = statusHints(status, t);
			const cannotReconnect = reconnectDisabled(status);
			return h("div", { className: "dshl-section" }, [
				h("h2", { className: "dshl-title", key: "title" }, t("title")),
				h("p", { className: "dshl-intro", key: "intro" }, t("intro")),
				h("div", { className: "dshl-group", key: "status" }, [
					h("div", { className: "dshl-group-head", key: "head" }, [
						h("h3", { className: "dshl-subtitle", key: "subtitle" }, t("status.title")),
						h("button", {
							type: "button",
							className: "dshl-btn",
							key: "reconnect",
							disabled: cannotReconnect,
							title: cannotReconnect ? t("status.reconnectIdle") : t("status.reconnectHint"),
							onClick: () => props.reconnect(),
						}, t("status.reconnect")),
					]),
					h("ul", { className: "dshl-rows", key: "rows" }, rows.map((row) => h("li", { className: "dshl-row", key: row.id }, [
						h("span", { className: "dshl-row-label", key: "label" }, row.label),
						h("span", {
							className: row.tone === "" ? "dshl-row-value" : "dshl-row-value " + row.tone,
							key: "value",
						}, row.value),
					]))),
					...hints.map((hint, index) => h("p", { className: "dshl-hint", key: "hint-" + index }, hint)),
				]),
				h("div", { className: "dshl-group", key: "features" }, [
					h("h3", { className: "dshl-subtitle", key: "subtitle" }, t("features.title")),
					h("ul", { className: "dshl-cards", key: "cards" }, FEATURES.map((id) => h("li", { className: "dshl-card", key: id }, [
						h("div", { className: "dshl-card-title", key: "title" }, t("feat." + id + ".title")),
						h("div", { className: "dshl-card-desc", key: "desc" }, t("feat." + id + ".desc")),
						h("div", { className: "dshl-card-need", key: "need" }, t("features.need") + "：" + t("feat." + id + ".need")),
					]))),
				]),
			]);
		}

		/**
		 * 注册「设置 → 启动器」分区。
		 *
		 * 为什么整段 fail-open：`settings.section` 由 dsh-client-ui-settings-general 声明；换一个
		 * 不声明它的 DSH，`slots.inject` 的回调就永远不跑 —— 那只是"没有面板"，不该影响主题通道。
		 * 所以这里先查服务形状、外面还包了一层 try/catch（见 apply）。
		 */
		function installSettingsSection(ctx) {
			if (typeof ctx.slots?.inject !== "function" || typeof ctx.slots?.register !== "function" || typeof ctx.locale?.register !== "function") {
				log("slots/locale 服务形状不认识，设置面板关闭（主题通道不受影响）");
				return;
			}
			installStyles();
			const t = ctx.locale.bind(NS);
			ctx.effect(() => ctx.locale.register(NS, { zh, en }), "dsh-launcher-plugin: 面板字典");
			// slots.inject 的回调在槽声明提交后跑、槽折叠时随注册一起注销：官方姿势，
			// 不依赖任何加载顺序（我们故意不把 ui-settings-general 写进 dsh.client.inject，
			// 免得将来某个 DSH 少了那个包就把整条即时通道一起卡住）。
			ctx.effect(() => ctx.slots.inject("settings.section", () => ctx.slots.register({
				name: "settings.section",
				id: SECTION_ID,
				order: SECTION_ORDER,
				label: () => t("nav"),
				locale: NS,
				inject: () => ({ readStatus, subscribeStatus, reconnect }),
			}, LauncherSection)), "dsh-launcher-plugin: 设置分区");
			log("设置面板已注册（settings.section → " + SECTION_ID + "，order " + SECTION_ORDER + "）");
		}
		//#endregion

		function apply(ctx) {
			// 面板（连同它要的主题展示数据）整段 fail-open：槽不在、slots/locale 行为变了、
			// react 取不到、ctx.on/ctx.effect 形状变了 —— 都只是"没有面板"，
			// 绝不牵连下面那条主题即时通道。
			try {
				// 本页主题只作展示：跟随 theme/change，与有没有桥接坐标无关。
				ctx.effect(() => {
					publish({ theme: themePreferenceOf(ctx) });
					return ctx.on("theme/change", (themeSnapshot) => {
						const preference = themeSnapshot?.preference;
						if (typeof preference === "string" && preference !== "") publish({ theme: preference });
					});
				}, "dsh-launcher-plugin: 面板主题跟随");
				installSettingsSection(ctx);
			} catch (error) {
				log("设置面板未装载（已忽略）：" + messageOf(error));
			}

			const coordinates = readCoordinates();
			if (coordinates !== null) {
				start(ctx, coordinates);
				return;
			}
			// 注入脚本与客户端模块的先后属于 DSH 页面装配的内部细节：短暂重试几次，还拿不到就
			// 当没有即时通道（fail-open —— 主题仍会同步，只是走服务端写入那条慢路）。
			publish({
				transport: "absent",
				reason: "本实例不是启动器拉起的（页面里没有桥接坐标），主题退回服务端写入路径",
			});
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
					log("帧发送失败（" + type + "）：" + messageOf(error));
					return false;
				}
			}

			function scheduleRetry() {
				if (disabled || retryTimer !== null) return;
				const delay = RETRY_DELAYS_MS[Math.min(retryIdx, RETRY_DELAYS_MS.length - 1)];
				retryIdx += 1;
				publish({ retry: retryIdx });
				retryTimer = setTimeout(() => {
					retryTimer = null;
					connect();
				}, delay);
			}

			/** 面板按钮用：跳过退避立即重连（通道健康时什么都不做）。 */
			function reconnect() {
				if (disabled) return false;
				if (socket !== null && socket.readyState === WS_OPEN) return false;
				if (retryTimer !== null) {
					clearTimeout(retryTimer);
					retryTimer = null;
				}
				retryIdx = 0;
				publish({ transport: "connecting", retry: 0 });
				connect();
				return true;
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
					log("WebSocket 构造失败：" + messageOf(error));
					publish({ transport: "offline", reason: "WebSocket 构造失败：" + messageOf(error) });
					scheduleRetry();
					return;
				}
				socket = ws;
				ws.onopen = () => {
					connecting = false;
					retryIdx = 0;
					publish({ transport: "online", reason: "", retry: 0, connectedAt: Date.now() });
					send("page-hello", {
						plugin: coordinates.plugin,
						pluginVersion: coordinates.pluginVersion,
					});
				};
				ws.onmessage = (event) => handleFrame(event.data);
				ws.onclose = () => {
					connecting = false;
					if (socket === ws) socket = null;
					if (disabled) return;
					// 断开之后一定要重连（退避由 scheduleRetry 排），所以这里不带 reason：
					// "离线（自动重连中）"本身就够，写一堆错误串只会让人以为出事了。
					publish({ transport: "offline", connectedAt: 0 });
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
				publish({ lastFrameAt: Date.now() });
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
						publish({
							transport: "disabled",
							connectedAt: 0,
							reason: "握手被拒（" + (payload.error ?? "unknown") + "）",
						});
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
					send("page-result", { id, ok: false, error: messageOf(error), preference });
				}
			}

			// 页面里自己换的主题：立刻回报，launcher 当场跟上（否则要等 DSH 把配置写一遍）。
			ctx.effect(() => ctx.on("theme/change", (themeSnapshot) => {
				const preference = themeSnapshot?.preference;
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

			reconnectRef = reconnect;
			ctx.effect(() => () => {
				if (reconnectRef === reconnect) reconnectRef = null;
			}, "dsh-launcher-plugin: 面板重连入口清理");

			publish({
				transport: "connecting",
				reason: "",
				retry: 0,
				connectedAt: 0,
				lastFrameAt: 0,
				bridgeHost: bridgeHostOf(coordinates.url),
				coord: coordOf(coordinates),
			});
			connect();
		}

		exports.apply = apply;
		exports.inject = inject;
		return module.exports;
	}
});
