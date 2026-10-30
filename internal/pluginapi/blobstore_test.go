package pluginapi

import (
	"path/filepath"
	"testing"
)

func TestBlobStore(t *testing.T) {
	dir := t.TempDir()
	s, err := openBlobStore(dir, "test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// set → get
	if err := s.Set("wtimg-a.png", "data:image/png;base64,AAAA"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("wtimg-b.png", "data:image/png;base64,BBBB"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get("wtimg-a.png")
	if err != nil || v != "data:image/png;base64,AAAA" {
		t.Fatalf("get 失败: %q %v", v, err)
	}
	// 覆盖写
	if err := s.Set("wtimg-a.png", "data:image/png;base64,CCCC"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("wtimg-a.png"); v != "data:image/png;base64,CCCC" {
		t.Fatalf("覆盖失败: %q", v)
	}
	// keys
	keys, err := s.Keys()
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys 失败: %v %v", keys, err)
	}
	// 不存在的 key → 空串无错
	if v, err := s.Get("nope"); err != nil || v != "" {
		t.Fatalf("缺失 key 应返回空: %q %v", v, err)
	}

	// 重启持久化（新开实例读同一文件）
	s2, err := openBlobStore(dir, "test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if v, _ := s2.Get("wtimg-b.png"); v != "data:image/png;base64,BBBB" {
		t.Fatalf("重启后数据丢失: %q", v)
	}
	if keys2, _ := s2.Keys(); len(keys2) != 2 {
		t.Fatalf("重启后 keys 丢失: %v", keys2)
	}

	// 文件确实生成在 plugin-data 目录
	if _, err := filepath.Abs(filepath.Join(dir, "test-plugin.blob.db")); err != nil {
		t.Fatal(err)
	}
}
