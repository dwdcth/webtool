// Package pluginapi 实现 /p/<token>/api/<domain>/<method> 的能力 API。
// 请求来自沙箱 iframe（opaque origin），因此所有响应带 CORS *；
// 权限在 Go 侧按 token 解析出的插件 id 强制校验，前端只是管道。
package pluginapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"webtool/internal/clipboardutil"
	"webtool/internal/plugin"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const maxBodyBytes = 10 << 20 // 10MB
const maxHTTPRespBytes = 32 << 20

type API struct {
	mgr *plugin.Manager

	mu     sync.Mutex
	stores map[string]*store
}

func New(mgr *plugin.Manager) *API {
	return &API{mgr: mgr, stores: map[string]*store{}}
}

type handlerFunc func(a *API, pluginID string, args map[string]any) (any, error)

// notImplemented 返回占位 handler（方法签名与能力域留待后续阶段实现）。
func notImplemented(name string) handlerFunc {
	return func(a *API, pluginID string, args map[string]any) (any, error) {
		return nil, fmt.Errorf("%s 尚未实现", name)
	}
}

var routes = map[string]map[string]handlerFunc{
	"storage": {
		"get":    (*API).storageGet,
		"set":    (*API).storageSet,
		"delete": (*API).storageDelete,
		"keys":   (*API).storageKeys,
		"clear":  (*API).storageClear,
	},
	"clipboard": {
		"read-text":  (*API).clipboardReadText,
		"write-text": (*API).clipboardWriteText,
		// read-all：文本 + 图片（图片经 wl-paste/xclip 读系统剪贴板）。
		// 用于插件内 navigator.clipboard.read 失败时的宿主兜底。
		"read-all": (*API).clipboardReadAll,
	},
	"http": {
		"get":     (*API).httpGet,
		"post":    (*API).httpPost,
		"request": (*API).httpRequest,
	},
	"fs": {
		"read-file":        (*API).fsReadFile,
		"write-file":       (*API).fsWriteFile,
		"read-dir":         (*API).fsReadDir,
		"exists":           (*API).fsExists,
		"stat":             (*API).fsStat,
		"mkdir":            notImplemented("fs.mkdir"),
		"select-file":      (*API).fsSelectFile,
		"select-directory": fsSelectDirectory,
	},
	"system": {
		"get-info":       (*API).systemGetInfo,
		"open-external":  (*API).systemOpenExternal,
		"show-in-folder": (*API).systemShowInFolder,
	},
	"node": {
		"get-plugin-dir": (*API).nodeGetPluginDir,
		"read-file":      (*API).nodeReadFile,
		"write-file":     (*API).nodeWriteFile,
		"read-dir":       notImplemented("node.read-dir"),
		"exists":         notImplemented("node.exists"),
		"stat":           notImplemented("node.stat"),
		"mkdir":          notImplemented("node.mkdir"),
		"spawn":          notImplemented("node.spawn"),
	},
}

// Handle 处理一次 API 调用。
func (a *API) Handle(w http.ResponseWriter, r *http.Request, pluginID, domain, method string) {
	corsHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	domainRoutes, ok := routes[domain]
	if !ok {
		writeResult(w, nil, fmt.Errorf("未知 API 域 %q", domain))
		return
	}
	h, ok := domainRoutes[method]
	if !ok {
		writeResult(w, nil, fmt.Errorf("未知方法 %s/%s", domain, method))
		return
	}
	if r.Method != http.MethodPost {
		writeResult(w, nil, fmt.Errorf("仅支持 POST"))
		return
	}
	args := map[string]any{}
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			writeResult(w, nil, fmt.Errorf("读取请求体失败: %v", err))
			return
		}
		if len(b) > 0 {
			if err := json.Unmarshal(b, &args); err != nil {
				writeResult(w, nil, fmt.Errorf("请求体须为 JSON 对象: %v", err))
				return
			}
		}
	}
	data, err := h(a, pluginID, args)
	writeResult(w, data, err)
}

func corsHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Cache-Control", "no-store")
}

