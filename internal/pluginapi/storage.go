package pluginapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"webtool/internal/plugin"
)

// store 是单插件的持久化 KV（JSON 文件，plugin-data/<id>.json）。
// 语义对齐 unihub 的 LMDB 插件存储：get/set/delete/keys/clear。
type store struct {
	mu   sync.Mutex
	path string
	data map[string]any
}

func storePath(mgr *plugin.Manager, pluginID string) string {
	// pluginsDir 的父目录即数据根（见 paths 布局）
	return filepath.Join(filepath.Dir(mgr.PluginsDir()), "plugin-data", pluginID+".json")
}

func newStore(mgr *plugin.Manager, pluginID string) *store {
	s := &store{path: storePath(mgr, pluginID), data: map[string]any{}}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded map[string]any
		if json.Unmarshal(b, &loaded) == nil && loaded != nil {
			s.data = loaded
		}
	}
	return s
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

func (s *store) Get(key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key]
}

func (s *store) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return s.saveLocked()
}

func (s *store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return s.saveLocked()
}

func (s *store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = map[string]any{}
	return s.saveLocked()
}
