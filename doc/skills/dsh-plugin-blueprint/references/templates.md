# 可复制骨架：双半边 manifest + 两侧入口

> 读它的时机：要开一个新插件包时。骨架取自本仓库 `dsh-launcher-plugin` 0.2.8（真机跑通）与官方 `ui-theme` 的 manifest 形状。

## A 双半边插件包（不含 bundle 声明，最常见）

行（entry）由**外部 patch** 提供：profile 的 `cordis.patch.yml`、别的 bundle 的 patch、或 overlay。

```json
{
  "name": "dsh-plugin-example",
  "version": "0.1.0",
  "type": "module",
  "main": "lib/index.js",
  "exports": {
    ".": { "types": "./lib/types/index.d.ts", "default": "./lib/index.js" },
    "./client": { "types": "./lib/types/client/index.d.ts", "default": "./lib/client.js" },
    "./package.json": "./package.json"
  },
  "files": ["lib", "README.md"],
  "dsh": {
    "client": {
      "platform": "web",
      "inject": ["@deepseek-ai/dsh-client-ui-theme"],
      "immediately": true
    }
  },
  "dependencies": { "@deepseek-ai/schemastery": "^3.18.1" },
  "peerDependencies": { "@deepseek-ai/cordis": "~4.0.4" }
}
```

要点：
- `dsh.client.platform === "web"` 且 `exports["./client"]` 能解析出 string，否则永远进不了 boot graph。
- `dsh.client.inject` 是**包名依赖信息**，不是 Cordis 服务注入。
- `exports` 里 `types` 可省（纯 JS 插件），但 `default` 必须指到真产物。
- 兼容性只看 `peerDependencies`；`engines.dsh` 与 `dsh.manifestVersion` 不强制。

## B bundle 包（自带 patch，`dsh plugin add` 会追加进 `dsh.profile.bundles`）

```json
{
  "name": "dsh-bundle-example",
  "version": "0.1.0",
  "type": "module",
  "main": "lib/index.js",
  "dsh": { "bundle": { "patch": "cordis.patch.yml" } }
}
```

`cordis.patch.yml`：

```yaml
- insert:
    - id: example
      name: dsh-plugin-example      # 包 specifier，Loader 从 profile/node_modules 解析
      config:
        someField: true
```

## C 宿主半边入口 `lib/index.js`

```js
import z from "@deepseek-ai/schemastery";

export const name = "dsh-plugin-example";     // 插件名，可与包名不同
export const inject = ["settings", "timer"];  // 要用的服务必须写全，否则抛 without inject
export const Config = z.object({
  enabled: z.boolean().default(true),         // 没有字段也必须写 Config
});

export function apply(ctx, config) {
  // 1) 资源一律在这里注册，随 fiber 销毁自动清理
  const off = ctx.on("some/event", (payload) => {
    try { /* 回调内吞异常，别让未捕获异常打挂进程 */ } catch (err) { console.error(err); }
  });
  ctx.effect(() => () => off(), "example: event");

  // 2) 定时器：ctx 是注入后的 ctx，disposer 自己记账
  let timer;
  const tick = () => { try { /* 做一次事 */ } catch (e) { console.error(e); } arm(); };
  const arm = () => { timer = ctx.timeout(tick, 5000); };
  arm();
  ctx.effect(() => () => timer?.(), "example: timer");
}
```

**不要**用 `apply` 的原始 ctx 调 `ctx.timeout`（会抛 `without inject`），也不要把定时器排在可能已 dispose 的 ctx 上（`inactive context`）。

## D 客户端半边入口 `lib/client.js`

```js
window.__ModuleLoader__.load({
  id: "dsh-plugin-example",          // 必须等于 package.json 的 name
  factory(require) {
    const React = require("react");  // 只有 baseline 9 条能直接 require
    return {
      inject: ["slots", "theme", "layout"],
      apply(ctx) {
        const dispose = ctx.slots.inject("conversation.composer.dock", () =>
          ctx.slots.register({ name: "example", id: "example:panel", order: 10 }, Panel),
        );
        ctx.effect(() => () => dispose(), "example: slot");
        function Panel() { return React.createElement("div", null, "hello"); }
      },
    };
  },
});
```

要发 chunk 用 `require.async("./client.extra.js")`（构建时切分，chunk 第 1 行同样调 `load()`）。

## E 装上去 + 验证

```powershell
# 装（走 pnpm 转发，cwd = profile）
dsh plugin --profile web add "file:C:/path/to/dsh-plugin-example"
# 或让启动器/宿主工具走 plugin_manager install_bundle

# 合成后的 profile（看行有没有、config 对不对）
dsh --profile web --dump-config
```

验证顺序：**先看安装返回的 `application`/`warnings` 字段，再 `cordis_inspect_query` 确认行存在，最后才看日志**；客户端看控制台 `web boot: …`。改了已安装包的 JS ⇒ **必须重启**才加载新模块代。
