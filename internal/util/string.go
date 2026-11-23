package util

import "strings"

func ExtractIDFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		for _, r := range lastPart {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return lastPart
	}
	return ""
}

func CleanURL(url string) string {
	for i, r := range url {
		if r > 127 {
			return url[:i]
		}
	}
	return url
}
