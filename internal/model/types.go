package model

import (
	"time"
)

// IntentType represents the user's intent derived from the message
type IntentType string

const (
	IntentChat            IntentType = "chat"
	IntentImageGeneration IntentType = "image_generation"
	IntentAnalysis        IntentType = "analysis"
	IntentDailySummary    IntentType = "daily_summary"
)

type Conversation struct {
	RootStatusID string
	CreatedAt    time.Time
	LastUpdated  time.Time
	Messages     []Message
}

type Message struct {
	Role    string
	Content string
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
	SourceType         string `json:"source_type,omitempty"`          // "mention", "federated", "home"
	SourceURL          string `json:"source_url,omitempty"`           // 投稿のURL
	PostAuthor         string `json:"post_author,omitempty"`          // 投稿者のAcct
	PostAuthorUserName string `json:"post_author_username,omitempty"` // 投稿者の表示名
}

type SearchQuery struct {
	TargetCandidates []string `json:"target_candidates"`
	Keys             []string `json:"keys"`
}

type Image struct {
	Data      string
	MediaType string
}