func writeResult(w http.ResponseWriter, data any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	resp := map[string]any{"ok": err == nil, "data": data}
	if err != nil {
		resp["error"] = err.Error()
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ---- 参数工具 ----

func argString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func argAny(args map[string]any, key string) any {
	return args[key]
}

func (a *API) require(pluginID, perm string) error {
	if !a.mgr.HasPermission(pluginID, perm) {
		return fmt.Errorf("权限不足：需要 %q（请在 package.json 的 unihub.permissions 中声明并在安装时确认）", perm)
	}
	return nil
}

// ---- storage（按插件隔离，无需权限声明）----

func (a *API) storeFor(pluginID string) *store {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.stores[pluginID]; ok {
		return s
	}
	s := newStore(a.mgr, pluginID)
	a.stores[pluginID] = s
	return s
}

func (a *API) storageGet(pluginID string, args map[string]any) (any, error) {
	return a.storeFor(pluginID).Get(argString(args, "key")), nil
}

func (a *API) storageSet(pluginID string, args map[string]any) (any, error) {
	return nil, a.storeFor(pluginID).Set(argString(args, "key"), argAny(args, "value"))
}

func (a *API) storageDelete(pluginID string, args map[string]any) (any, error) {
	return nil, a.storeFor(pluginID).Delete(argString(args, "key"))
}

func (a *API) storageKeys(pluginID string, _ map[string]any) (any, error) {
	return a.storeFor(pluginID).Keys(), nil
}

func (a *API) storageClear(pluginID string, _ map[string]any) (any, error) {
	return nil, a.storeFor(pluginID).Clear()
}

// ---- clipboard ----
// 全部经 clipboardutil（wl-clipboard/xclip/xsel），不依赖 Wails 运行时，
// harness 等无应用环境同样可用。

func (a *API) clipboardReadText(pluginID string, _ map[string]any) (any, error) {
	if err := a.require(pluginID, "clipboard"); err != nil {
		return nil, err
	}
	return map[string]any{"text": clipboardutil.ReadText()}, nil
}

func (a *API) clipboardWriteText(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "clipboard"); err != nil {
		return nil, err
	}
	if !clipboardutil.WriteText(argString(args, "text")) {
		return nil, fmt.Errorf("写入剪贴板失败（需要 wl-copy 或 xclip）")
	}
	return nil, nil
}

// clipboardReadAll 返回 {text, dataUrl}：文本与图片均来自系统剪贴板
// （wl-paste/xclip），两者均可为空。用于插件 navigator.clipboard 兜底。
func (a *API) clipboardReadAll(pluginID string, _ map[string]any) (any, error) {
	if err := a.require(pluginID, "clipboard"); err != nil {
		return nil, err
	}
	return map[string]any{
		"text":    clipboardutil.ReadText(),
		"dataUrl": clipboardutil.ReadImagePNG(),
	}, nil
}

// ---- http（宿主代理执行，绕 CORS）----

func (a *API) httpGet(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "http"); err != nil {
		return nil, err
	}
	return a.doHTTPRequest(http.MethodGet, argString(args, "url"), argString(args, "contentType"), headerMap(args), "")
}

func (a *API) httpPost(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "http"); err != nil {
		return nil, err
	}
	body, err := normalizeBody(argAny(args, "data"), argString(args, "contentType"))
	if err != nil {
		return nil, err
	}
	ct := argString(args, "contentType")
	if ct == "" {
		ct = "application/json"
	}
	return a.doHTTPRequest(http.MethodPost, argString(args, "url"), ct, headerMap(args), body)
}

func (a *API) httpRequest(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "http"); err != nil {
		return nil, err
	}
	method := strings.ToUpper(argString(args, "method"))
	if method == "" {
		method = http.MethodGet
	}
	body, err := normalizeBody(argAny(args, "body"), "")
	if err != nil {
		return nil, err
	}
	return a.doHTTPRequest(method, argString(args, "url"), argString(args, "contentType"), headerMap(args), body)
}

