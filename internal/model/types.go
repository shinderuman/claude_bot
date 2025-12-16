package model

import (
	"fmt"
	"time"
)

// IntentType represents the user's intent derived from the message
type IntentType string

const (
	IntentChat            IntentType = "chat"
	IntentImageGeneration IntentType = "image_generation"
	IntentAnalysis        IntentType = "analysis"
	IntentDailySummary    IntentType = "daily_summary"
	IntentFollowRequest   IntentType = "follow_request"
)

// SourceType represents the source of a fact
const (
	// SourceTypeMention はメンションからのファクト抽出を示す
	SourceTypeMention = "mention"

	// SourceTypeSummary は会話要約からのファクト抽出を示す
	SourceTypeSummary = "summary"

	// SourceTypeArchive はアーカイブ処理によるファクトを示す
	SourceTypeArchive = "archive"

	// SourceTypeFederated は連合タイムラインからのファクト抽出を示す
	SourceTypeFederated = "federated"

	// SourceTypeHome はホームタイムラインからのファクト抽出を示す
	SourceTypeHome = "home"

	// SourceTypeMentionURL はメンション内URLからのファクト抽出を示す
	SourceTypeMentionURL = "mention_url"

	// SourceTypeTest はテスト用のソースタイプを示す
	SourceTypeTest = "test"

	// GeneralTarget は一般知識のターゲットを示す
	GeneralTarget = "__general__"

	// UnknownTarget は不明なターゲットを示す
	UnknownTarget = "unknown"
)

type Conversation struct {
	RootStatusID string
	CreatedAt    time.Time
	LastUpdated  time.Time
	Messages     []Message
}

type Message struct {
	Role      string
	Content   string
	StatusIDs []string // Mastodon Status IDs (multiple if split)
}

// GetLastUserStatusID retrieves the status ID of the last user message in the conversation
func (c *Conversation) GetLastUserStatusID() string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		msg := c.Messages[i]
		if msg.Role == "user" && len(msg.StatusIDs) > 0 {
			// ユーザー発言は通常1つのStatusIDを持つが、将来的な拡張や稀なケース（分割投稿の統合など）を考慮し、
			// 物理的に「最後」のステータスIDを取得するために末尾の要素を使用する。
			return msg.StatusIDs[len(msg.StatusIDs)-1]
		}
	}
	return ""
}

type Session struct {
	Conversations []Conversation
	Summary       string
	LastUpdated   time.Time
}

type Fact struct {
	Target         string      `json:"target"`          // 情報の対象（誰の情報か）
	TargetUserName string      `json:"target_username"` // 対象のUserName
	Author         string      `json:"author"`          // 情報の提供者（誰が言ったか）
	AuthorUserName string      `json:"author_username"` // 提供者のUserName
	Key            string      `json:"key"`
	Value          interface{} `json:"value"`
	Timestamp      time.Time   `json:"timestamp"`

	// ソース情報
	SourceID           string `json:"source_id,omitempty"`            // 情報源となった発言のID
	SourceType         string `json:"source_type,omitempty"`          // "mention", "federated", "home"
	SourceURL          string `json:"source_url,omitempty"`           // 投稿のURL
	PostAuthor         string `json:"post_author,omitempty"`          // 投稿者のAcct
	PostAuthorUserName string `json:"post_author_username,omitempty"` // 投稿者の表示名
}

// ComputeUniqueKey returns a stable unique key for the fact based on its meaningful content
func (f *Fact) ComputeUniqueKey() string {
	return fmt.Sprintf("%s|%s|%v", f.Target, f.Key, f.Value)
}

type SearchQuery struct {
	TargetCandidates []string `json:"target_candidates"`
	Keys             []string `json:"keys"`
}

type Image struct {
	Data      string
	MediaType string
}
