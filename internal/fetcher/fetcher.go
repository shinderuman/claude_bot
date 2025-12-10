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
	// User-Agent to mimic a browser to avoid 403s on some sites
	UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 (+https://github.com/shinderuman/claude_bot)"

	// HTTP
	MaxRedirects            = 10
	MaxContentLength        = 2000
	ContentTruncationSuffix = "..."

	// Content-Type
	ContentTypeHTML = "text/html"
)

type PageContent struct {
	URL         string
	Title       string
	Description string
	ImageURL    string
	SiteName    string
	Content     string // Extracted text content from body
}

// FetchPageContent retrieves metadata (Title, Description, OGP) and text content from the given URL.
// It enforces strict security measures: timeout, size limit, content type check, and redirect validation.
func FetchPageContent(ctx context.Context, urlStr string, blacklist []string) (*PageContent, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	// Custom client to handle redirect validation
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return errors.New("stopped after 10 redirects")
			}
			// Validate redirect URL
			// If blacklist is nil/empty, IsValidURL will still check for IP addresses and scheme
			if err := IsValidURL(req.URL.String(), blacklist); err != nil {
				return fmt.Errorf("リダイレクト先が不正です: %w", err)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("リクエスト作成エラー: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("通信エラー: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPエラー: %d", resp.StatusCode)
	}

	// Content-Type check
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, ContentTypeHTML) {
		return nil, errors.New("HTMLコンテンツではありません")
	}

	// Limit reader to prevent reading large files
	limitedReader := io.LimitReader(resp.Body, MaxBodySize)

	// リダイレクト後の最終URLを取得
	finalURL := resp.Request.URL.String()

	return extractPageContent(limitedReader, finalURL)
}

func extractPageContent(r io.Reader, urlStr string) (*PageContent, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("HTMLパースエラー: %w", err)
	}

	meta := &PageContent{URL: urlStr}
	extractMetaAndTitle(doc, meta)

	// Extract text content from body
	meta.Content = extractTextContent(doc)

	// タイトルなどのクリーニング
	meta.Title = strings.TrimSpace(meta.Title)
	meta.Description = strings.TrimSpace(meta.Description)
	meta.Content = strings.TrimSpace(meta.Content)

	return meta, nil
}

func extractMetaAndTitle(n *html.Node, meta *PageContent) {
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
		extractMetaAndTitle(c, meta)
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
	// Use rune based slicing to avoid invalid UTF-8 sequences
	runes := []rune(sb.String())
	if len(runes) > MaxContentLength {
		return string(runes[:MaxContentLength]) + ContentTruncationSuffix
	}
	return string(runes)
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func FormatPageContent(meta *PageContent) string {
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
