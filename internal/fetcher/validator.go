package fetcher

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

func IsValidURL(urlStr string, blacklist []string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return errors.New("無効なURL形式です")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("許可されていないスキームです (http/httpsのみ許可)")
	}

	host := u.Hostname()
	if host == "" {
		return errors.New("ホスト名がありません")
	}

	if net.ParseIP(host) != nil {
		return errors.New("IPアドレスへの直接アクセスは許可されていません")
	}

	if isBlacklisted(host, blacklist) {
		return errors.New("アクセスが禁止されているドメインです")
	}

	return nil
}

func IsValidURLBasic(urlStr string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return errors.New("無効なURL形式です")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("許可されていないスキームです (http/httpsのみ許可)")
	}

	host := u.Hostname()
	if host == "" {
		return errors.New("ホスト名がありません")
	}

	if net.ParseIP(host) != nil {
		return errors.New("IPアドレスへの直接アクセスは許可されていません")
	}

	return nil
}

func isBlacklisted(host string, blacklist []string) bool {
	host = strings.ToLower(host)
	for _, pattern := range blacklist {
		pattern = strings.ToLower(pattern)

		if host == pattern {
			return true
		}

		matched, err := filepath.Match(pattern, host)
		if err == nil && matched {
			return true
		}
	}
	return false
}
