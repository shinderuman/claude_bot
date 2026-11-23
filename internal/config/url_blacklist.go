package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"claude_bot/internal/util"

	"github.com/fsnotify/fsnotify"
)

type URLBlacklist struct {
	mu       sync.RWMutex
	domains  []string
	filePath string
}

func NewURLBlacklist(filePath string) (*URLBlacklist, error) {
	b := &URLBlacklist{
		filePath: filePath,
		domains:  []string{},
	}

	if err := b.reload(); err != nil {
		return nil, err
	}

	return b, nil
}

func (b *URLBlacklist) Get() []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return slices.Clone(b.domains)
}

func (b *URLBlacklist) reload() error {
	file, err := os.Open(b.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

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

	log.Printf("URLブラックリスト読込: %d件、%s", len(domains), b.filePath)
	return nil
}

func (b *URLBlacklist) StartWatching(ctx context.Context) error {
	path, err := filepath.Abs(b.filePath)
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(filepath.Dir(path)); err != nil {
		watcher.Close()
		return err
	}

	go b.watchLoop(ctx, watcher, path)
	log.Printf("URLブラックリスト監視開始: %s", path)
	return nil
}

func (b *URLBlacklist) watchLoop(ctx context.Context, watcher *fsnotify.Watcher, path string) {
	defer watcher.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) == path && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
				if err := b.reload(); err != nil {
					log.Printf("URLブラックリスト再読込失敗: %v", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("URLブラックリスト監視エラー: %v", err)
		}
	}
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

func InitializeURLBlacklist(ctx context.Context, envValue string) (*URLBlacklist, error) {
	blacklistPath, err := util.GetFilePath("url_blacklist.txt")
	if err == nil {
		blacklist, err := NewURLBlacklist(blacklistPath)
		if err != nil {
			return nil, fmt.Errorf("URL Blacklistを読み込めません: %w", err)
		}
		if err := blacklist.StartWatching(ctx); err != nil {
			return nil, fmt.Errorf("URL Blacklistの監視を開始できません: %w", err)
		}
		return blacklist, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	log.Printf("url_blacklist.txtなし: 環境変数URL_BLACKLISTを使用")
	domains := LoadBlacklistFromEnv(envValue)

	blacklist := &URLBlacklist{
		filePath: blacklistPath,
		domains:  domains,
	}

	log.Printf("URLブラックリスト読込: 環境変数から%d件", len(domains))
	return blacklist, nil
}
