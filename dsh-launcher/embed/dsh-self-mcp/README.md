# dsh-self-mcp — DSH 自管理重启插件

提供唯一工具 **`dsh-restart`**：重启整个 DSH 进程。重启完成后，本插件会**自动向发起会话注入「重启完成」消息并继续该会话**，方便调试需要重启才生效的插件。

## 工作原理

```
模型调用 dsh-restart(reason, confirm="restart-dsh")
  └─ execute:
       1. 写 <cwd>/.dsh-self-mcp/pending.json（交付意图：sessionId/callId/reason/ts）
       2. 若 DSH_LAUNCHER=1：写 <cwd>/.dsh-self-mcp/restart-request.json（launcher 契约）
          否则：自 spawn 替换进程（兜底）
       3. 返回 {status:"restarting"}，500ms 后请求 ctx.appExit(0) 干净退出

dsh-launcher（监督者）
  └─ 子进程干净退出后，exit-reconcile 发现 restart-request.json
       → 消费即删（杜绝重启循环）→ 自动重新拉起同一实例

新进程 boot，插件重新挂载
  └─ 读 pending.json → 通过本地 web API POST /api/session.prompt
       （与用户发消息完全同路径）向发起会话注入「重启完成」并唤醒继续
     → 成功即删除 pending.json；失败保留待下次启动重试（进程内退避重试，不无限循环）
```

## 装配（内置 + 按实例勾选，零残留）

插件源码内置于 dsh-launcher（`dsh-launcher/embed/dsh-self-mcp/`，`embeddata.go` 用
`//go:embed` 打进 exe）。装配分两步，都由 launcher UI 完成：

1. **安装到全局**（插件市场 → 已安装页签 → dsh-self-mcp 面板「安装到全局」）：
   解出内置源码到 `<profile>/.dsh-builtin/dsh-self-mcp/`，再用常规 pnpm 命令
   （`pnpm add file:<该目录>`）装进全局 profile —— 走命令、不纯 copy，依赖照常解析；
   `.dsh-builtin` 是稳定路径，package.json 里的 `file:` spec 在后续 pnpm rebuild 中不会失效。
2. **实例勾选**（实例表单「启用自管理重启」→ `Instance.SelfRestart`）：
   双重门控 = 插件已装 **&&** 实例勾选 → 本次启动生成临时覆盖层 `.dsh-self-restart-<id>.yml`
   （`insert:` 块新增条目——loader 补丁语义里裸行只按 id 覆盖已有条目、找不到会跳过）：
   ```yaml
   - insert:
       - id: self-restart
         name: 'dsh-self-mcp'
   ```
   并注入 `DSH_LAUNCHER=1`、`DSH_INSTANCE_ID=<id>`。

- **不改 `cordis.patch.yml`**；未勾选的实例（含别的项目/控制台直启）→ 无行引用 →
  工具不存在、状态不碰，零残留。
- 插件未安装时 UI 置灰提示先安装；即使误配也绝不生成覆盖层（fail-soft，实例照常启动）。
- 放弃时：插件市场卸载（`pnpm remove dsh-self-mcp`）即可；`.dsh-self-mcp/` 状态目录
  在实例目录内、已 gitignore、重启完成后自动清空。

## 护栏

- `confirm` 必填且必须为 `restart-dsh`；
- 子代理 / 工作流子代理禁止触发（只看根会话）；
- 已有未交付的重启请求时拒绝（幂等）；
- 重启请求文件被 launcher 消费即删（防循环）。

## 状态文件

| 文件 | 位置 | 作用 |
|---|---|---|
| `pending.json` | `<实例目录>/.dsh-self-mcp/` | 交付意图（跨重启的持久状态） |
| `restart-request.json` | 同上 | launcher 契约：请求自动重新拉起 |

## 验证

- Go：`cd dsh-launcher && go test ./...`（含 `self_restart_test.go`）
- 覆盖层挂载：`node <dsh>/lib/bin.js web --patch .dsh-self-restart-test.yml --port 0 --no-open`
  启动成功后清理临时覆盖层文件（已 gitignore）。
