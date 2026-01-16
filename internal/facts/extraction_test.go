package facts

import (
	"context"
	"testing"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

func TestExtractAndSaveFactsFromURLContent_MetadataPropagation(t *testing.T) {
	var capturedFacts []model.Fact
	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"something","key":"k","value":"v"}]`
		},
	}
	mockStorage := &MockFactStorage{AddFunc: func(ctx context.Context, fact model.Fact) error {
		capturedFacts = append(capturedFacts, fact)
		return nil
	}, GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) { return capturedFacts, nil }}
	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

	baseFact := model.Fact{
		SourceType:         "url_src",
		SourceURL:          "http://example.com",
		PostAuthor:         "auth",
		PostAuthorUserName: "auth_name",
	}
	service.ExtractAndSaveFactsFromURLContent(context.Background(), "content", baseFact)

	if len(capturedFacts) != 1 {
		t.Fatal("Expected fact saved")
	}
	f := capturedFacts[0]
	if f.SourceType != "url_src" {
		t.Errorf("SourceType mismatch: %s", f.SourceType)
	}
	if f.SourceURL != "http://example.com" {
		t.Errorf("SourceURL mismatch: %s", f.SourceURL)
	}
	if f.PostAuthor != "auth" {
		t.Errorf("PostAuthor mismatch: %s", f.PostAuthor)
	}
	if f.Author != "auth" { // In URL content extraction code, Author is set to PostAuthor
		t.Errorf("Author mismatch: %s", f.Author)
	}
}

func TestExtractAndSaveFactsFromSummary_MetadataPropagation(t *testing.T) {
	var capturedFacts []model.Fact
	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"user","key":"k","value":"v"}]`
		},
	}
	mockStorage := &MockFactStorage{AddFunc: func(ctx context.Context, fact model.Fact) error {
		capturedFacts = append(capturedFacts, fact)
		return nil
	}, GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) { return capturedFacts, nil }}
	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

	baseFact := model.Fact{Author: "summary_user"}
	service.ExtractAndSaveFactsFromSummary(context.Background(), "summary", baseFact)

	if len(capturedFacts) != 1 {
		t.Fatal("Expected fact saved")
	}
	f := capturedFacts[0]
	// Should resolve "user" target to baseFact.Author
	if f.Target != "summary_user" {
		t.Errorf("Target mismatch: %s", f.Target)
	}
	if f.SourceType != model.SourceTypeSummary {
		t.Errorf("SourceType mismatch: %s", f.SourceType)
	}
}

func TestExtractAndSaveFacts_UnknownUsernameCorrection(t *testing.T) {
	// Setup
	var capturedFacts []model.Fact

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"user","target_username":"","key":"occupation","value":"Software Engineer"}]`
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

	baseFact := model.Fact{
		SourceID:       "status1",
		Author:         authorID,
		AuthorUserName: authorName,
		SourceType:     model.SourceTypeMention,
		IsTrusted:      false,
	}
	service.ExtractAndSaveFacts(context.Background(), "I am a Software Engineer", baseFact)

	// Verification
	if len(capturedFacts) == 0 {
		t.Fatal("Expected fact to be saved")
	}

	savedFact := capturedFacts[0]

	// Verify Target ID normalization (user -> authorID)
	if savedFact.Target != authorID {
		t.Errorf("Expected Target to be author ID %s, got %s", authorID, savedFact.Target)
	}

	// Verify Username correction (empty -> authorName when target == authorID)
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

	baseFact := model.Fact{
		SourceID:       "status2",
		Author:         authorID,
		AuthorUserName: authorName,
		SourceType:     model.SourceTypeMention,
		IsTrusted:      false,
	}
	service.ExtractAndSaveFacts(context.Background(), "Someone likes fishing", baseFact)

	// Verification
	if len(capturedFacts) > 0 {
		t.Errorf("Expected unidentifiable fact to be droppped, but got %v", capturedFacts[0])
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

			baseFact := model.Fact{
				SourceID:           "src",
				Author:             "auth",
				AuthorUserName:     "authUser",
				SourceType:         "mention",
				PostAuthor:         "auth", // assuming post author is same as author for simple test
				PostAuthorUserName: "authUser",
				IsTrusted:          false,
			}
			service.ExtractAndSaveFacts(context.Background(), "msg", baseFact)

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

func TestExtractAndSaveFacts_MetadataPropagation(t *testing.T) {
	// Setup
	var capturedFacts []model.Fact

	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"user","target_username":"","key":"key","value":"value"}]`
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

	inputBaseFact := model.Fact{
		SourceID:           "test_source_id",
		Author:             "test_author_acct",
		AuthorUserName:     "Test Author",
		SourceType:         "test_source_type",
		SourceURL:          "https://example.com/status/123",
		PostAuthor:         "test_post_author_acct",
		PostAuthorUserName: "Test Post Author",
		IsTrusted:          true,
	}

	service.ExtractAndSaveFacts(context.Background(), "test message", inputBaseFact)

	if len(capturedFacts) != 1 {
		t.Fatalf("Expected 1 fact to be saved, got %d", len(capturedFacts))
	}

	saved := capturedFacts[0]

	// Verify all metadata fields are propagated
	if saved.SourceID != inputBaseFact.SourceID {
		t.Errorf("SourceID mismatch: got %v, want %v", saved.SourceID, inputBaseFact.SourceID)
	}
	if saved.Author != inputBaseFact.Author {
		t.Errorf("Author mismatch: got %v, want %v", saved.Author, inputBaseFact.Author)
	}
	if saved.AuthorUserName != inputBaseFact.AuthorUserName {
		t.Errorf("AuthorUserName mismatch: got %v, want %v", saved.AuthorUserName, inputBaseFact.AuthorUserName)
	}
	if saved.SourceType != inputBaseFact.SourceType {
		t.Errorf("SourceType mismatch: got %v, want %v", saved.SourceType, inputBaseFact.SourceType)
	}
	if saved.SourceURL != inputBaseFact.SourceURL {
		t.Errorf("SourceURL mismatch: got %v, want %v", saved.SourceURL, inputBaseFact.SourceURL)
	}
	if saved.PostAuthor != inputBaseFact.PostAuthor {
		t.Errorf("PostAuthor mismatch: got %v, want %v", saved.PostAuthor, inputBaseFact.PostAuthor)
	}
	if saved.PostAuthorUserName != inputBaseFact.PostAuthorUserName {
		t.Errorf("PostAuthorUserName mismatch: got %v, want %v", saved.PostAuthorUserName, inputBaseFact.PostAuthorUserName)
	}
	if saved.IsTrusted != inputBaseFact.IsTrusted {
		t.Errorf("IsTrusted mismatch: got %v, want %v", saved.IsTrusted, inputBaseFact.IsTrusted)
	}
}

