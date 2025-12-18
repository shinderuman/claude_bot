package util

import "strings"

// ExtractIDFromURL extracts the last numeric segment from a URL
func ExtractIDFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		// 数字のみかチェック
		for _, r := range lastPart {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return lastPart
	}
	return ""
}

// CleanURL removes non-ASCII characters from the end of the URL
func CleanURL(url string) string {
	for i, r := range url {
		if r > 127 {
			return url[:i]
		}
	}
	return url
}
