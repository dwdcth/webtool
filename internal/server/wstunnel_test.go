package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newWSEchoServer 启动本地 WS echo 上游（收到一条回一条，加前缀标记）。
func newWSEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			ctx := r.Context()
			typ, rd, err := c.Reader(ctx)
			if err != nil {
				return
			}
			wr, err := c.Writer(ctx, typ)
			if err != nil {
				return
			}
			if _, err := wr.Write([]byte("echo:")); err != nil {
				return
			}
			if _, err := io.Copy(wr, rd); err != nil {
				return
			}
			if err := wr.Close(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(up.Close)
	return up
}

func TestWSTunnelEcho(t *testing.T) {
	up := newWSEchoServer(t)
	upWS := "ws" + strings.TrimPrefix(up.URL, "http")

	m, _, r := newTestStack(t)
	installPluginZip(t, m, map[string]string{
		"package.json": `{"name":"wsplug","unihub":{"id":"wsplug"},
			"webtool":{"upstreamHosts":["127.0.0.1"]}}`,
		"index.html": `<html></html>`,
	})
	tok := r.TokenFor("wsplug")

	tunnel, err := NewWSTunnel(r, m)
	if err != nil {
		t.Fatalf("隧道启动失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 正常转发：经隧道连 echo 上游，消息往返
	target := tunnel.Addr() + "/tunnel?token=" + tok + "&url=" + url.QueryEscape(upWS)
	c, _, err := websocket.Dial(ctx, target, nil)
	if err != nil {
		t.Fatalf("隧道连接失败: %v", err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	_, msg, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(msg) != "echo:hello" {
		t.Fatalf("echo 内容不对: %q", string(msg))
	}

	// 无效 token → 拒绝
	if bad, _, err := websocket.Dial(ctx,
		tunnel.Addr()+"/tunnel?token=deadbeef&url="+url.QueryEscape(upWS), nil); err == nil {
		bad.CloseNow()
		t.Fatal("无效 token 不应连上")
	}

	// 非白名单 host → 拒绝（白名单只放行 127.0.0.1）
	if bad, _, err := websocket.Dial(ctx,
		tunnel.Addr()+"/tunnel?token="+tok+"&url="+url.QueryEscape("wss://example.com/hub"), nil); err == nil {
		bad.CloseNow()
		t.Fatal("非白名单 host 不应连上")
	}
}
