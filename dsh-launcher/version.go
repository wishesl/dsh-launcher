package main

// version 是启动器自身的版本号，显示在顶栏品牌区的版本 pill 上。
//
// 发版时用构建参数注入，不要在源码里手工改它：
//
//	wails build -ldflags "-X main.version=0.1.5"
//
// 没注入时保持 "dev"，前端会原样显示成一个小写等宽标签（表示本地构建），
// 而不是假装成一个正式版本号。
var version = "dev"

// GetLauncherVersion 返回启动器版本（顶栏品牌区用）。绑定为
// window.go.main.App.GetLauncherVersion。
func (a *App) GetLauncherVersion() string {
	return version
}

// bestFitDSHVersion 是**本次启动器构建实际验证过**的 DSH 版本 —— 不是"最新版"，
// 而是"我们知道各项能力都正常工作的那个"。
//
// 它只用于**推荐**，绝不用于门控。这个区别很重要：版本号是上游控制的时间戳，用户装的是
// latest，任何"版本 → 启用什么"的表都必然滞后一步；功能到底能不能用，始终由能力探测决定
// （见 capabilities.go），这里只负责告诉用户"我们试过这个"。
//
// 验证过一个新版本之后改这里，或用构建参数覆盖：
//
//	wails build -ldflags "-X main.bestFitDSHVersion=0.1.6-rc.1"
var bestFitDSHVersion = "0.1.7-rc.2"

// GetBestFitVersion 返回启动器最佳适配的 DSH 版本（版本列表 / 实例表单打标签用）。
func (a *App) GetBestFitVersion() string {
	return bestFitDSHVersion
}
