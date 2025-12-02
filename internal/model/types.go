package model

import (
	"time"
)

type Conversation struct {
	RootStatusID string
	CreatedAt    time.Time
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
	Target         string    `json:"target"`          // 情報の対象（誰の情報か）
	TargetUserName string    `json:"target_username"` // 対象のUserName
	Author         string    `json:"author"`          // 情報の提供者（誰が言ったか）
	AuthorUserName string    `json:"author_username"` // 提供者のUserName
	Key            string    `json:"key"`
	Value          string    `json:"value"`
	Timestamp      time.Time `json:"timestamp"`
}

type SearchQuery struct {
	TargetCandidates []string `json:"target_candidates"`
	Keys             []string `json:"keys"`
}
