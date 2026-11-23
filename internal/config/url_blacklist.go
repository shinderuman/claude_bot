package config

import (
	"bufio"
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/util"

	"github.com/fsnotify/fsnotify"
)

type URLBlacklist struct {
	mu       sync.RWMutex
	domains  []string
	filePath string
	watcher  *fsnotify.Watcher
}

func NewURLBlacklist(filePath string) *URLBlacklist {
	b := &URLBlacklist{
		filePath: filePath,
		domains:  []string{},
	}

	if err := b.reload(); err != nil {
		log.Printf("URL Blacklist初期読み込みエラー（空のリストで起動します）: %v", err)
	}

	return b
}

func (b *URLBlacklist) Get() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]string, len(b.domains))
	copy(result, b.domains)
	return result
}

func (b *URLBlacklist) reload() error {
	file, err := os.Open(b.filePath)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck

	var domains []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		domains = append(domains, line)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	b.domains = domains
	b.mu.Unlock()

	log.Printf("URL Blacklist再読み込み完了: %d件 (ファイル: %s)", len(domains), b.filePath)
	return nil
}

func (b *URLBlacklist) StartWatching(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	b.watcher = watcher

	if err := watcher.Add(b.filePath); err != nil {
		watcher.Close() //nolint:errcheck
		return err
	}

	go b.watchLoop(ctx)
	log.Printf("URL Blacklistファイル監視開始: %s", b.filePath)

	return nil
}

func (b *URLBlacklist) watchLoop(ctx context.Context) {
	defer b.watcher.Close() //nolint:errcheck

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-b.watcher.Events:
			if !ok {
				return
			}
			b.handleFileEvent(ctx, event)
		case err, ok := <-b.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("URL Blacklistファイル監視エラー: %v", err)
		}
	}
}

func (b *URLBlacklist) handleFileEvent(ctx context.Context, event fsnotify.Event) {
	shouldReload := event.Op&fsnotify.Write == fsnotify.Write ||
		event.Op&fsnotify.Create == fsnotify.Create

	if shouldReload {
		if err := b.reload(); err != nil {
			log.Printf("URL Blacklist再読み込みエラー: %v", err)
		}
		return
	}

	shouldRewatch := event.Op&fsnotify.Rename == fsnotify.Rename ||
		event.Op&fsnotify.Remove == fsnotify.Remove

	if shouldRewatch {
		go b.attemptRewatch(ctx)
	}
}

func (b *URLBlacklist) attemptRewatch(ctx context.Context) {
	// Editors can replace the file by renaming a temporary file over it.
	for range 5 {
		if b.tryAddWatcher() {
			if err := b.reload(); err != nil {
				log.Printf("URL Blacklist再読み込みエラー: %v", err)
			}
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (b *URLBlacklist) tryAddWatcher() bool {
	if _, err := os.Stat(b.filePath); err != nil {
		return false
	}
	return b.watcher.Add(b.filePath) == nil
}

func LoadBlacklistFromEnv(envValue string) []string {
	if envValue == "" {
		return []string{}
	}

	var domains []string
	for _, domain := range strings.Split(envValue, ",") {
		domain = strings.TrimSpace(domain)
		if domain != "" {
			domains = append(domains, domain)
		}
	}

	return domains
}

func InitializeURLBlacklist(ctx context.Context, envValue string) *URLBlacklist {
	blacklistPath := util.GetFilePath("url_blacklist.txt")

	if _, err := os.Stat(blacklistPath); err == nil {
		blacklist := NewURLBlacklist(blacklistPath)
		if err := blacklist.StartWatching(ctx); err != nil {
			log.Printf("URL Blacklistファイル監視開始エラー: %v", err)
		}
		return blacklist
	}

	log.Printf("url_blacklist.txtが見つかりません。環境変数URL_BLACKLISTを使用します")
	domains := LoadBlacklistFromEnv(envValue)

	blacklist := &URLBlacklist{
		filePath: blacklistPath,
		domains:  domains,
	}

	log.Printf("URL Blacklist読み込み完了: %d件 (環境変数)", len(domains))
	return blacklist
}
