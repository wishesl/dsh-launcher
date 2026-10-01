# 实战教训（本仓库 dsh-launcher-plugin 0.2.6–0.2.9）+ 参考坐标

> 读它的时机：写"跨装载/时序/长连接/依赖就绪"类插件之前；这五条都是真机上崩过、丢过功能、或误判过之后才总结出来的。

## 1 五条教训

- **定时器**：`cordis-plugin-timer` 的 `ctx.timeout` 把 `setTimeout` 注册在**timer 服务自己的 ctx** 的 effect 上，**不随调用方 fiber 销毁而取消**；用 apply 的 ctx 续排 5s 轮询 ⇒ fiber 失效后回调照样跑，服务解析抛 `cannot get required service "timer" in inactive context`，未捕获直接打挂进程（启动 5s 后 exit 1，每次必崩）。修法：`ctx.inject(["timer"], ...)` 拿注入 ctx + 自己记账 disposer + 回调 try/catch。
- **双装载**：声明 `dsh.client` 后宿主重复装载服务端半边，模块级幂等闩（`pendingHandled = true`）永久闩死 ⇒ 新会话再也补交不了"重启完成"消息。修法：进程级 `pendingDelivery` + 每次读**当时**的 ctx/会话 + 单会话单飞 + `gen` 防陈旧 + 用尽后释放幂等闸。
- **可观测性**：`ctx.logger` 输出既不进实例日志也不进 app.log（真机实测）⇒ 交付链必须走 `console.error` + 桥接帧这类**可见**痕迹。"看不见的成功"和失败一样致命。
- **架构必然性**：DSH 服务端**没有** setTheme，只有页面侧 `setTheme`（乐观重绘）；服务端写 `settings.update` 每次 330–353ms（两次 describe + 文件锁 + HMR + atomic write）。想即时就必须新增一条**页面通道** —— 而页面通道会把上面所有生命周期问题一起引爆。**新通道 = 生命周期契约重审**，不是意外。
- **"未就绪"不是"失败"**（0.2.8 → 0.2.9）：重启后 `sessionController` 比插件晚装配 1~2 秒。0.2.8 把它当失败：烧掉 6 次重试、约 2 秒，靠"重新装载的握手"才救回来，还打了一句错话（`该 profile 未挂载 Web 会话控制器` —— profile 明明有）。0.2.9 改用 `ctx.inject(["sessionController"], cb)` 的**就绪回调**驱动交付 + 带 `SESSION_CONTROLLER_PENDING` 标记的"可等待"错误（不消耗重试预算）：真机复跑变成 **第 1 次尝试、约 1 秒、0 次重试**。
  ⇒ 通用规则：**依赖取不到时先问"是不是还没装配"**；就绪用事件（`inject` 回调）等，不要用重试次数等；错误文案只写确证的事实。

## 2 参考坐标

- 官方 skill（`@deepseek-ai/dsh-agent-preset` 包的 `skills/`，由 `cordis` preset 挂载，web profile 会话里默认不可见）：
  - `cordis-plugin-development/SKILL.md` + `references/{host-plugin,ui-plugin,practices,verification,mcp-bundle,user-actions}.md` + `templates/decoration/`
  - `cordis-composition-reference/SKILL.md` + `references/packages.md`（按包组列出每个可装载插件包及是否吃 config，**用 grep 搜不要整读**）
- `@deepseek-ai/dsh-tool-cordis`：`cordis_inspect_list` / `cordis_inspect_query` 的宿主侧实现；`Config.listConfigs` 分页列 Loader entry 并给出 `packageDir`。
- 本仓库真实样例：`dsh-launcher/embed/dsh-launcher-plugin/`（0.2.9，双半边 + 自开 WebSocket 桥 + 自重启阶梯 + 能力握手 + 就绪驱动交付，含 `lib/index.js` / `lib/client.js` / `README.md` 的帧协议与生命周期说明）。
