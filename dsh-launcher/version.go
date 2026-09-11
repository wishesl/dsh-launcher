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
