//go:build !windows && !darwin && !linux

package main

import "errors"

// 其它平台没有系统通知后端：fail-open，调用方只记一行日志。
func platformNotify(title, body string) error {
	return errors.New("该平台没有系统通知后端")
}
