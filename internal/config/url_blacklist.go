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

// URLBlacklist manages a dynamically reloadable URL blacklist
type URLBlacklist struct {
	mu       sync.RWMutex
	domains  []string
	filePath string
	watcher  *fsnotify.Watcher
}

// NewURLBlacklist creates a new URLBlacklist from a file
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

// Get returns a copy of the current blacklist
func (b *URLBlacklist) Get() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]string, len(b.domains))
	copy(result, b.domains)
	return result
}

// reload reads the blacklist file and updates the domains
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

		// Skip empty lines and comments
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

// StartWatching starts watching the blacklist file for changes
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

// watchLoop watches for file changes and reloads the blacklist
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

// handleFileEvent processes filesystem events for the blacklist file
func (b *URLBlacklist) handleFileEvent(ctx context.Context, event fsnotify.Event) {
	// Reload on write, create, or rename events
	shouldReload := event.Op&fsnotify.Write == fsnotify.Write ||
		event.Op&fsnotify.Create == fsnotify.Create

	if shouldReload {
		if err := b.reload(); err != nil {
			log.Printf("URL Blacklist再読み込みエラー: %v", err)
		}
		return
	}

	// If file was renamed or removed, re-add watch
	shouldRewatch := event.Op&fsnotify.Rename == fsnotify.Rename ||
		event.Op&fsnotify.Remove == fsnotify.Remove

	if shouldRewatch {
		go b.attemptRewatch(ctx)
	}
}

// attemptRewatch tries to re-establish the file watcher after a file move/delete
func (b *URLBlacklist) attemptRewatch(ctx context.Context) {
	// Wait a bit for the new file to be created
	// (editors often remove and recreate files)
	for range 5 {
		if b.tryAddWatcher() {
			// 監視再開成功時、一度読み込んでおく
			if err := b.reload(); err != nil {
				log.Printf("URL Blacklist再読み込みエラー: %v", err)
			}
			return
		}

		// Wait 100ms before retry
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// tryAddWatcher attempts to valid file existence and add it to the watcher
func (b *URLBlacklist) tryAddWatcher() bool {
	if _, err := os.Stat(b.filePath); err != nil {
		return false
	}
	return b.watcher.Add(b.filePath) == nil
}

// LoadFromEnv loads blacklist from environment variable (fallback)
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

// InitializeURLBlacklist initializes the URL blacklist from file or env
func InitializeURLBlacklist(ctx context.Context, envValue string) *URLBlacklist {
	blacklistPath := util.GetFilePath("url_blacklist.txt")

	// Check if file exists
	if _, err := os.Stat(blacklistPath); err == nil {
		// File exists, use it
		blacklist := NewURLBlacklist(blacklistPath)
		if err := blacklist.StartWatching(ctx); err != nil {
			log.Printf("URL Blacklistファイル監視開始エラー: %v", err)
		}
		return blacklist
	}

	// File doesn't exist, use env variable (legacy support)
	log.Printf("url_blacklist.txtが見つかりません。環境変数URL_BLACKLISTを使用します")
	domains := LoadBlacklistFromEnv(envValue)

	blacklist := &URLBlacklist{
		filePath: blacklistPath,
		domains:  domains,
	}

	log.Printf("URL Blacklist読み込み完了: %d件 (環境変数)", len(domains))
	return blacklist
}
