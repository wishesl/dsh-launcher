## 改了什么

<!-- 一两句话说明动机与结果；关联 issue 用 Fixes #123 -->

## 怎么验证的

<!-- 例如：npm run build 通过 / wails build 通过 / 手动跑了 X 场景 -->

## 检查清单

- [ ] `cd dsh-launcher/frontend && npm run build` 通过（tsc + vite）
- [ ] `cd dsh-launcher && wails build` 通过（仅改文档 / CI 时可跳过）
- [ ] 新增的进度 / 日志输出进了**右侧第三栏**，没有做成覆盖式浮层（[AGENTS.md §0](../../blob/master/AGENTS.md)）
- [ ] 传给子组件的回调用了 `useCallback` 稳定化
- [ ] Wails 事件订阅仍然只有 `App.tsx` 一个所有者
- [ ] 提交信息为中文，前缀符合仓库约定（feat / fix / ui / docs / refactor）
