package mastodon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	gomastodon "github.com/mattn/go-mastodon"
)

func (c *Client) BuildMention(acct string) string {
	return "@" + acct + " "
}

func (c *Client) PostResponseWithSplit(ctx context.Context, inReplyToID, mention, response, visibility string) ([]*gomastodon.Status, error) {
	parts := splitResponse(response, mention, c.config.MaxPostChars)
	if len(parts) == 0 {
		return nil, fmt.Errorf("投稿の文字数上限 %d にメンションと本文が収まりません", c.config.MaxPostChars)
	}

	var postedStatuses []*gomastodon.Status
	currentReplyID := inReplyToID
	for i, part := range parts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return postedStatuses, ctx.Err()
			case <-time.After(SplitPostDelay):
			}
		}

		content := mention + part
		status, err := c.postReply(ctx, currentReplyID, content, visibility)
		if err != nil {
			log.Printf("分割投稿失敗 (%d/%d): %v", i+1, len(parts), err)
			if errorNotifier != nil {
				go errorNotifier("分割投稿失敗", err.Error())
			}
			return postedStatuses, err
		}
		currentReplyID = string(status.ID)
		postedStatuses = append(postedStatuses, status)
	}

	return postedStatuses, nil
}

func (c *Client) PostResponseWithMedia(ctx context.Context, inReplyToID, mention, response, visibility, mediaPath string) (string, error) {
	attachment, err := c.client.UploadMedia(ctx, mediaPath)
	if err != nil {
		log.Printf("メディアアップロードエラー: %v", err)
		if errorNotifier != nil {
			go errorNotifier("メディアアップロードエラー", err.Error())
		}
		return "", err
	}

	fullResponse := mention + " " + response
	toot := &gomastodon.Toot{
		Status:      fullResponse,
		InReplyToID: gomastodon.ID(inReplyToID),
		Visibility:  visibility,
		MediaIDs:    []gomastodon.ID{attachment.ID},
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		c.handlePostError(err, "Media", fullResponse)
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
		c.handlePostError(err, "Reply", content)
		return nil, err
	}

	return status, nil
}

func (c *Client) PostStatus(ctx context.Context, content, visibility string) (*gomastodon.Status, error) {
	limit := c.config.MaxPostChars - utf8.RuneCountInString(BotTag)
	if limit < 0 {
		return nil, fmt.Errorf("投稿の文字数上限 %d にBotタグが収まりません", c.config.MaxPostChars)
	}
	truncated := truncateText(content, limit) + BotTag

	toot := &gomastodon.Toot{
		Status:     truncated,
		Visibility: visibility,
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		c.handlePostError(err, "Status", truncated)
		return nil, err
	}
	return status, nil
}

func (c *Client) handlePostError(err error, contextType, content string) {
	log.Printf("投稿エラー (%s): %v", contextType, err)

	var apiErr *gomastodon.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 422 {
		log.Printf("⚠️ 422 Error detected (%s). Content length: %d", contextType, len([]rune(content)))
		if errorNotifier != nil {
			go errorNotifier(fmt.Sprintf("投稿エラー (%s/422)", contextType), fmt.Sprintf("Error: %v\nContent: %s", err, content))
		}
		return
	}

	if errorNotifier != nil {
		go errorNotifier(fmt.Sprintf("投稿エラー (%s)", contextType), err.Error())
	}
}

func splitResponse(response, mention string, maxChars int) []string {
	mentionLen := utf8.RuneCountInString(mention)
	maxContentLen := maxChars - mentionLen
	if maxContentLen <= 0 {
		return nil
	}

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
