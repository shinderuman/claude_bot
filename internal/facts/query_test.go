package facts

import (
	"context"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

func TestQueryRelevantFactsUsesAuthorFallbackAndAvoidsDuplicateRecentMatches(t *testing.T) {
	storage := store.NewMemoryFactStore()
	facts := []model.Fact{
		{Target: "author", TargetUserName: "Author Name", Key: "hobby", Value: "gardening", Author: "source", SourceType: model.SourceTypeHome},
		{Target: "bot", Key: "hobby", Value: "drawing"},
		{Target: model.GeneralTarget, Key: "hobby", Value: "reading"},
		{Target: "other", Key: "news", Value: "recent item"},
	}
	for i, fact := range facts {
		fact.Timestamp = time.Now().Add(-time.Duration(i) * time.Hour)
		if err := storage.Add(t.Context(), fact); err != nil {
			t.Fatal(err)
		}
	}
	modelClient := &MockLLMClient{
		GenerateTextFunc: func(context.Context, []model.Message, string, int64, []model.Image, float64) string {
			return `{"target_candidates":[],"keys":["hobby"]}`
		},
	}
	service := NewFactService(&config.Config{EnableFactStore: true, BotUsername: "bot"}, store.NewFactStore(storage, ""), modelClient, nil, nil, nil)
	result := service.QueryRelevantFacts(t.Context(), "author", "Author Name", "hobbies")
	for _, expected := range []string{"Author Name (対象ID: author)", "gardening (source: home) (情報提供者ID: source)", "drawing", "reading", "recent item"} {
		if strings.Count(result, expected) != 1 {
			t.Errorf("result = %q, %q should appear once", result, expected)
		}
	}
}
