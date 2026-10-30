// Package router 维护"会话 token ↔ 插件 id"映射。
// 插件沙箱 iframe 的所有资源与 API 调用都走 /p/<token>/...，
// 身份由不可猜的 token 承载，插件无法冒充其他插件。
package router

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

type Router struct {
	mu       sync.Mutex
	byPlugin map[string]string
	byToken  map[string]string
}

func New() *Router {
	return &Router{
		byPlugin: map[string]string{},
		byToken:  map[string]string{},
	}
}

// TokenFor 返回插件本会话的稳定 token（同插件复用，多标签共享）。
func (r *Router) TokenFor(pluginID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.byPlugin[pluginID]; ok {
		return t
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand 不可用: " + err.Error())
	}
	t := hex.EncodeToString(b)
	r.byPlugin[pluginID] = t
	r.byToken[t] = pluginID
	return t
}

// PluginOf 由 token 反查插件 id。
func (r *Router) PluginOf(token string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byToken[token]
	return id, ok
}

// Revoke 撤销插件 token（卸载时调用）。
func (r *Router) Revoke(pluginID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.byPlugin[pluginID]; ok {
		delete(r.byToken, t)
		delete(r.byPlugin, pluginID)
	}
}
