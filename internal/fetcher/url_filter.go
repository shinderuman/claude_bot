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

	// ユーザープロフィールURL
	// 1. /@username 形式
	if strings.Contains(path, "/@") {
		// /@以降を取得
		atIndex := strings.Index(path, "/@")
		if atIndex != -1 {
			afterAt := path[atIndex+2:]
			// 末尾のスラッシュを削除して正規化
			afterAt = strings.TrimSuffix(afterAt, "/")

			// スラッシュが含まれていなければプロフィールURL
			// (例: /@user, /@user/)
			// スラッシュが含まれていれば投稿URLなど
			// (例: /@user/123456)
			if !strings.Contains(afterAt, "/") {
				return true
			}
		}
	}

	// 2. /users/username 形式
	if strings.Contains(path, "/users/") {
		usersIndex := strings.Index(path, "/users/")
		if usersIndex != -1 {
			afterUsers := path[usersIndex+7:]
			// 末尾のスラッシュを削除して正規化
			afterUsers = strings.TrimSuffix(afterUsers, "/")

			// スラッシュが含まれていなければプロフィールURL
			if !strings.Contains(afterUsers, "/") {
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
