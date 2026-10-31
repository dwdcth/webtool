// wstunnel.go：本地 WebSocket 隧道。
//
// 背景：插件页（如 Bitwarden 的 SignalR 通知）需要连外站 wss://，但
// WebKitGTK 的 WebSocket 不走系统代理，境内自托管服务器直连握手失败；
// 且 wails:// 自定义 scheme 下浏览器也无法发起 ws upgrade 连回宿主。
// 方案：宿主在 127.0.0.1 随机端口自开 WS 入口，bridge.js 把白名单内的
// ws(s):// 改写到该入口，由 Go 侧经 upstreamClient 同款代理链转连上游。
package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"

	"github.com/coder/websocket"

	"webtool/internal/manifest"
)

// WSTunnel 本地 WebSocket 隧道服务。
type WSTunnel struct {
	listener net.Listener
	mux      *http.ServeMux
}

// wsTokenResolver 校验插件 token（*router.Router 实现之）。
type wsTokenResolver interface {
	PluginOf(token string) (string, bool)
}

// wsManifestGetter 获取插件清单（*plugin.Manager 实现之）。
type wsManifestGetter interface {
	Manifest(pluginID string) (*manifest.Manifest, error)
}

// NewWSTunnel 创建并启动隧道（监听 127.0.0.1 随机端口）。
func NewWSTunnel(rtr wsTokenResolver, mgr wsManifestGetter) (*WSTunnel, error) {
	t := &WSTunnel{mux: http.NewServeMux()}
	t.mux.HandleFunc("/tunnel", func(w http.ResponseWriter, r *http.Request) {
		serveWSTunnel(w, r, rtr, mgr)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("ws tunnel listen: %w", err)
	}
	t.listener = ln
	go func() {
		if err := http.Serve(ln, t.mux); err != nil && err != http.ErrServerClosed {
			slog.Error("ws tunnel serve", "error", err)
		}
	}()
	return t, nil
}

// Addr 返回隧道入口地址（ws://127.0.0.1:<port>）。
func (t *WSTunnel) Addr() string { return "ws://" + t.listener.Addr().String() }

// serveWSTunnel 处理单条隧道连接：
// 校验 token 与上游白名单 → 接受浏览器 WS → 经代理链拨上游 → 双向泵。
func serveWSTunnel(w http.ResponseWriter, r *http.Request, rtr wsTokenResolver, mgr wsManifestGetter) {
	rawURL := r.URL.Query().Get("url")
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		http.Error(w, "bad ws url", http.StatusBadRequest)
		return
	}
	pluginID, ok := rtr.PluginOf(r.URL.Query().Get("token"))
	if !ok {
		http.Error(w, "unknown plugin token", http.StatusNotFound)
		return
	}
	mf, err := mgr.Manifest(pluginID)
	if err != nil || !matchUpstreamHost(mf.UpstreamHosts, u.Hostname()) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"upstream host not allowed"}`))
		return
	}

	// 浏览器侧连接：插件页面 Origin 是 wails://localhost 或 127.0.0.1
	// （与隧道端口不同源），跳过 Origin 校验；token 校验已是准入条件
	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept 已写响应
	}
	defer browserConn.CloseNow()

	// 上游侧连接：复用 upstreamClient 的 Transport（含系统代理探测链）
	upstreamConn, _, err := websocket.Dial(r.Context(), rawURL, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: upstreamClient.Transport},
	})
	if err != nil {
		_ = browserConn.Close(websocket.StatusBadGateway, "upstream dial failed")
		return
	}
	defer upstreamConn.CloseNow()

	// 逐消息双向泵：保留 WS 消息边界与类型（SignalR MessagePack 二进制帧依赖）
	err = pumpWS(r.Context(), browserConn, upstreamConn)
	_ = browserConn.Close(websocket.StatusNormalClosure, "done")
	_ = upstreamConn.Close(websocket.StatusNormalClosure, "done")
	_ = err
}

// pumpWS 双向逐消息转发，任一方向结束（对端关闭/出错）即终止两侧。
func pumpWS(ctx context.Context, a, b *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- relayWS(ctx, b, a) }()
	go func() { done <- relayWS(ctx, a, b) }()
	first := <-done
	cancel()
	<-done
	_ = a.CloseNow()
	_ = b.CloseNow()
	return first
}

// relayWS 单向转发 src → dst，逐消息搬运（类型透传）。
func relayWS(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		typ, r, err := src.Reader(ctx)
		if err != nil {
			return err // 含正常关闭
		}
		w, err := dst.Writer(ctx, typ)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, r); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
}
