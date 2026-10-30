// Package pluginapi 的二进制对象存储：每插件一个 SQLite 文件
// （plugin-data/<id>.blob.db，modernc.org/sqlite 纯 Go 驱动，随 Wails3 依赖树自带）。
//
// 背景：网页插件（heynote 等）的图片若存 localStorage 有 5-10MB 配额限制；
// 桌面版 heynote 则是"一文件一缓冲区"。SQLite 单文件容器兼顾两者顾虑：
// 无文件爆炸、磁盘级容量。
package pluginapi

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // 纯 Go 驱动，无 CGO
)

type blobStore struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
}

func openBlobStore(dataDir, pluginID string) (*blobStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, pluginID+".blob.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS blobs (
		key  TEXT PRIMARY KEY,
		data TEXT NOT NULL,
		created_at INTEGER DEFAULT (unixepoch())
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return &blobStore{db: db, path: path}, nil
}

func (s *blobStore) Set(key, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO blobs(key, data) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET data=excluded.data`, key, data)
	return err
}

func (s *blobStore) Get(key string) (string, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM blobs WHERE key = ?`, key).Scan(&data)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return data, err
}

func (s *blobStore) Keys() ([]string, error) {
	rows, err := s.db.Query(`SELECT key FROM blobs ORDER BY created_at, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *blobStore) Close() error { return s.db.Close() }
