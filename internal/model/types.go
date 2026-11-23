package model

import (
	"fmt"
	"time"
)

type IntentType string

const (
	IntentChat            IntentType = "chat"
	IntentImageGeneration IntentType = "image_generation"
	IntentAnalysis        IntentType = "analysis"
	IntentDailySummary    IntentType = "daily_summary"
	IntentFollowRequest   IntentType = "follow_request"

	RoleUser      = "user"
	RoleAssistant = "assistant"

	SourceTypeMention    = "mention"
	SourceTypeSummary    = "summary"
	SourceTypeArchive    = "archive"
	SourceTypeFederated  = "federated"
	SourceTypeHome       = "home"
	SourceTypeMentionURL = "mention_url"
	SourceTypeTest       = "test"
	SourceTypeSelf       = "self"
	SourceTypeSystem     = "system"

	GeneralTarget = "__general__"
	UnknownTarget = "unknown"

	SystemFactKeyPrefix             = SourceTypeSystem + ":"
	SystemColleagueProfileKeyPrefix = SystemFactKeyPrefix + "colleague_profile:"
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
	StatusIDs []string
}

func (c *Conversation) GetLastUserStatusID() string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		msg := c.Messages[i]
		if msg.Role == RoleUser && len(msg.StatusIDs) > 0 {
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
	Target         string    `json:"target"`
	TargetUserName string    `json:"target_username"`
	Author         string    `json:"author"`
	AuthorUserName string    `json:"author_username"`
	Key            string    `json:"key"`
	Value          string    `json:"value"`
	Timestamp      time.Time `json:"timestamp"`

	SourceID           string `json:"source_id,omitempty"`
	SourceType         string `json:"source_type,omitempty"`
	SourceURL          string `json:"source_url,omitempty"`
	PostAuthor         string `json:"post_author,omitempty"`
	PostAuthorUserName string `json:"post_author_username,omitempty"`
	IsTrusted          bool   `json:"is_trusted,omitempty"`
}

func (f *Fact) ComputeUniqueKey() string {
	return fmt.Sprintf("%s|%s|%s", f.Target, f.Key, f.Value)
}

type SearchQuery struct {
	TargetCandidates []string `json:"target_candidates"`
	Keys             []string `json:"keys"`
}

type Image struct {
	Data      string
	MediaType string
}
