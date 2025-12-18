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

func (c *Client) ExtractUserMessage(notification *gomastodon.Notification) string {
	content, _, _ := c.ExtractContentFromStatus(notification.Status)
	return content
}

// ExtractContentFromStatus extracts clean text content and images from a status
func (c *Client) ExtractContentFromStatus(status *gomastodon.Status) (string, []model.Image, error) {
	content := stripHTML(string(status.Content))
	words := strings.Fields(content)

	var filtered []string
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			filtered = append(filtered, word)
		}
	}

	text := strings.Join(filtered, " ")

	var images []model.Image
	for _, attachment := range status.MediaAttachments {
		if attachment.Type == "image" {
			base64Image, mediaType, err := c.downloadImage(attachment.URL)
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

	return text, images, nil
}

func (c *Client) downloadImage(url string) (string, string, error) {
	resp, err := http.Get(url)
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

	// メディアタイプ判定
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

// StripHTML exposes stripHTML as a public method
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

func (c *Client) BuildMention(acct string) string {
	return "@" + acct + " "
}

func (c *Client) PostResponseWithSplit(ctx context.Context, inReplyToID, mention, response, visibility string) ([]*gomastodon.Status, error) {
	parts := splitResponse(response, mention, c.config.MaxPostChars)

	var postedStatuses []*gomastodon.Status
	currentReplyID := inReplyToID
	for i, part := range parts {
		// 投稿順序の保証のため待機
		if i > 0 {
			time.Sleep(SplitPostDelay)
		}

		content := mention + part
		status, err := c.postReply(ctx, currentReplyID, content, visibility)
		if err != nil {
			log.Printf("分割投稿失敗 (%d/%d): %v", i+1, len(parts), err)
			return postedStatuses, err
		}
		currentReplyID = string(status.ID)
		postedStatuses = append(postedStatuses, status)
	}

	return postedStatuses, nil
}

// PostResponseWithMedia posts a response with media attachment
func (c *Client) PostResponseWithMedia(ctx context.Context, inReplyToID, mention, response, visibility, mediaPath string) (string, error) {
	// Upload media
	attachment, err := c.client.UploadMedia(ctx, mediaPath)
	if err != nil {
		log.Printf("メディアアップロードエラー: %v", err)
		return "", err
	}

	// Post with media
	fullResponse := mention + " " + response
	toot := &gomastodon.Toot{
		Status:      fullResponse,
		InReplyToID: gomastodon.ID(inReplyToID),
		Visibility:  visibility,
		MediaIDs:    []gomastodon.ID{attachment.ID},
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー (Media): %v", err)
		if strings.Contains(err.Error(), "422") {
			log.Printf("⚠️ 422 Error detected. Content length: %d", len([]rune(fullResponse)))
			log.Printf("Rejected Content: %s", fullResponse)
		}
		return "", err
	}

	return string(status.ID), nil
}

func (c *Client) postReply(ctx context.Context, inReplyToID, content, visibility string) (*gomastodon.Status, error) {
	toot := &gomastodon.Toot{
		Status:      content,
		InReplyToID: gomastodon.ID(inReplyToID),
		Visibility:  visibility,
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー: %v", err)
		if strings.Contains(err.Error(), "422") {
			log.Printf("⚠️ 422 Error detected (Reply). Content length: %d", len([]rune(content)))
			log.Printf("Rejected Content: %s", content)
		}
		return nil, err
	}

	return status, nil
}

// PostStatus posts a new status (not a reply)
func (c *Client) PostStatus(ctx context.Context, content, visibility string) (*gomastodon.Status, error) {
	toot := &gomastodon.Toot{
		Status:     content,
		Visibility: visibility,
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー (Status): %v", err)
		if strings.Contains(err.Error(), "422") {
			log.Printf("⚠️ 422 Error detected (Status). Content length: %d", len([]rune(content)))
			log.Printf("Rejected Content: %s", content)
		}
		return nil, err
	}
	return status, nil
}

func splitResponse(response, mention string, maxChars int) []string {
	mentionLen := len([]rune(mention))
	maxContentLen := maxChars - mentionLen

	runes := []rune(response)
	if len(runes) <= maxContentLen {
		return []string{response}
	}

	return splitByNewline(runes, maxContentLen)
}

func splitByNewline(runes []rune, maxLen int) []string {
	var parts []string
	start := 0

	for start < len(runes) {
		end := start + maxLen
		if end >= len(runes) {
			parts = append(parts, string(runes[start:]))
			break
		}

		splitPos := findLastNewline(runes, start, end)
		if splitPos == -1 {
			splitPos = end
		}

		parts = append(parts, string(runes[start:splitPos]))
		start = skipLeadingNewlines(runes, splitPos)
	}

	return parts
}

func findLastNewline(runes []rune, start, end int) int {
	for i := end - 1; i > start; i-- {
		if runes[i] == '\n' {
			return i
		}
	}
	return -1
}

func skipLeadingNewlines(runes []rune, pos int) int {
	for pos < len(runes) && runes[pos] == '\n' {
		pos++
	}
	return pos
}
