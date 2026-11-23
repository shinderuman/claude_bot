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
	ProfileFieldSystemID = "SystemID"

	ProfileFieldMentionStatus = "外部サーバーからのメンション受付"

	ProfileFieldLastUpdated = "プロフィール最終更新日時"

	MentionStatusPublic = "公開中"

	MentionStatusStopped = "停止中"

	ProfileFieldModelName = "AIモデル"

	DisclaimerText = "\n\n\n※このアカウントの投稿には事実に基づく内容が含まれることもありますが、すべての正確性は保証できません。"

	MaxMastodonProfileChars = 500
)

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

func (c *Client) TruncateToSafeProfileBody(text string) string {
	limit := MaxMastodonProfileChars - len([]rune(DisclaimerText))
	return truncateText(text, limit)
}

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

func (c *Client) BuildProfileFields(cfg *config.Config, currentFields []gomastodon.Field, authKey string) []gomastodon.Field {
	var newFields []gomastodon.Field

	targetKeys := map[string]struct{}{
		ProfileFieldSystemID:      {},
		ProfileFieldMentionStatus: {},
		ProfileFieldLastUpdated:   {},
		ProfileFieldModelName:     {},
	}

	for _, f := range currentFields {
		if _, isTarget := targetKeys[f.Name]; isTarget {
			continue
		}
		newFields = append(newFields, f)
	}

	newFields = append(newFields, gomastodon.Field{
		Name:  ProfileFieldSystemID,
		Value: authKey,
	})

	mentionStatus := MentionStatusStopped
	if cfg.AllowRemoteUsers {
		mentionStatus = MentionStatusPublic
	}
	newFields = append(newFields, gomastodon.Field{
		Name:  ProfileFieldMentionStatus,
		Value: mentionStatus,
	})

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.UTC
		log.Printf("タイムゾーン読み込みエラー (%s): %v. UTCを使用します。", cfg.Timezone, err)
	}
	now := time.Now().In(loc)
	lastUpdated := now.Format("2006/01/02 15:04")

	newFields = append(newFields, gomastodon.Field{
		Name:  ProfileFieldLastUpdated,
		Value: lastUpdated,
	})

	var modelName string
	switch cfg.LLMProvider {
	case config.LLMProviderGemini:
		modelName = cfg.GeminiModel
	case config.LLMProviderClaude:
		modelName = cfg.AnthropicModel
	}

	if modelName != "" {
		newFields = append(newFields, gomastodon.Field{
			Name:  ProfileFieldModelName,
			Value: modelName,
		})
	}

	return newFields
}

func (c *Client) ExtractCleanProfileNote(note string) string {
	clean := c.StripHTML(note)
	clean = strings.ReplaceAll(clean, strings.TrimSpace(DisclaimerText), "")
	return strings.TrimSpace(clean)
}
