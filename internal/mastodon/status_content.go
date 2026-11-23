package mastodon

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

func ExtractStatusText(status *gomastodon.Status) string {
	content := stripHTML(string(status.Content))
	words := strings.Fields(content)

	var filtered []string
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			filtered = append(filtered, word)
		}
	}

	return strings.Join(filtered, " ")
}

func (c *Client) DownloadStatusImages(ctx context.Context, status *gomastodon.Status) []model.Image {
	var images []model.Image
	for _, attachment := range status.MediaAttachments {
		if attachment.Type == "image" {
			base64Image, mediaType, err := c.downloadImage(ctx, attachment.URL)
			if err != nil {
				log.Printf("画像ダウンロードエラー (%s): %v", attachment.URL, err)
				continue
			}
			images = append(images, model.Image{
				Data:      base64Image,
				MediaType: mediaType,
			})
		}
	}

	return images
}

func (c *Client) downloadImage(ctx context.Context, url string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return "", "", fmt.Errorf("not an image: %s", mimeType)
	}

	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}

func stripHTML(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return htmlStr
	}

	var buf strings.Builder
	extractText(doc, &buf)
	return buf.String()
}

func (c *Client) StripHTML(htmlStr string) string {
	return stripHTML(htmlStr)
}

func extractText(n *html.Node, buf *strings.Builder) {
	if n.Type == html.TextNode {
		buf.WriteString(n.Data)
	} else if n.Type == html.ElementNode && n.Data == "br" {
		buf.WriteString("\n")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, buf)
	}
}
