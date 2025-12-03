package fetcher

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
	MaxBodySize = 500 * 1024 // 500KB limit (increased for content extraction)
	Timeout     = 10 * time.Second
	UserAgent   = "MastodonBot/1.0 (+https://github.com/shinderuman/claude_bot)"
)

type Metadata struct {
	URL         string
	Title       string
	Description string
	ImageURL    string
	SiteName    string
	Content     string // Extracted text content from body
}

// FetchMetadata retrieves metadata (Title, Description, OGP) and text content from the given URL.
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

	// Extract text content from body
	meta.Content = extractTextContent(doc)

	// タイトルなどのクリーニング
	meta.Title = strings.TrimSpace(meta.Title)
	meta.Description = strings.TrimSpace(meta.Description)
	meta.Content = strings.TrimSpace(meta.Content)

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

// extractTextContent extracts visible text from the HTML body
func extractTextContent(n *html.Node) string {
	var sb strings.Builder
	var f func(*html.Node)

	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Skip script, style, and other non-content tags
			switch n.Data {
			case "script", "style", "noscript", "iframe", "svg", "header", "footer", "nav":
				return
			}
		}

		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}

	// Find body tag to start extraction
	var body *html.Node
	var findBody func(*html.Node)
	findBody = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBody(c)
			if body != nil {
				return
			}
		}
	}
	findBody(n)

	if body != nil {
		f(body)
	} else {
		// If no body tag found, try extracting from root (fallback)
		f(n)
	}

	// Limit content length to avoid token limit issues
	content := sb.String()
	if len(content) > 2000 {
		return content[:2000] + "..."
	}
	return content
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
	if meta.Content != "" {
		sb.WriteString(fmt.Sprintf("本文抜粋: %s\n", meta.Content))
	}
	return sb.String()
}