func headerMap(args map[string]any) map[string]string {
	out := map[string]string{}
	if raw, ok := args["headers"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// normalizeBody 把 data（对象→JSON 字符串 / 字符串→原样）转为请求体。
func normalizeBody(data any, contentType string) (string, error) {
	if data == nil {
		return "", nil
	}
	switch v := data.(type) {
	case string:
		return v, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

func (a *API) doHTTPRequest(method, url, contentType string, headers map[string]string, body string) (any, error) {
	if url == "" || !(strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) {
		return nil, fmt.Errorf("url 必须以 http:// 或 https:// 开头")
	}
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return nil, err
	}
	if contentType != "" && body != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPRespBytes))
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	outHeaders := map[string]string{}
	for k := range resp.Header {
		outHeaders[k] = resp.Header.Get(k)
	}
	result := map[string]any{
		"status":      resp.StatusCode,
		"statusText":  resp.Status,
		"headers":     outHeaders,
		"contentType": ct,
		"isImage":     false,
	}
	if strings.HasPrefix(ct, "image/") {
		result["isImage"] = true
		result["body"] = "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(raw)
	} else {
		result["body"] = string(raw)
	}
	return result, nil
}

// ---- fs（unihub.fs 兼容：全盘访问，需 fs 权限）----

func (a *API) fsReadFile(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "fs"); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(argString(args, "path"))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (a *API) fsWriteFile(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "fs"); err != nil {
		return nil, err
	}
	p := argString(args, "path")
	if p == "" {
		return nil, fmt.Errorf("path 不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return nil, os.WriteFile(p, []byte(argString(args, "content")), 0o644)
}

func (a *API) fsReadDir(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "fs"); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(argString(args, "path"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func (a *API) fsExists(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "fs"); err != nil {
		return nil, err
	}
	_, err := os.Stat(argString(args, "path"))
	return err == nil, nil
}

func (a *API) fsStat(pluginID string, args map[string]any) (any, error) {
	if err := a.require(pluginID, "fs"); err != nil {
		return nil, err
	}
	st, err := os.Stat(argString(args, "path"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"isFile":    !st.IsDir(),
		"isDirectory": st.IsDir(),
		"size":      st.Size(),
		"mtime":     st.ModTime().Format(time.RFC3339),
	}, nil
}

func (a *API) fsSelectFile(_ string, _ map[string]any) (any, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	return path, nil
}

func fsSelectDirectory(a *API, _ string, _ map[string]any) (any, error) {
	path, err := application.Get().Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		return nil, err
	}
	return path, nil
}

// ---- system ----

func (a *API) systemGetInfo(_ string, _ map[string]any) (any, error) {
	env := application.Get().Env.Info()
	return map[string]any{
		"platform": env.OS,
		"arch":     env.Arch,
	}, nil
}

func (a *API) systemOpenExternal(_ string, args map[string]any) (any, error) {
	url := argString(args, "url")
	if !(strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")) {
		return nil, fmt.Errorf("仅允许 http/https 链接")
	}
	if err := application.Get().Browser.OpenURL(url); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *API) systemShowInFolder(_ string, args map[string]any) (any, error) {
	return nil, application.Get().Env.OpenFileManager(argString(args, "path"), false)
}

// ---- node（沙箱：限制在插件目录内，无需权限声明）----

// safePluginPath 把插件提供的相对/绝对路径安全映射到插件目录内。
func safePluginPath(m *plugin.Manager, pluginID, rel string) (string, error) {
	mf, err := m.Manifest(pluginID)
	if err != nil || mf == nil {
		return "", fmt.Errorf("插件 %s 清单不可用", pluginID)
	}
	clean := filepath.Clean("/" + strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	full := filepath.Join(mf.Dir, filepath.FromSlash(clean))
	relChecked, err := filepath.Rel(mf.Dir, full)
	if err != nil || strings.HasPrefix(relChecked, "..") {
		return "", fmt.Errorf("路径越界：%q", rel)
	}
	return full, nil
}

func (a *API) nodeGetPluginDir(pluginID string, _ map[string]any) (any, error) {
	mf, err := a.mgr.Manifest(pluginID)
	if err != nil {
		return nil, err
	}
	return mf.Dir, nil
}

func (a *API) nodeReadFile(pluginID string, args map[string]any) (any, error) {
	full, err := safePluginPath(a.mgr, pluginID, argString(args, "path"))
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (a *API) nodeWriteFile(pluginID string, args map[string]any) (any, error) {
	full, err := safePluginPath(a.mgr, pluginID, argString(args, "path"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	return nil, os.WriteFile(full, []byte(argString(args, "content")), 0o644)
}

func nilNode(name string) handlerFunc {
	return notImplemented(name)
}
