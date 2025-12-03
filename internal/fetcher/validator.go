package fetcher

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// IsValidURL checks if the URL is safe to fetch.
// It validates the scheme, ensures the host is not an IP address,
// and checks against the provided blacklist.
func IsValidURL(urlStr string, blacklist []string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return errors.New("無効なURL形式です")
	}

	// スキームチェック (HTTP/HTTPSのみ許可)
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("許可されていないスキームです (http/httpsのみ許可)")
	}

	host := u.Hostname()
	if host == "" {
		return errors.New("ホスト名がありません")
	}

	// IPアドレス直接指定を拒否
	if net.ParseIP(host) != nil {
		return errors.New("IPアドレスへの直接アクセスは許可されていません")
	}

	// ブラックリストチェック
	if isBlacklisted(host, blacklist) {
		return errors.New("アクセスが禁止されているドメインです")
	}

	return nil
}

// IsValidURLBasic checks if the URL is safe to fetch (for trusted sources like mentions).
// It validates the scheme and ensures the host is not an IP address,
// but does NOT check against blacklist (trusted user input).
func IsValidURLBasic(urlStr string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return errors.New("無効なURL形式です")
	}

	// スキームチェック (HTTP/HTTPSのみ許可)
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("許可されていないスキームです (http/httpsのみ許可)")
	}

	host := u.Hostname()
	if host == "" {
		return errors.New("ホスト名がありません")
	}

	// IPアドレス直接指定を拒否
	if net.ParseIP(host) != nil {
		return errors.New("IPアドレスへの直接アクセスは許可されていません")
	}

	return nil
}

// isBlacklisted checks if the host matches any pattern in the blacklist.
// Supports wildcard matching (e.g., "*.example.com").
func isBlacklisted(host string, blacklist []string) bool {
	host = strings.ToLower(host)
	for _, pattern := range blacklist {
		pattern = strings.ToLower(pattern)

		// 単純一致
		if host == pattern {
			return true
		}

		// ワイルドカードマッチ
		matched, err := filepath.Match(pattern, host)
		if err == nil && matched {
			return true
		}
	}
	return false
}
