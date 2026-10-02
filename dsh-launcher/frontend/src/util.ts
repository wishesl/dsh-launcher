import type { BridgeStatus, CapabilityReport, Instance } from './types';

/** 启动器内置桥接插件的包名。必须与 Go 侧 `selfRestartPluginName`
 *  （dsh-launcher/self_restart.go）和插件 package.json 的 name 一致：
 *  它同时是市场里内置卡片的标识、装机副本的目录名、以及更新比对的键。 */
export const BUILTIN_PLUGIN_NAME = 'dsh-launcher-plugin';


export interface WebUrlInfo {
  url: string;
  port: number;
  autoPort: boolean; // true when --port 0 (OS 自动选端口，无法预知地址)
  runtime: boolean;  // true when the URL was captured from live process output
}

// 夹取到 [lo, hi]。hi < lo 时返回 lo（调用方可以把动态算出的上限直接传进来，
// 不必先判断窗口是否已经窄到装不下最小宽度）。
export function clamp(v: number, lo: number, hi: number): number {
  return Math.min(Math.max(v, lo), Math.max(lo, hi));
}

// 把 URL 里 token/secret 之类的查询参数值打码，只用于**界面展示**。
// DSH 的内嵌地址形如 http://127.0.0.1:3080/?token=<每启动一次的会话令牌>，
// 直接显示在标题栏上等于把令牌摆在截图/旁人眼前；真实地址仍照常传给 iframe。
const SECRET_QUERY_KEYS = /^(token|secret|key|code|password)$/i;

export function maskUrlSecrets(url: string): string {
  if (!url) return url;
  const at = url.indexOf('?');
  if (at === -1) return url;
  const head = url.slice(0, at);
  const params = url
    .slice(at + 1)
    .split('&')
    .map((seg) => {
      const eq = seg.indexOf('=');
      if (eq === -1) return seg;
      const name = seg.slice(0, eq);
      return SECRET_QUERY_KEYS.test(decodeURIComponent(name)) ? `${name}=••••••••` : seg;
    });
  return `${head}?${params.join('&')}`;
}

// 从实例的 extraArgs 里解析 --port（支持 `--port 3081` 和 `--port=3081`）。
// 未指定时 DSH web 默认监听 3080。
// 运行中实例优先使用后端从进程输出捕获的真实地址（runtime=true），
// 这样 --port 0 等动态端口场景也能拿到可点击的 URL。
export function getWebUrl(inst: Instance): WebUrlInfo {
  if (inst.webUrl) {
    const m = /:(\d{2,5})\/?$/.exec(inst.webUrl);
    const port = m ? parseInt(m[1], 10) : 0;
    return { url: inst.webUrl, port, autoPort: false, runtime: true };
  }
  const extra = inst.extraArgs || '';
  const m = /--port(?:[=\s]+(\d+))?/i.exec(extra);
  const raw = m && m[1] !== undefined ? parseInt(m[1], 10) : 3080;
  const parsed = isNaN(raw) ? 3080 : raw;
  const autoPort = parsed === 0; // --port 0 → OS 自动分配
  const port = autoPort ? 0 : parsed;
  return { url: `http://127.0.0.1:${port || 3080}`, port, autoPort, runtime: false };
}

// semver 与后端 parseVersion 保持一致：x.y.z[-prerelease]
const SEMVER_RE = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;

export function isValidVersion(v: string): boolean {
  return SEMVER_RE.test(v.trim());
}

