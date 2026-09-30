# 安全策略

## 支持的版本

| 版本 | 支持 |
|---|---|
| 最新 Release（[Releases 页](https://github.com/wishesl/dsh-launcher/releases)） | ✅ |
| 更早版本 / 本地构建（`dev`） | ❌ 不保证 |

先升级到最新 Release 再确认问题是否仍在。

## 报告漏洞

**请不要公开开 issue 报安全问题。** 通过 GitHub 的私密渠道提交：

👉 [Report a vulnerability（Security Advisory）](https://github.com/wishesl/dsh-launcher/security/advisories/new)

需要包含：影响范围（哪个入口、什么条件下触发）、复现步骤、可能的攻击面。收到后会尽快确认并安排修复；在修复发布前不会公开细节。

## 不算安全问题的范畴

- 操作系统 / WebView2 自身的提示（如「未知发布者」——产物未做代码签名，README 已注明）。
- 上游 DSH 的行为问题（请到 DSH 上游仓库报告）。
