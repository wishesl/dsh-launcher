//go:build linux

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Linux 系统通知：libnotify 的 notify-send（桌面环境基本都带）。
//
// 没装就是"该环境没有通知后端"：fail-open，调用方只记一行日志，不影响通知之外的任何功能
// （也不重试 —— 见 notify.go 顶部说明）。
func platformNotify(title, body string) error {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return errors.New("没有 notify-send（libnotify），该环境不支持系统通知")
	}
	out, err := exec.Command(path, "-a", "DSH Launcher", title, body).CombinedOutput()
	if err != nil {
		return fmt.Errorf("notify-send: %w（%s）", err, strings.TrimSpace(string(out)))
	}
	return nil
}
