package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	MaxBodySize = 100 * 1024 // 100KB limit for metadata fetching
	Timeout     = 5 * time.Second
	UserAgent   = "MastodonBot/1.0 (+https://github.com/shinderuman/claude_bot)"
)

type Metadata struct {
	URL         string
	Title       string
	Description string
	ImageURL    string
	SiteName    string
}

// FetchMetadata retrieves metadata (Title, Description, OGP) from the given URL.
// It enforces strict security measures: timeout, size limit, and content type check.
func FetchMetadata(ctx context.Context, urlStr string) (*Metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエスト作成エラー: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("通信エラー: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPエラー: %d", resp.StatusCode)
	}

	// Content-Type check
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		return nil, errors.New("HTMLコンテンツではありません")
	}

	// Limit reader to prevent reading large files
	limitedReader := io.LimitReader(resp.Body, MaxBodySize)

	return extractMetadata(limitedReader, urlStr)
}

func extractMetadata(r io.Reader, urlStr string) (*Metadata, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("HTMLパースエラー: %w", err)
	}

	meta := &Metadata{URL: urlStr}
	extractMetaTags(doc, meta)

	// タイトルなどのクリーニング
	meta.Title = strings.TrimSpace(meta.Title)
	meta.Description = strings.TrimSpace(meta.Description)

	return meta, nil
}

func extractMetaTags(n *html.Node, meta *Metadata) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "title":
			if n.FirstChild != nil {
				meta.Title = n.FirstChild.Data
			}
		case "meta":
			property := getAttr(n, "property")
			name := getAttr(n, "name")
			content := getAttr(n, "content")

			// OGP tags
			switch property {
			case "og:title":
				meta.Title = content
			case "og:description":
				meta.Description = content
			case "og:image":
				meta.ImageURL = content
			case "og:site_name":
				meta.SiteName = content
			}

			// Standard meta tags
			switch name {
			case "description":
				if meta.Description == "" {
					meta.Description = content
				}
			}
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractMetaTags(c, meta)
	}
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func FormatMetadata(meta *Metadata) string {
	var sb strings.Builder
	sb.WriteString("\n\n[参照URL情報]\n")
	sb.WriteString(fmt.Sprintf("URL: %s\n", meta.URL))
	if meta.Title != "" {
		sb.WriteString(fmt.Sprintf("タイトル: %s\n", meta.Title))
	}
	if meta.Description != "" {
		sb.WriteString(fmt.Sprintf("説明: %s\n", meta.Description))
	}
	return sb.String()
}
