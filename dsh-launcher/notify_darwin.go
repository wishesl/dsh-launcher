//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// macOS 系统通知：osascript 的 display notification（系统自带，不引第三方依赖）。
func platformNotify(title, body string) error {
	script := fmt.Sprintf("display notification %s with title %s",
		appleScriptString(body), appleScriptString(title))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript: %w（%s）", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// appleScriptString 把一个字符串转成 AppleScript 字面量：转义反斜杠与双引号。正文已经在
// notify.go 里压成一行，这里不必处理换行。
func appleScriptString(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}
