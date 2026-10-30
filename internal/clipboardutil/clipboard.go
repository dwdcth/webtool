// Package clipboardutil 系统剪贴板图片读取。
//
// 为什么不用 golang-design/clipboard：该库在 Linux 上依赖 xclip/xsel
// 外部命令（无 Wayland 原生支持）；本开发机为 Wayland 会话且只装了
// wl-clipboard，故自行实现：优先 wl-paste（Wayland 原生），
// xclip 作为 X11/XWayland 兜底。未来需要 macOS/Windows 图片剪贴板时
// 再引入该库（其跨平台部分为 CGo 实现）。
package clipboardutil

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"time"
)

// ReadImagePNG 读取剪贴板中的 PNG 图片，返回 dataURL；
// 剪贴板无图片或工具不可用时返回空串（不报错，调用方按无图处理）。
func ReadImagePNG() string {
	var data []byte
	if b := runClipboardCmd(2*time.Second, "wl-paste", "-t", "image/png"); len(b) > 8 {
		data = b
	} else if b := runClipboardCmd(2*time.Second, "xclip", "-selection", "clipboard", "-t", "image/png", "-o"); len(b) > 8 {
		data = b
	}
	if data == nil {
		return ""
	}
	// PNG 魔数校验，避免把错误文本当图片
	if data[0] != 0x89 || data[1] != 'P' || data[2] != 'N' || data[3] != 'G' {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// ReadText 读剪贴板文本（wl-paste → xclip → xsel），失败返回空串。
func ReadText() string {
	if b := runClipboardCmd(2*time.Second, "wl-paste"); len(b) > 0 {
		return string(b)
	}
	if b := runClipboardCmd(2*time.Second, "xclip", "-selection", "clipboard", "-o"); len(b) > 0 {
		return string(b)
	}
	if b := runClipboardCmd(2*time.Second, "xsel", "-ob"); len(b) > 0 {
		return string(b)
	}
	return ""
}

// WriteText 写文本到剪贴板（wl-copy → xclip）。
func WriteText(text string) bool {
	if path, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command(path) //nolint:gosec // 固定命令
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run() == nil
	}
	if path, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command(path, "-selection", "clipboard", "-i") //nolint:gosec // 固定参数
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run() == nil
	}
	return false
}

func runClipboardCmd(timeout time.Duration, name string, args ...string) []byte {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil
	}
	cmd := exec.Command(path, args...) //nolint:gosec // 固定参数
	done := make(chan []byte, 1)
	go func() {
		out, _ := cmd.Output()
		done <- out
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return nil
	}
}
