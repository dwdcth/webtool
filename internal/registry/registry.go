// Package registry 维护已安装插件注册表（plugins.json）。
package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record 是单个插件的安装记录。
type Record struct {
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	Enabled     bool      `json:"enabled"`
	InstalledAt time.Time `json:"installedAt"`
	Source      string    `json:"source"` // file | url | capture | builtin
	SourceURL   string    `json:"sourceUrl,omitempty"`
}

type Registry struct {
	mu    sync.Mutex
	path  string
	items map[string]*Record
}

func Load(path string) *Registry {
	r := &Registry{path: path, items: map[string]*Record{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return r
	}
	var items map[string]*Record
	if json.Unmarshal(b, &items) == nil && items != nil {
		r.items = items
	}
	return r
}

func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

func (r *Registry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r.items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o644)
}

func (r *Registry) Get(id string) (Record, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.items[id]
	if !ok {
		return Record{}, false
	}
	return *rec, true
}

func (r *Registry) Upsert(rec Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[rec.ID] = &rec
	return r.saveLocked()
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)
	return r.saveLocked()
}

func (r *Registry) List() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, 0, len(r.items))
	for _, rec := range r.items {
		out = append(out, *rec)
	}
	return out
}
