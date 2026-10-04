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
 * 1.2 起加一道**内嵌门禁**：只有**启动器内嵌视图**（启动器前端里的 `<iframe>`）那个页面接这条
 * 通道，外部浏览器标签页**不接**（见 isEmbeddedView 与 doc/网页端即时通道实现方案.md）。
 *
 *   为什么：这条通道天生是"每页一条"，而它省下的 330ms 只对"用户正看着启动器点主题"那个页面有
 *   意义 —— 也就是内嵌视图。外部标签页晚 300ms 跟随完全可接受。而启动器侧的 `pages` 是**每实例
 *   一个槽**（新连接踢掉旧连接），一旦有两个页面同时接进来就会互踢：被踢的那页按契约 500ms 重连
 *   （退避在每次握手成功时清零），于是 2 次/秒无限振荡（真机日志：连续十几分钟、每秒恰好 2 条
 *   「网页端已接入」、段内「已断开」为 0）。
 *
 *   ⚠️ 不能用 URL 标记区分：内嵌那条带 token 的地址，DSH 回 `303 → location: ./`，token 换成会话、
 *   **query 被整个丢掉**；外部那条落地也是 `/`。两者最终 URL 完全相同，只能按"本页是否被 iframe
 *   内嵌"判断。也别改回"每实例单值 + 踢旧"或去做页面侧选举 —— 门禁之下启动器的单槽语义重新
 *   正确，够用。
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
		/** 设置导航行的认领标记：官方 nav 图标按 section id 硬编码（未知 id 一律通用齿轮），
		 *  第三方**没有图标位**可用 —— 只能自己认领那一行（见 installNavIcon）。 */
		const NAV_ICON_MARKER = "data-dsh-launcher-nav-icon";
		/** 设置面板里的导航行：`[role="dialog"]` 是设置对话框，`nav` 是左侧导航列。 */
		const NAV_ROW_SELECTOR = '[role="dialog"] nav button';
		/** 导航图标尺寸：与官方 nav 图标同尺寸（16px），行距节奏不变。 */
		const NAV_ICON_SIZE = 16;
		/**
		 * 启动器 logo（设置导航行 + 面板页头共用）。
		 *
		 * 源：`dsh-launcher/frontend/src/assets/logo.png`（256×256，黑底，与启动器窗口品牌区 /
		 * 任务栏图标同一张）。这里内联 64×64 HighQualityBicubic 缩放后的 PNG：DSH 页面读不到插件包
		 * 内的文件、bundle 也只能 require 模块，data URI 是唯一自洽的做法（约 12KB）。
		 * 重生成：`Add-Type -AssemblyName System.Drawing` → Graphics 用 HighQualityBicubic 缩到
		 * 64×64 → `[Convert]::ToBase64String` → 拼成 `data:image/png;base64,…`。
		 * ⚠️ 它是**黑底彩色**图，不跟随主题色（要跟随就得另画一版单色剪影走 CSS mask）。
		 */
		const NAV_LOGO_DATA_URI = "data:image/png;base64,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";

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
.dshl-head{display:flex;align-items:center;gap:10px}
.dshl-panel-logo{width:24px;height:24px;flex:none;display:block;object-fit:cover;border-radius:6px}
`;
		const CSS_TAG_ID = "dsh-launcher-plugin/section.css";
		/** 导航图标那段 CSS 单独一个标签：它随认领它的 effect 一起装卸（见 installNavIcon）。 */
		const NAV_CSS_TAG_ID = "dsh-launcher-plugin/nav-icon.css";

		/**
		 * 导航行的样式：藏掉官方那个兜底齿轮，用 `::before` 画我们自己的 logo。
		 * 只对带认领标记的那一颗按钮生效 —— 不依赖 CSS module 的哈希类名，也不碰其它导航行。
		 */
		function navIconCss() {
			return [
				"[" + NAV_ICON_MARKER + "] > svg{display:none}",
				"[" + NAV_ICON_MARKER + "]::before{content:\"\";flex:none;width:" + NAV_ICON_SIZE
					+ "px;height:" + NAV_ICON_SIZE + "px;border-radius:4px;background-image:url(\""
					+ NAV_LOGO_DATA_URI + "\");background-repeat:no-repeat;background-position:center;background-size:"
					+ NAV_ICON_SIZE + "px " + NAV_ICON_SIZE + "px}",
			].join("\n");
		}

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
		 *     reason, retry, connectedAt, lastFrameAt, bridgeHost, theme, coord,
		 *     external }  // external = 本页是外部标签页（门禁不放行），不是故障
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
			external: false,
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

		/**
		 * 本页是不是**启动器内嵌视图**（启动器前端里的 `<iframe>`）打开的？—— 这条通道的门禁。
		 *
		 * 只有内嵌视图接，外部浏览器标签页不接：启动器侧的 `pages` 是**每实例一个槽**、新连接踢掉
		 * 旧连接，两个页面同时接进来就会互踢成 2 次/秒的振荡（被踢的那页按契约重连，而退避在每次
		 * 握手成功时清零，永远停在 500ms）。外部页面本来也不需要这 330ms —— 用户点启动器主题时看
		 * 的是内嵌视图。
		 *
		 * 判据只能是 `self !== top`（内嵌 = 有父窗口），**不能用 URL 标记**：内嵌那条带 token 的地址
		 * 会被 DSH 用 `303 → ./` 换成会话、query 整个丢掉，与外部页面最终 URL 完全相同。
		 *
		 * 取不到 `top`、或比较抛异常时一律返回 false（= 不接）。不接是安全方向：主题仍会同步，只是
		 * 走服务端写入那条慢路（约 300ms），功能不会坏。
		 */
		function isEmbeddedView() {
			try {
				if (window.self === void 0 || window.top === void 0) return false;
				return window.self !== window.top;
			} catch {
				// 跨源隔离等场景下读 top 可能抛：当作"不是内嵌"，不猜。
				return false;
			}
		}
		//#endregion

		//#region 面板文案与功能清单
		/** 功能展示清单（静态）：只说插件与启动器之间提供了什么、各自的前提是什么。
		 *  ⚠️ 这里刻意不带实时状态：网页半边看不到服务端半边的探测结论，而"没有证据"不能当"失败"
		 *  也不能当"正常"（见 AGENTS.md §9 的三态纪律）。 */
		const FEATURES = ["pageTheme", "themeReport", "themeSet", "restartTool", "restartDelivery", "sessionNotify", "embedRelax", "capsReport"];

		const zh = {
			nav: "启动器",
			title: "DSH 启动器桥接",
			intro: "本实例通过一条本机 WebSocket（127.0.0.1 的临时端口）与 DSH 启动器通信：换主题、自重启、重启后继续原来的会话、回答完成与提问的系统通知都走它。下面显示这个网页与该通道的连接状态，以及插件提供的功能。",
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
			"hint.externalPage": "外部标签页不接即时通道（设计如此，只有启动器内嵌视图接）：主题照旧同步，只是每次约 300ms。要看即时换肤请用启动器的内嵌视图。",
			"features.title": "功能展示",
			"features.need": "前提",
			"feat.pageTheme.title": "主题即时同步",
			"feat.pageTheme.desc": "在启动器里点主题，本页当场换肤；写盘交给 DSH 自己在后台完成。",
			"feat.pageTheme.need": "插件 0.2.13+，且从启动器内嵌视图打开（外部标签页不接入）",
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
			"feat.sessionNotify.title": "回答完成 / 提问通知",
			"feat.sessionNotify.desc": "会话里 AI 回答完成（正文 = 最终回复前 50 字）、或用提问工具等你回答（正文 = 我有一些问题）时，由启动器弹一条系统通知，标题是会话标题。",
			"feat.sessionNotify.need": "插件 0.2.14+，且启动器「设置 → 通知」里的对应开关是开着的（默认开）",
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
			intro: "This instance talks to the DSH launcher over one loopback WebSocket (a temporary port on 127.0.0.1): theme switching, self-restart, restart hand-off and the answer/question notifications all ride on it. Below is this page's link state and the features the plugin provides.",
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
			"hint.externalPage": "External tabs do not join the instant channel (by design: only the launcher's embedded view does). Theming still syncs, just with the usual ~300ms. Use the launcher's embedded view for instant repaint.",
			"features.title": "Features",
			"features.need": "Requires",
			"feat.pageTheme.title": "Instant theme sync",
			"feat.pageTheme.desc": "Pick a theme in the launcher and this page repaints immediately; writing the setting is left to DSH in the background.",
			"feat.pageTheme.need": "plugin 0.2.13+, opened from the launcher's embedded view (external tabs do not join)",
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
			"feat.sessionNotify.title": "Answer / question notifications",
			"feat.sessionNotify.desc": "When an answer finishes (body = first 50 characters of the reply) or the model asks you something (body = \"I have some questions\"), the launcher shows an OS notification titled with the session title.",
			"feat.sessionNotify.need": "plugin 0.2.14+ and the matching switch under the launcher's Settings → Notifications (on by default)",
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
			if (status.external === true) hints.push(t("hint.externalPage"));
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
				h("div", { className: "dshl-head", key: "head" }, [
					h("img", { className: "dshl-panel-logo", key: "logo", src: NAV_LOGO_DATA_URI, alt: "", draggable: false }),
					h("h2", { className: "dshl-title", key: "title" }, t("title")),
				]),
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
			// 导航行那一格：官方没有图标位，只能自己认领（传的 label 必须与注册时那个一致）。
			installNavIcon(ctx, () => t("nav"));
			log("设置面板已注册（settings.section → " + SECTION_ID + "，order " + SECTION_ORDER + "）");
		}

		/**
		 * 认领「设置」里我们那一行导航，把官方兜底的通用齿轮换成启动器 logo。
		 *
		 * 为什么必须自己认领：`settings.section` 只投影 `id / order / label`，而官方 nav 图标是按
		 * section id 硬编码的（`navIcon(id)`），未知 id 一律通用齿轮 —— 第三方**没有图标位**。
		 * 做法与 dshmarket 一致（见其 `src/client/settings-nav-icon.ts`）：按**行文本 = 我们的
		 * label** 认领那一颗按钮，打上标记，再用注入的 CSS 藏掉 `> svg`、用 `::before` 画自己的
		 * 图标。社区同款还有 dsh-better-sidebar / dsh-skill-mcp-panel。
		 *
		 * 刻意做窄：只动带标记的那一行，不碰任何官方结构；marker 与观察者随 fiber 一起清理；
		 * MutationObserver 覆盖重渲染与切语言（行文本会跟着 label 变）。
		 *
		 * ⚠️ 官方哪天改了设置面板结构（`[role="dialog"] nav button` 不成立），这里就是零命中 ——
		 * 结果是**退回通用齿轮**，不报错、不影响面板与主题通道。
		 * ⚠️ 官方哪天给 `settings.section` 长出 `icon` 字段，这段整个删掉（dshmarket 也这么定的）。
		 */
		function installNavIcon(ctx, resolveLabel) {
			if (typeof document === "undefined") return;
			ctx.effect(() => {
				const tag = document.createElement("style");
				tag.dataset.plugin = "dsh-launcher-plugin";
				tag.dataset.pluginCss = NAV_CSS_TAG_ID;
				tag.textContent = navIconCss();
				document.head.appendChild(tag);

				let disposed = false;
				let scheduled = false;
				const sync = () => {
					scheduled = false;
					if (disposed) return;
					const wanted = String(resolveLabel() ?? "").trim();
					// 空 label 一条都不标：语言还没解析出来时不能把整个 nav 认成自己的。
					for (const row of document.querySelectorAll(NAV_ROW_SELECTOR)) {
						if (wanted !== "" && String(row.textContent ?? "").trim() === wanted) {
							row.setAttribute(NAV_ICON_MARKER, "");
						} else {
							row.removeAttribute(NAV_ICON_MARKER);
						}
					}
				};
				// 一串 DOM 变更合并成一次 sync，并赶在下一帧之前落地（别让用户先看到齿轮）。
				const schedule = () => {
					if (scheduled || disposed) return;
					scheduled = true;
					queueMicrotask(sync);
				};
				sync();
				const observer = typeof MutationObserver === "function" ? new MutationObserver(schedule) : null;
				if (observer !== null && document.body !== void 0 && document.body !== null) {
					observer.observe(document.body, { childList: true, subtree: true, characterData: true });
				}
				return () => {
					disposed = true;
					observer?.disconnect();
					for (const row of document.querySelectorAll("[" + NAV_ICON_MARKER + "]")) {
						row.removeAttribute(NAV_ICON_MARKER);
					}
					tag.remove();
				};
			}, "dsh-launcher-plugin: 设置导航图标");
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
				// 门禁（0.2.13）：只有启动器内嵌视图接这条通道，外部标签页不接 —— 启动器的 page 槽是
				// 每实例一个，两个页面同时接进来会互踢成 2 次/秒的振荡；而外部页面本来也不需要这
				// 330ms（用户点启动器主题时看的是内嵌视图）。不接不是故障：主题照旧同步，只是走服务端
				// 写入那条慢路。这里不排任何重连 —— 页面是不是内嵌的，刷新也不会变。
				if (!isEmbeddedView()) {
					publish({
						transport: "absent",
						external: true,
						reason: "本页是外部标签页，不接即时通道（只有启动器内嵌视图接）：主题照旧同步，只是要等 DSH 把配置写一遍（实测约 300ms）",
						bridgeHost: bridgeHostOf(coordinates.url),
						coord: coordOf(coordinates),
					});
					return;
				}
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
