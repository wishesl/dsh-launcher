//go:build windows

package main

import (
	"sync"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
)

// Windows 系统通知：WinRT Toast（go-toast）。
//
// AppID 固定 "DSH Launcher"：SetAppData 只写 HKCU\SOFTWARE\Classes\AppUserModelId\<AppID>
// （不需要管理员权限），只为让通知显示可读的应用名；失败也不影响 Push。
//
// Push 内部是 COM 优先 + PowerShell 兜底（wintoast.PowershellFallback）：COM 失败时仍会弹出来，
// 但会返回一个非 nil 错误（errors.Join 保留 COM 错误）—— 调用方据此只记一行日志，**不**断言
// "没弹出来"（见 notify.go 顶部说明）。
//
// 刻意**不**在这里预初始化 COM（ole.RoInitialize）：go-toast 自己的 initialize() 把
// "该线程已初始化"（S_FALSE）当错误，预初始化会毒化它的判定，反而每次都退到 PowerShell。
const notifyAppID = "DSH Launcher"

var notifyAppDataOnce sync.Once

func platformNotify(title, body string) error {
	notifyAppDataOnce.Do(func() {
		_ = toast.SetAppData(toast.AppData{AppID: notifyAppID})
	})
	n := toast.Notification{AppID: notifyAppID, Title: title, Body: body}
	return n.Push()
}
