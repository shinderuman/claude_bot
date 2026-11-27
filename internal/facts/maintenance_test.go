package facts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

type MockLLMClient struct {
	GenerateTextFunc func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string
}

func (m *MockLLMClient) GenerateText(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64, _ string) string {
	if m.GenerateTextFunc != nil {
		return m.GenerateTextFunc(ctx, messages, systemPrompt, maxTokens, currentImages, temperature)
	}
	return ""
}

func (m *MockLLMClient) GenerateJSON(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, operation string, result any) error {
	return json.Unmarshal([]byte(m.GenerateText(ctx, messages, systemPrompt, maxTokens, nil, 0, operation)), result)
}

type MockFactStorage struct {
	AddFunc         func(ctx context.Context, fact model.Fact) error
	GetByTargetFunc func(ctx context.Context, target string) ([]model.Fact, error)
	GetAllFactsFunc func(ctx context.Context) ([]model.Fact, error)
	RemoveFunc      func(ctx context.Context, target string, filter func(model.Fact) bool) (int, error)
	ReplaceFunc     func(ctx context.Context, target string, oldFacts, newFacts []model.Fact) error
}

func (m *MockFactStorage) Add(ctx context.Context, fact model.Fact) error {
	return m.AddFunc(ctx, fact)
}
func (m *MockFactStorage) GetByTarget(ctx context.Context, target string) ([]model.Fact, error) {
	return m.GetByTargetFunc(ctx, target)
}
func (m *MockFactStorage) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	return m.GetAllFactsFunc(ctx)
}
func (m *MockFactStorage) Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error) {
	if m.RemoveFunc != nil {
		return m.RemoveFunc(ctx, target, filter)
	}
	return 0, nil
}
func (m *MockFactStorage) Replace(ctx context.Context, target string, oldFacts, newFacts []model.Fact) error {
	if m.ReplaceFunc != nil {
		return m.ReplaceFunc(ctx, target, oldFacts, newFacts)
	}
	return nil
}
func (m *MockFactStorage) GetRecent(ctx context.Context, limit int) ([]model.Fact, error) {
	return nil, nil
}
func (m *MockFactStorage) Close() error { return nil }
func (m *MockFactStorage) EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error) {
	return 0, nil
}
func (m *MockFactStorage) SearchFuzzy(ctx context.Context, targets []string, keys []string) ([]model.Fact, error) {
	return nil, nil
}

func TestConsolidateBotFacts(t *testing.T) {
	target := "test_bot"
	systemFact := model.Fact{
		Target: target, Key: "system:colleague_profile:other", Value: "Ignore me", Timestamp: time.Now(),
	}
	normalFact1 := model.Fact{
		Target: target, Key: "preference", Value: "Apple", Timestamp: time.Now(),
	}
	normalFact2 := model.Fact{
		Target: target, Key: "preference", Value: "Banana", Timestamp: time.Now(),
	}

	facts := []model.Fact{systemFact, normalFact1, normalFact2}

	mockStorage := &MockFactStorage{
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) {
			return nil, nil
		},
		GetByTargetFunc: func(ctx context.Context, tgt string) ([]model.Fact, error) {
			if tgt == target {
				return facts, nil
			}
			return nil, nil
		},
		ReplaceFunc: func(ctx context.Context, tgt string, oldFacts, newFacts []model.Fact) error {
			for _, f := range oldFacts {
				if strings.HasPrefix(f.Key, "system:") {
					t.Errorf("Replace called with system fact in oldFacts (it should have been filtered out!): %v", f.Key)
				}
			}
			if len(oldFacts) != 2 {
				t.Errorf("Expected 2 facts to be replaced, got %d", len(oldFacts))
			}
			return nil
		},
	}

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"test_bot","key":"preference","value":"Fruit (Consolidated)","timestamp":"2023-01-01T00:00:00Z"}]`
		},
	}

	factStore := store.NewFactStore(mockStorage, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

	err := service.ConsolidateBotFacts(context.Background(), target, facts)
	if err != nil {
		t.Fatalf("ConsolidateBotFacts failed: %v", err)
	}

}

func TestArchiveKeepsFactsWhenLaterBatchFails(t *testing.T) {
	const target = "user@example.com"
	input := makeFacts(FactArchiveBatchSize+1, time.Now())
	storage := store.NewMemoryFactStore()
	for i := range input {
		input[i].Target = target
		if err := storage.Add(t.Context(), input[i]); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(context.Context, []model.Message, string, int64, []model.Image, float64) string {
			calls++
			if calls == 1 {
				return `[{"key":"archive","value":"first batch"}]`
			}
			return ""
		},
	}
	service := NewFactService(&config.Config{BotUsername: "bot"}, store.NewFactStore(storage, ""), mockLLM, nil, nil, nil)
	if err := service.archiveTargetFacts(t.Context(), target, input); err == nil || calls != 2 {
		t.Fatalf("archiving error = %v, LLM calls = %d", err, calls)
	}

	all, err := storage.GetByTarget(t.Context(), target)
	if err != nil || len(all) != len(input) {
		t.Fatalf("remaining facts = %d, %v, want %d", len(all), err, len(input))
	}
}

func TestFactReplacementRejectsTruncatedJSON(t *testing.T) {
	const target = "user"
	fact := model.Fact{Target: target, Key: "hobby", Value: "gardening"}
	for _, operation := range []string{"archive", "consolidation"} {
		t.Run(operation, func(t *testing.T) {
			storage := store.NewMemoryFactStore()
			if err := storage.Add(t.Context(), fact); err != nil {
				t.Fatal(err)
			}
			modelClient := &MockLLMClient{
				GenerateTextFunc: func(context.Context, []model.Message, string, int64, []model.Image, float64) string {
					return `[{"key":"summary","value":"first entry"},{"key":"unfinished`
				},
			}
			service := NewFactService(&config.Config{}, store.NewFactStore(storage, ""), modelClient, nil, nil, nil)

			var err error
			switch operation {
			case "archive":
				err = service.archiveTargetFacts(t.Context(), target, []model.Fact{fact})
			case "consolidation":
				err = service.ConsolidateBotFacts(t.Context(), target, []model.Fact{fact})
			}
			var syntaxErr *json.SyntaxError
			if !errors.As(err, &syntaxErr) {
				t.Fatalf("fact replacement error = %v, want JSON syntax error", err)
			}
			got, err := storage.GetByTarget(t.Context(), target)
			if err != nil || len(got) != 1 || got[0].Value != fact.Value {
				t.Fatalf("facts after failed replacement = %+v, %v", got, err)
			}
		})
	}
}