// Extract `owner/repo` (lowercased) from a GitHub URL, or null.
export function githubRepoOf(url: string): string | null {
  const m = /^https:\/\/github\.com\/([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+?)(?:[/?#].*)?$/.exec(url);
  return m ? m[1].toLowerCase() : null;
}

// Extract `owner/repo` (lowercased) from a `github:owner/repo[#path:/…]` pnpm
// spec, or null.
export function specRepoOf(spec: string): string | null {
  const m = /^github:([A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+?)(?:#path:.*)?$/.exec(spec.trim());
  return m ? m[1].toLowerCase() : null;
}

// Build the canonical GitHub URL from a `github:` spec ("" when not a github
// source) — lets an installed-source favorite record its GitHub address even
// when the package.json has no homepage.
export function githubURLFromSpec(spec: string): string {
  const repo = specRepoOf(spec);
  return repo ? `https://github.com/${repo}` : '';
}

/**
 * 目录只保留"尾巴"（…\父\当前）。
 *
 * 完整路径一律从左边开始被截断，而**一眼能区分两台实例的信息恰恰在右边**（同一批实例
 * 往往共享 `E:\…\dsh-vsn\` 这一长串前缀）。所以凡是"一行里放路径"的地方都走这里，
 * 完整路径交给 tooltip。⭐ 别在调用点各写各的截断 —— 卡片和市场的写法必须一致。
 */
export function shortDir(dir: string): string {
  const trimmed = (dir || '').replace(/[\\/]+$/, '');
  const sep = trimmed.includes('\\') ? '\\' : '/';
  const parts = trimmed.split(/[\\/]/).filter(Boolean);
  if (parts.length <= 2) return trimmed;
  return `…${sep}${parts.slice(-2).join(sep)}`;
}

// ---- 能力门控 ----

/** 面板里某一项的结论（取不到返回 undefined）。 */
export function capabilityOf(caps: CapabilityReport | null | undefined, id: string) {
  return caps?.items.find((i) => i.id === id);
}

/**
 * 内嵌入口的门控原因，'' 表示可以进。
 *
 * 策略是**只在有明确结论时才拦**：
 *  - 插件报告了 `embedRelax=false` → 拦，并把插件给的原因原样显示出来。这正是我们要
 *    抓的静默失效：DSH 改了内部接口，插件跳过打补丁，内嵌必然 401 / 一直「自动重连中」。
 *  - 报告里没有这一项、或压根没取到报告 → **不拦**。因为"没有报告"混淆了多种原因
 *    （插件版本旧、实例没启用自管理重启、报告还没写出来），硬拦会把本来能用的内嵌锁死；
 *    这种情况交给右栏「兼容性」面板说明，而不是禁用按钮。
 */
export function embedGateReason(caps?: CapabilityReport | null): string {
  if (!caps) return '';
  const item = capabilityOf(caps, 'embedRelax');
  if (!item || item.ok) return '';
  return item.reason || '该 DSH 版本未提供内嵌所需的会话接口';
}

/** 兼容性面板是否有**确定的问题**（用于右栏标签上的提示点）。
 *  只统计 !ok && !unknown：unknown 是"没有结论 / 不适用"，标红就是误报。 */
export function hasCapabilityFailure(caps?: CapabilityReport | null): boolean {
  return !!caps?.items.some((i) => !i.ok && !i.unknown);
}

/**
 * 这台实例当前是否存在需要用户注意的兼容性问题（用于「兼容性检查」按钮上的计数）。
 *
 * 只统计**已经跑起来**（ready / running）的实例：启动中不参与判定 —— 能力报告是插件在
 * apply() 时写的，那个窗口里"没有报告"是正常的，报红就是狼来了。
 * （是否挂载插件与实例无关：只取决于全局有没有装 dsh-launcher-plugin，见 self_restart.go。）
 *
 * 计为问题的情况：收到了报告，但里面有**确定的**红灯（例如上游改了内部接口、`embedRelax`
 * 失败）。"没有报告"由启动器分诊：插件没装 / 副本是旧版 / 桥接没起来都属于
 * 已知的良性原因，后端会把这些标成 unknown，这里就不再报警（否则功能一切正常却满屏红灯）。
 */
export function capsAlert(caps: CapabilityReport | undefined, inst: Instance): boolean {
  if (inst.status !== 'ready' && inst.status !== 'running') return false;
  if (!caps) return false; // 后端总会返回报告；取不到说明是前端拿数据的问题，不该报警
  return caps.items.some((i) => !i.ok && !i.unknown);
}

// ---- 顶栏「内置插件连接状态」胶囊 ----

/** RFC3339 → 本地时钟。解析不了就原样返回 —— 宁可显示得笨一点，也别显示 Invalid Date。 */
function clockLabel(at: string): string {
  const t = Date.parse(at);
  if (!Number.isFinite(t)) return at;
  return new Date(t).toLocaleTimeString();
}

/**
 * 顶栏「插件连接状态」胶囊的结论（见 components/Header.tsx）。返回 null = 没有可描述的
 * 实例，调用方整块不渲染。
 *
 * 判据全部来自一次 `GetCapabilities`（纯本地读取）：收到握手 → `plugin` 非空；没收到 →
 * 后端已把良性原因（未安装 / 桥没起来 / 副本是旧版 / 实例没跑）写进 `pluginLoaded` 项的
 * reason 并标记 unknown，前端只负责原样展示。
 *
 * 判定顺序是有意排的：
 *  1. `stale` 先判 —— 报告来自上一次启动时，里面的"红灯"多半是上一轮的遗留，这时候报红
 *     就是狼来了，只作琥珀色的"结论可能过期"。
 *  2. 确定的红灯（`!ok && !unknown`）→ 红点 + 计数。
 *  3. 有握手 → 绿点 + 插件版本。
 *  4. 其余（没握手）→ 中性灰点 + 后端给的原因，**绝不红**。
 */
export function bridgeStatusOf(
  caps: CapabilityReport | undefined,
  inst: { id: string; name: string } | null
): BridgeStatus | null {
  if (!caps || !inst) return null;
  const who = inst.name || inst.id;
  if (caps.stale) {
    return {
      tone: 'stale',
      label: '插件报告可能过期',
      value: '',
      title: `${who}：这次上报的报告来自上一次启动，结论仅供参考；重启这台实例后会重新上报。`,
    };
  }
  const failures = caps.items.filter((i) => !i.ok && !i.unknown);
  if (failures.length > 0) {
    const detail = failures.map((i) => `${i.label}：${i.reason || i.detail || '失败'}`).join('；');
    return {
      tone: 'alert',
      label: '插件告警',
      value: String(failures.length),
      title: `${who}：${detail}`,
    };
  }
  if (caps.plugin) {
    const at = caps.pluginAt ? `，${clockLabel(caps.pluginAt)} 上报` : '';
    return {
      tone: 'ok',
      label: '插件已连接',
      value: caps.plugin.split('@')[1] ?? '',
      title: `${who}：${caps.plugin} 已握手${at}。内嵌 / 主题反向同步 / 重启投递都可用。`,
    };
  }
  const item = capabilityOf(caps, 'pluginLoaded');
  return {
    tone: 'unknown',
    label: '插件未连接',
    value: '',
    title: `${who}：${item?.reason || '还没收到插件的能力报告'}。内嵌 / 主题反向同步 / 重启投递这次不可用。`,
  };
}


