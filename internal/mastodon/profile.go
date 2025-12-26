package mastodon

import (
	"context"
	"log"
	"strings"
	"time"

	"claude_bot/internal/config"

	gomastodon "github.com/mattn/go-mastodon"
)

const (
	// ProfileFieldSystemID is the key name for the peer authentication hash in the profile fields
	ProfileFieldSystemID = "SystemID"

	// ProfileFieldMentionStatus is the key name for the mention status in the profile fields
	ProfileFieldMentionStatus = "外部サーバーからのメンション受付"

	// ProfileFieldLastUpdated is the key name for the last updated time in the profile fields
	ProfileFieldLastUpdated = "プロフィール最終更新日時"

	// MentionStatusPublic indicates that the bot accepts mentions from remote users
	MentionStatusPublic = "公開中"

	// MentionStatusStopped indicates that the bot does not accept mentions from remote users
	MentionStatusStopped = "停止中"

	// DisclaimerText is the disclaimer appended to the Mastodon profile
	DisclaimerText = "\n\n\n※このアカウントの投稿には事実に基づく内容が含まれることもありますが、すべての正確性は保証できません。"

	// MaxMastodonProfileChars is the maximum length of a Mastodon profile (including disclaimer)
	MaxMastodonProfileChars = 500
)

// FormatProfileBody formats the profile text for Mastodon (compaction only)
func (c *Client) FormatProfileBody(text string) string {
	lines := strings.Split(text, "\n")
	var compacted []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			compacted = append(compacted, trimmed)
		}
	}
	return strings.Join(compacted, "\n")
}

// TruncateToSafeProfileBody truncates text to fit within the profile limit allowing for the disclaimer.
func (c *Client) TruncateToSafeProfileBody(text string) string {
	limit := MaxMastodonProfileChars - len([]rune(DisclaimerText))
	return truncateText(text, limit)
}

// UpdateProfileWithFields constructs and updates both the profile note and custom fields
func (c *Client) UpdateProfileWithFields(ctx context.Context, cfg *config.Config, body string, authKey string) error {
	finalNote := body + DisclaimerText
	if err := c.UpdateProfile(ctx, finalNote); err != nil {
		return err
	}

	currentUser, err := c.GetAccountCurrentUser(ctx)
	if err != nil {
		return err
	}

	newFields := c.BuildProfileFields(cfg, currentUser.Fields, authKey)
	return c.UpdateProfileFields(ctx, newFields)
}

// BuildProfileFields constructs the profile fields, including SystemID, Mention Status, and Last Updated
// NOTE: This logic is moved from facts/service.go verbatim.
func (c *Client) BuildProfileFields(cfg *config.Config, currentFields []gomastodon.Field, authKey string) []gomastodon.Field {
	var newFields []gomastodon.Field

	// Track which keys update logic has handled
	targetKeys := map[string]struct{}{
		ProfileFieldSystemID:      {},
		ProfileFieldMentionStatus: {},
		ProfileFieldLastUpdated:   {},
	}

	// 1. Existing fields: Keep non-target fields (Preserve user order)
	for _, f := range currentFields {
		if _, isTarget := targetKeys[f.Name]; isTarget {
			continue
		}
		newFields = append(newFields, f)
	}

	// 2. Add/Append managed fields in fixed order

	// SystemID
	newFields = append(newFields, gomastodon.Field{
		Name:  ProfileFieldSystemID,
		Value: authKey,
	})

	// Mention Status
	mentionStatus := MentionStatusStopped
	if cfg.AllowRemoteUsers {
		mentionStatus = MentionStatusPublic
	}
	newFields = append(newFields, gomastodon.Field{
		Name:  ProfileFieldMentionStatus,
		Value: mentionStatus,
	})

	// Last Updated
	// Load timezone from config
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		// Fallback to UTC if timezone is invalid or load fails
		loc = time.UTC
		log.Printf("タイムゾーン読み込みエラー (%s): %v. UTCを使用します。", cfg.Timezone, err)
	}
	now := time.Now().In(loc)
	lastUpdated := now.Format("2006/01/02 15:04")

	newFields = append(newFields, gomastodon.Field{
		Name:  ProfileFieldLastUpdated,
		Value: lastUpdated,
	})

	return newFields
}

// ExtractCleanProfileNote extracts the profile note without HTML and disclaimer
// NOTE: This logic is moved/consolidated from collector/collector.go.
func (c *Client) ExtractCleanProfileNote(note string) string {
	clean := c.StripHTML(note)
	// 免責事項を削除
	clean = strings.ReplaceAll(clean, strings.TrimSpace(DisclaimerText), "")
	return strings.TrimSpace(clean)
}
