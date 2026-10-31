package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"webtool/internal/hostservice"
	"webtool/internal/paths"
	"webtool/internal/plugin"
	"webtool/internal/pluginapi"
	"webtool/internal/router"
	"webtool/internal/server"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := paths.EnsureDirs(); err != nil {
		slog.Error("初始化数据目录失败", "error", err)
		os.Exit(1)
	}

	mgr := plugin.NewManager(paths.PluginsDir(), paths.DataDir()+"/plugins.json")
	if err := mgr.LoadInstalled(); err != nil {
		slog.Warn("加载已安装插件失败", "error", err)
	}
	rtr := router.New()
	api := pluginapi.New(mgr)
	assetSvc := server.NewAssetService(mgr, rtr, api)
	// 桥脚本版本化：内容摘要进注入标签的 ?v= 查询，
	// 避免 webview 在升级后继续使用缓存的旧 bridge.js
	if b, err := fs.ReadFile(assets, "frontend/dist/host/bridge.js"); err == nil {
		sum := sha256.Sum256(b)
		assetSvc.SetBridgeVersion(hex.EncodeToString(sum[:4]))
	}
	// 本地 WebSocket 隧道：插件内白名单外站 ws(s):// 改写至此转连
	// （SignalR 通知等；WebKitGTK 的 WS 不走系统代理）
	if tunnel, err := server.NewWSTunnel(rtr, mgr); err == nil {
		assetSvc.SetWSTunnel(tunnel)
	} else {
		slog.Error("WS 隧道启动失败（插件外站 WebSocket 将直连）", "error", err)
	}
	rulesPath := filepath.Join(paths.DataDir(), "rules.json")
	assetSvc.SetGlobalRules(server.LoadOrCreateGlobalRules(rulesPath))
	hostSvc := hostservice.NewPluginService(mgr, rtr, assetSvc, rulesPath)

	frontend, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		slog.Error("嵌入前端资源失败", "error", err)
		os.Exit(1)
	}

	app := application.New(application.Options{
		Name:        "WebTool",
		Description: "插件化网页工具箱",
		LogLevel:    slog.LevelInfo,
		Services: []application.Service{
			application.NewServiceWithOptions(hostSvc, application.ServiceOptions{
				Name: "webtool.host",
			}),
			application.NewServiceWithOptions(assetSvc, application.ServiceOptions{
				Name:  "webtool.assets",
				Route: "/p/",
			}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontend),
			// 根级绝对路径请求按 Referer 回填到来源插件的 token 空间
			Middleware:     server.RefererRewriteMiddleware,
			DisableLogging: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.webtool.desktop",
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     "WebTool",
		Width:     1280,
		Height:    860,
		MinWidth:  960,
		MinHeight: 640,
	})

	if err := app.Run(); err != nil {
		slog.Error("应用退出", "error", err)
		os.Exit(1)
	}
}