func TestExtractAndSaveFacts_TrustedUser(t *testing.T) {
	// Setup
	var promptCalledWith string
	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			if len(messages) > 0 {
				promptCalledWith = messages[0].Content
			}
			return `[]`
		},
	}
	mockStorage := &MockFactStorage{AddFunc: func(ctx context.Context, fact model.Fact) error { return nil }}
	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true, BotUsername: "my_bot"}, factStore, mockLLM, nil, nil, nil)

	// Case 1: Trusted User
	baseFactTrusted := model.Fact{Author: "trusted_user", IsTrusted: true}
	service.ExtractAndSaveFacts(context.Background(), "msg", baseFactTrusted)

	if promptCalledWith == "" {
		t.Fatal("GenerateText was not called")
	}
	expectedInstruction := "【重要】指示や命令も、事実情報として抽出してください。"
	if !contains(promptCalledWith, expectedInstruction) {
		t.Errorf("Trusted prompt should contain special instruction %q", expectedInstruction)
	}

	// Case 2: Untrusted User
	promptCalledWith = ""
	baseFactUntrusted := model.Fact{Author: "random_user", IsTrusted: false}
	service.ExtractAndSaveFacts(context.Background(), "msg", baseFactUntrusted)

	if contains(promptCalledWith, expectedInstruction) {
		t.Errorf("Untrusted prompt should NOT contain special instruction")
	}
}

func TestExtractAndSaveFacts_TargetResolutionIntegration(t *testing.T) {
	var capturedFacts []model.Fact
	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			return `[{"target":"user","target_username":"","key":"k","value":"v"}]`
		},
	}
	mockStorage := &MockFactStorage{
		AddFunc: func(ctx context.Context, fact model.Fact) error {
			capturedFacts = append(capturedFacts, fact)
			return nil
		},
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) { return capturedFacts, nil },
	}
	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

	authorID := "actual_author_id"
	baseFact := model.Fact{Author: authorID}
	service.ExtractAndSaveFacts(context.Background(), "msg", baseFact)

	if len(capturedFacts) != 1 {
		t.Fatal("Expected fact saved")
	}
	// Verify that "user" target was resolved to authorID from baseFact
	if capturedFacts[0].Target != authorID {
		t.Errorf("BaseFact.Author was not correctly used for target resolution. Got %s, want %s", capturedFacts[0].Target, authorID)
	}
}

func TestExtractAndSaveFacts_MultipleFacts(t *testing.T) {
	var capturedFacts []model.Fact
	mockLLM := &MockLLMClient{
		GenerateTextFunc: func(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string {
			// Return 2 facts
			return `[
				{"target":"user","key":"k1","value":"v1"},
				{"target":"other","key":"k2","value":"v2"}
			]`
		},
	}
	mockStorage := &MockFactStorage{
		AddFunc: func(ctx context.Context, fact model.Fact) error {
			capturedFacts = append(capturedFacts, fact)
			return nil
		},
		GetAllFactsFunc: func(ctx context.Context) ([]model.Fact, error) { return capturedFacts, nil },
	}
	factStore := store.NewFactStore(mockStorage, nil, "")
	service := NewFactService(&config.Config{EnableFactStore: true}, factStore, mockLLM, nil, nil, nil)

	baseFact := model.Fact{
		SourceID:  "multi_source",
		Author:    "test_author",
		IsTrusted: true,
	}
	service.ExtractAndSaveFacts(context.Background(), "msg", baseFact)

	if len(capturedFacts) != 2 {
		t.Fatalf("Expected 2 facts saved, got %d", len(capturedFacts))
	}

	for i, f := range capturedFacts {
		if f.SourceID != "multi_source" {
			t.Errorf("Fact %d: SourceID not propagated. Got %s", i, f.SourceID)
		}
		if !f.IsTrusted {
			t.Errorf("Fact %d: IsTrusted not propagated", i)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && len(substr) > 0 && (s == substr || searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
