package fetcher

import (
	"net/url"
	"strings"
)

func IsNoiseURL(urlStr string) bool {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return true
	}

	path := strings.ToLower(parsedURL.Path)

	if strings.Contains(path, "/tags/") {
		return true
	}

	if strings.Contains(path, "/@") {
		atIndex := strings.Index(path, "/@")
		if atIndex != -1 {
			afterAt := path[atIndex+2:]
			afterAt = strings.TrimSuffix(afterAt, "/")

			if !strings.Contains(afterAt, "/") {
				return true
			}
		}
	}

	if strings.Contains(path, "/users/") {
		usersIndex := strings.Index(path, "/users/")
		if usersIndex != -1 {
			afterUsers := path[usersIndex+7:]
			afterUsers = strings.TrimSuffix(afterUsers, "/")

			if !strings.Contains(afterUsers, "/") {
				return true
			}
		}
	}

	if path == "" || path == "/" {
		return true
	}

	return false
}
