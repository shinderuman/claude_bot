package fetcher

import (
	"net/url"
	"strings"
)

// IsNoiseURL はハッシュタグURLやユーザープロフィールURLなどのノイズURLかを判定します
func IsNoiseURL(urlStr string) bool {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return true // パースできないURLはノイズとして扱う
	}

	path := strings.ToLower(parsedURL.Path)

	// ハッシュタグURL
	if strings.Contains(path, "/tags/") {
		return true
	}

	// ユーザープロフィールURL (/@username の形式)
	// ただし、特定の投稿URL (/@username/123456 の形式) は除外しない
	if strings.Contains(path, "/@") {
		// /@以降にスラッシュがなければプロフィールURL
		atIndex := strings.Index(path, "/@")
		if atIndex != -1 {
			afterAt := path[atIndex+2:]
			if !strings.Contains(afterAt, "/") {
				return true
			}
		}
	}

	// サーバーのトップページ (パスが空または/)
	if path == "" || path == "/" {
		return true
	}

	return false
}
