package main

import (
	"embed"
	"os"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 自更新重启（RestartLauncherNow 拉起的新进程）：必须先等老进程退出，再进 wails.Run ——
	// Wails 的单实例互斥体是在 wails.Run 里创建的，抢在前面会被当成"第二实例"直接 os.Exit(0)。
	if pid := updatedFromPID(os.Args); pid > 0 {
		waitForProcessExit(pid, 30*time.Second)
	}
	// 上一次自更新留下的 .old / 半截 .part：启动时清掉（best-effort，删不掉不算错）。
	cleanupUpdateLeftovers()

	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "DSH Launcher",
		Width:     1280,
		Height:    820,
		MinWidth:  1000,
		MinHeight: 640,
		// 去掉原生标题栏（含系统自带的最小化/最大化/关闭），窗口控制改由
		// Header 右上角自定义按钮实现（见 Header.tsx 的 win-controls）。
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 252, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.onBeforeClose,
		SingleInstanceLock: &options.SingleInstanceLock{
			// A second launch of the exe (or `wails dev`) exits immediately and
			// asks the running instance to bring its window back to the front.
			UniqueId: "dsh-launcher",
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				app.showWindow()
			},
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
