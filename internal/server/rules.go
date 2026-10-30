package server

import (
	"encoding/json"
	"os"
	"path/filepath"

	"webtool/internal/intercept"
)

// DefaultGlobalRules 是全局规则的初始内容：迁移自旧版 webtool 里
// 硬编码的 ctool 拦截（chrome_tool 的公告/统计请求返回空成功 JSON，
// armconverter 等功能性路径不受影响）。用户可在 <dataDir>/rules.json
// 中自由修改，应用启动时加载、改动重启生效（P2 提供编辑 UI 与热更新）。
func DefaultGlobalRules() []intercept.Rule {
	raw := []intercept.Rule{{
		Name:  "legacy-ctool-notice-stat-mock",
		Match: intercept.Match{PathRegex: `^/?chrome_tool/(notice|stat)([/?]|$)`},
		Action: intercept.Action{
			Type:        intercept.ActionRespond,
			Status:      200,
			Body:        `{"code":0,"data":[],"info":"操作成功"}`,
			ContentType: "application/json",
		},
	}}
	out, err := intercept.Compile(raw)
	if err != nil {
		panic("默认拦截规则不合法: " + err.Error())
	}
	return out
}

// LoadOrCreateGlobalRules 读取规则文件；不存在则写入默认规则并返回。
func LoadOrCreateGlobalRules(path string) []intercept.Rule {
	if b, err := os.ReadFile(path); err == nil {
		var raw []intercept.Rule
		if json.Unmarshal(b, &raw) == nil {
			if rules, err := intercept.Compile(raw); err == nil {
				return rules
			}
		}
		// 文件损坏：忽略并回落到默认（不覆盖用户文件）
		return DefaultGlobalRules()
	}
	rules := DefaultGlobalRules()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if b, err := json.MarshalIndent(rules, "", "  "); err == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
	}
	return rules
}

// SaveGlobalRules 写规则文件（不做校验，校验在调用方）。
func SaveGlobalRules(path string, rules []intercept.Rule) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// SetGlobalRules 热更新全局规则。
func (s *AssetService) SetGlobalRules(rules []intercept.Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalRules = rules
}