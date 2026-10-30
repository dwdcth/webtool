// Package marketplace 拉取与解析插件市场目录。
// 默认源为 unihub 官方（GitHub 仓库静态 JSON，经 jsDelivr CDN），
// 插件 zip 托管在其腾讯云 COS——对客户端只是"读 JSON + 下载 zip"。
package marketplace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultSource 是默认市场源（unihub 官方）。
const DefaultSource = "https://cdn.jsdelivr.net/gh/t8y2/unihub@main/marketplace/plugins.json"

// Entry 是市场条目（前端展示与安装所需的最小字段集）。
type Entry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author,omitempty"`
	Icon        string   `json:"icon,omitempty"` // emoji 或 URL
	Category    string   `json:"category,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Downloads   int64    `json:"downloads,omitempty"`
	ZipURL      string   `json:"zipUrl"` // 安装地址（install.zip）
}

// raw 是 unihub 市场 JSON 的原始结构。
type raw struct {
	Version   string `json:"version"`
	UpdatedAt string `json:"lastUpdated"`
	Plugins   []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Author      any    `json:"author"`
		Icon        string `json:"icon"`
		Category    string `json:"category"`
		Keywords    []string `json:"keywords"`
		Permissions []string `json:"permissions"`
		Homepage    string `json:"homepage"`
		Downloads   int64  `json:"downloads"`
		Install     struct {
			Zip string `json:"zip"`
			Npm string `json:"npm"`
		} `json:"install"`
	} `json:"plugins"`
}

var client = &http.Client{Timeout: 20 * time.Second}

// Fetch 拉取并解析市场目录。
func Fetch(sourceURL string) ([]Entry, error) {
	if sourceURL == "" {
		sourceURL = DefaultSource
	}
	resp, err := client.Get(sourceURL) //nolint:noctx // 一次性拉取，自带超时
	if err != nil {
		return nil, fmt.Errorf("市场源请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("市场源响应异常: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var cat raw
	if err := json.Unmarshal(b, &cat); err != nil {
		return nil, fmt.Errorf("市场 JSON 解析失败: %w", err)
	}
	entries := make([]Entry, 0, len(cat.Plugins))
	for _, p := range cat.Plugins {
		if p.ID == "" || p.Install.Zip == "" {
			continue // 无安装地址的条目不展示
		}
		entries = append(entries, Entry{
			ID:          p.ID,
			Name:        p.Name,
			Version:     p.Version,
			Description: p.Description,
			Author:      authorString(p.Author),
			Icon:        p.Icon,
			Category:    p.Category,
			Keywords:    p.Keywords,
			Permissions: p.Permissions,
			Homepage:    p.Homepage,
			Downloads:   p.Downloads,
			ZipURL:      p.Install.Zip,
		})
	}
	return entries, nil
}

// authorString 兼容 "string" 与 {name: ...} 两种写法。
func authorString(a any) string {
	switch v := a.(type) {
	case string:
		return v
	case map[string]any:
		if n, ok := v["name"].(string); ok {
			return n
		}
	}
	return ""
}
