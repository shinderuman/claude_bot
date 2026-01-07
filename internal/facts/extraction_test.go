package facts

import (
	"context"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

func TestExtractAndSaveFacts_UnknownUsernameCorrection(t *testing.T) {
	// Setup
	var capturedFacts []model.Fact

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"user","target_username":"unknown","key":"occupation","value":"Software Engineer"}]`
		},
	}
	mockStorage := &MockFactStorage{
		AddFunc: func(ctx context.Context, fact model.Fact) error {
			capturedFacts = append(capturedFacts, fact)
			return nil
		},
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) {
			return capturedFacts, nil
		},
	}

	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil)

	authorID := "user123"
	authorName := "test_user"

	service.ExtractAndSaveFacts(context.Background(), "status1", authorID, authorName, "I am a Software Engineer", model.SourceTypeMention, "", "", "")

	// Verification
	if len(capturedFacts) == 0 {
		t.Fatal("Expected fact to be saved")
	}

	savedFact := capturedFacts[0]

	// Verify Target ID normalization (user -> authorID)
	if savedFact.Target != authorID {
		t.Errorf("Expected Target to be author ID %s, got %s", authorID, savedFact.Target)
	}

	// Verify Username correction (unknown -> authorName)
	if savedFact.TargetUserName != authorName {
		t.Errorf("Expected TargetUserName to be corrected to %s, got %s", authorName, savedFact.TargetUserName)
	}
}

func TestExtractAndSaveFacts_DropUnidentifiable(t *testing.T) {
	// Setup
	var capturedFacts []model.Fact

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"unknown","target_username":"unknown","key":"hobby","value":"fishing"}]`
		},
	}
	mockStorage := &MockFactStorage{
		AddFunc: func(ctx context.Context, fact model.Fact) error {
			capturedFacts = append(capturedFacts, fact)
			return nil
		},
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) {
			return capturedFacts, nil
		},
	}

	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil)

	authorID := "user123"
	authorName := "test_user"

	service.ExtractAndSaveFacts(context.Background(), "status2", authorID, authorName, "Someone likes fishing", model.SourceTypeMention, "", "", "")

	// Verification
	if len(capturedFacts) > 0 {
		t.Errorf("Expected unidentifiable fact to be droppped, but got %v", capturedFacts[0])
	}
}

func TestExtractAndSaveFacts_DropExampleUnknownUsername(t *testing.T) {
	// Setup
	var capturedFacts []model.Fact

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"other456","target_username":"unknown","key":"attribute","value":"kind"}]`
		},
	}
	mockStorage := &MockFactStorage{
		AddFunc: func(ctx context.Context, fact model.Fact) error {
			capturedFacts = append(capturedFacts, fact)
			return nil
		},
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) {
			return capturedFacts, nil
		},
	}

	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil)

	authorID := "user123"
	authorName := "test_user"

	service.ExtractAndSaveFacts(context.Background(), "status3", authorID, authorName, "Other person is kind", model.SourceTypeMention, "", "", "")

	// Verification: Should NOT save fact if username is unknown
	if len(capturedFacts) > 0 {
		t.Errorf("Expected fact with unknown username to be dropped, but got: %v", capturedFacts[0])
	}
}
