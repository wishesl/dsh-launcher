// 启动器换肤：跟随实例内 dsh-launcher-plugin 推送的 ui-theme。
// 单一入口 —— App.tsx 是唯一的事件所有者，收到 dsh:theme 就调这里。
// 设计见《dsh-launcher-plugin实现方案.md》§2.1 / §2.4。
//
// 三态语义：
//   light / dark → 直接落到 <html data-theme>，CSS 里是两条确定的覆盖段；
//   system       → 落 "system"，由 CSS 的 @media (prefers-color-scheme: dark)
//                  解析（插件/launcher 都不做二次解析，见方案 §2.4）；
//                  原生标题栏用 WindowSetSystemDefaultTheme() 跟系统走。
// 我们**不再**自己写 localStorage —— DSH 的 ui-theme 是唯一真相（存在共享 profile，
// 所以多实例天然一致），本地再存一份只会多出一个会打架的真相。
import {
  WindowSetDarkTheme,
  WindowSetLightTheme,
  WindowSetSystemDefaultTheme,
} from '../wailsjs/runtime/runtime';

export type ThemePreference = 'light' | 'dark' | 'system';

/** 把任意上报值归一为三态（后端已归一；前端再兜一层，脏值当 system）。 */
export function normalizeThemePreference(raw: unknown): ThemePreference {
  return raw === 'light' || raw === 'dark' ? raw : 'system';
}

/**
 * 应用一次主题。
 *
 * `persist=false` 用于启动时的初值：这时还没有任何实例上报，写 localStorage 只会把
 * "上次的答案"钉成"本次的答案"，而它可能已经过期。挂载后实例一连上就会推真正的一份。
 */
export function applyThemePreference(raw: unknown, persist = true): ThemePreference {
  const pref = normalizeThemePreference(raw);
  const root = document.documentElement;
  if (root.dataset.theme !== pref) root.dataset.theme = pref;
  try {
    if (persist) window.localStorage?.setItem('dsh-launcher-theme', pref);
  } catch {
    /* 隐私模式 / 存储被禁：无所谓，主题本来就能自愈（实例一上报就回来） */
  }
  syncNativeWindowTheme(pref);
  return pref;
}

/** 启动时的初值：本地缓存 → 系统偏好。没有缓存时不写回（见 persist 说明）。 */
export function bootstrapTheme(): ThemePreference {
  let cached: string | null = null;
  try {
    cached = window.localStorage?.getItem('dsh-launcher-theme') ?? null;
  } catch {
    cached = null;
  }
  if (cached === 'light' || cached === 'dark' || cached === 'system') {
    return applyThemePreference(cached, false);
  }
  return applyThemePreference('system', false);
}

/**
 * 原生窗口主题（标题栏 / 边框）跟 CSS 的 `data-theme` 对齐。
 *
 * 浏览器预览里 wailsjs runtime 的 `window.runtime` 不存在（见 AGENTS.md §8.4），
 * 直接调会抛——所以整段包在 try 里：换肤是外观增强，绝不能因为它让应用白屏。
 */
function syncNativeWindowTheme(pref: ThemePreference): void {
  try {
    if (pref === 'light') WindowSetLightTheme();
    else if (pref === 'dark') WindowSetDarkTheme();
    else WindowSetSystemDefaultTheme();
  } catch {
    /* 非 Wails 环境（浏览器预览）—— 忽略 */
  }
}
