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
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

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
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

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
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

	authorID := "user123"
	authorName := "test_user"

	service.ExtractAndSaveFacts(context.Background(), "status3", authorID, authorName, "Other person is kind", model.SourceTypeMention, "", "", "")

	// Verification: Should NOT save fact if username is unknown
	if len(capturedFacts) > 0 {
		t.Errorf("Expected fact with unknown username to be dropped, but got: %v", capturedFacts[0])
	}
}

func TestExtractAndSaveFacts_BotValidation(t *testing.T) {
	// Setup
	var capturedFacts []model.Fact

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return ""
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

	knownBots := map[string]struct{}{
		"known_bot_user": {},
	}

	fs := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, fs, mockLLM, nil, nil, knownBots)

	tests := []struct {
		name           string
		llmResponse    string
		expectSaved    bool
		targetOverride string // If set, overrides the target in the fact for checking
	}{
		{
			name:        "Known Bot with 'bot' in value -> Should Skip",
			llmResponse: `[{"target":"known_bot_user","target_username":"Known Bot","key":"type","value":"This is a bot account"}]`,
			expectSaved: false,
		},
		{
			name:        "Known Bot without 'bot' in value -> Should Save",
			llmResponse: `[{"target":"known_bot_user","target_username":"Known Bot","key":"status","value":"Online now"}]`,
			expectSaved: true,
		},
		{
			name:        "Unknown Bot (Regular User) with 'bot' in value -> Should Save",
			llmResponse: `[{"target":"regular_user","target_username":"Regular User","key":"description","value":"I am not a bot"}]`,
			expectSaved: true,
		},
		{
			name:        "Known Bot with 'BOT' (case insensitive) in value -> Should Skip",
			llmResponse: `[{"target":"known_bot_user","target_username":"Known Bot","key":"info","value":"I am a BOT"}]`,
			expectSaved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset captured facts
			capturedFacts = []model.Fact{}

			// Override LLM response
			mockLLM.GenerateTextFunc = func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
				return tt.llmResponse
			}

			service.ExtractAndSaveFacts(context.Background(), "src", "auth", "authUser", "msg", "mention", "", "", "")

			if tt.expectSaved {
				if len(capturedFacts) == 0 {
					t.Errorf("Expected fact to be saved, but it was not")
				}
			} else {
				if len(capturedFacts) > 0 {
					t.Errorf("Expected fact to be skipped, but it was saved: %v", capturedFacts[0])
				}
			}
		})
	}
}
