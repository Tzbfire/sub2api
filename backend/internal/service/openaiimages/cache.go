package openaiimages

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ImageCache 把生成的图片字节落盘到本地，签发可在 TTL 内通过 HTTP 访问的短链。
//
// 设计目标：
//   - 用户请求 response_format=url 时，对 WebDriver / ResponsesToolDriver 路径
//     （上游不直接返回可用 url）也能返回 url 形式；
//   - 重启后仍可访问（落盘），TTL 默认 24h；ttl < 0 表示永久保留；
//   - TTL 模式后台 goroutine 定期 GC 过期文件；永久保留模式不做过期删除；
//   - 内存索引用于 O(1) 查找 mime/exp，不存字节。
type ImageCache struct {
	dir string
	ttl time.Duration

	mu      sync.RWMutex
	entries map[string]*cacheEntry

	stopOnce sync.Once
	stopCh   chan struct{}
}

type cacheEntry struct {
	mime    string
	expires time.Time
}

// NewImageCache 创建并启动一个 cache。dir 为空使用 ./data/image_cache。
// ttl==0 使用 24h；ttl<0 表示永久保留。
func NewImageCache(dir string, ttl time.Duration) (*ImageCache, error) {
	if dir == "" {
		dir = filepath.Join(".", "data", "image_cache")
	}
	ttl = normalizeCacheTTL(ttl)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create image cache dir: %w", err)
	}
	c := &ImageCache{
		dir:     dir,
		ttl:     ttl,
		entries: make(map[string]*cacheEntry),
		stopCh:  make(chan struct{}),
	}
	c.scanExisting()
	go c.gcLoop()
	return c, nil
}

// Close 停止 GC 循环（测试用）。
func (c *ImageCache) Close() {
	c.stopOnce.Do(func() { close(c.stopCh) })
}

// SetTTL 更新缓存保留策略，并把当前内存索引同步到新的策略。
// ttl==0 使用默认 24h；ttl<0 表示永久保留。
func (c *ImageCache) SetTTL(ttl time.Duration) {
	if c == nil {
		return
	}
	ttl = normalizeCacheTTL(ttl)
	c.mu.RLock()
	current := c.ttl
	c.mu.RUnlock()
	if current == ttl {
		return
	}
	now := time.Now()
	type victim struct{ id, mime string }
	var victims []victim
	c.mu.Lock()
	c.ttl = ttl
	for id, e := range c.entries {
		if ttl < 0 {
			e.expires = time.Time{}
			continue
		}
		info, err := os.Stat(c.fileFor(id, e.mime))
		if err != nil {
			victims = append(victims, victim{id, e.mime})
			continue
		}
		exp := info.ModTime().Add(ttl)
		if now.After(exp) {
			victims = append(victims, victim{id, e.mime})
			continue
		}
		e.expires = exp
	}
	for _, v := range victims {
		delete(c.entries, v.id)
	}
	c.mu.Unlock()
	for _, v := range victims {
		_ = os.Remove(c.fileFor(v.id, v.mime))
	}
}

// Put 写入字节并返回随机 id。mime 用于决定扩展名与读取时回放。
func (c *ImageCache) Put(data []byte, mime string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty image data")
	}
	if mime == "" {
		mime = "image/png"
	}
	id, err := newCacheID()
	if err != nil {
		return "", err
	}
	path := c.fileFor(id, mime)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write cache file: %w", err)
	}
	c.mu.Lock()
	ttl := c.ttl
	expires := time.Time{}
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	c.entries[id] = &cacheEntry{mime: mime, expires: expires}
	c.mu.Unlock()
	return id, nil
}

// Get 读取字节与 mime；过期或不存在返回 ok=false。
//
// 注意：会一次性把整个文件读进内存。HTTP 直接响应路径请用 OpenForServe，
// 它走 io.Copy 流式响应，单文件几乎不增长堆。
func (c *ImageCache) Get(id string) ([]byte, string, bool) {
	c.mu.RLock()
	e, ok := c.entries[id]
	var mime string
	var expires time.Time
	if ok {
		mime = e.mime
		expires = e.expires
	}
	c.mu.RUnlock()
	if !ok {
		return nil, "", false
	}
	if !expires.IsZero() && time.Now().After(expires) {
		c.deleteEntry(id, mime)
		return nil, "", false
	}
	data, err := os.ReadFile(c.fileFor(id, mime))
	if err != nil {
		return nil, "", false
	}
	return data, mime, true
}

// OpenForServe 打开 cache 文件用于流式响应（http.ServeContent）。
// 调用方必须 Close() 返回的 *os.File。过期或不存在返回 ok=false。
func (c *ImageCache) OpenForServe(id string) (f *os.File, mime string, modTime time.Time, ok bool) {
	c.mu.RLock()
	e, exists := c.entries[id]
	var expires time.Time
	if exists {
		mime = e.mime
		expires = e.expires
	}
	c.mu.RUnlock()
	if !exists {
		return nil, "", time.Time{}, false
	}
	if !expires.IsZero() && time.Now().After(expires) {
		c.deleteEntry(id, mime)
		return nil, "", time.Time{}, false
	}
	path := c.fileFor(id, mime)
	file, err := os.Open(path)
	if err != nil {
		return nil, "", time.Time{}, false
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, "", time.Time{}, false
	}
	return file, mime, stat.ModTime(), true
}

func (c *ImageCache) fileFor(id, mime string) string {
	return filepath.Join(c.dir, id+extForMime(mime))
}

func (c *ImageCache) deleteEntry(id, mime string) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
	_ = os.Remove(c.fileFor(id, mime))
}

func (c *ImageCache) gcLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-t.C:
			c.gcOnce()
		}
	}
}

func (c *ImageCache) gcOnce() {
	now := time.Now()
	type victim struct{ id, mime string }
	var victims []victim
	c.mu.RLock()
	for id, e := range c.entries {
		if !e.expires.IsZero() && now.After(e.expires) {
			victims = append(victims, victim{id, e.mime})
		}
	}
	c.mu.RUnlock()
	for _, v := range victims {
		c.deleteEntry(v.id, v.mime)
	}
}

// scanExisting 启动时把已落盘的文件挂回索引（按文件 mtime + ttl 推断过期）。
func (c *ImageCache) scanExisting() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		ext := filepath.Ext(name)
		id := strings.TrimSuffix(name, ext)
		if len(id) < 16 {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		exp := time.Time{}
		if c.ttl > 0 {
			exp = info.ModTime().Add(c.ttl)
			if now.After(exp) {
				_ = os.Remove(filepath.Join(c.dir, name))
				continue
			}
		}
		c.entries[id] = &cacheEntry{mime: mimeForExt(ext), expires: exp}
	}
}

func newCacheID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func extForMime(mime string) string {
	switch strings.ToLower(mime) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

func normalizeCacheTTL(ttl time.Duration) time.Duration {
	if ttl == 0 {
		return 24 * time.Hour
	}
	return ttl
}
