package mastodon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"claude_bot/internal/config"

	"github.com/mattn/go-mastodon"
)

func TestFormatProfileBody(t *testing.T) {
	c := &Client{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Short text",
			input: "Line 1\n\n\nLine 2",
			want:  "Line 1\nLine 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.FormatProfileBody(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildProfileFields(t *testing.T) {
	c := &Client{}
	cfg := &config.Config{
		AllowRemoteUsers: true,
		Timezone:         "Asia/Tokyo",
		LLMProvider:      "gemini",
		GeminiModel:      "gemini-1.5-pro",
	}
	authKey := "test-auth-key"
	existingFields := []mastodon.Field{
		{Name: "Existing", Value: "Val"},
	}

	got := c.BuildProfileFields(cfg, existingFields, authKey)

	if len(got) != 5 {
		t.Fatalf("Expected 5 fields, got %d", len(got))
	}

	if got[0].Name != "Existing" {
		t.Errorf("First field should be preserved. Got %s", got[0].Name)
	}

	if got[1].Name != ProfileFieldSystemID {
		t.Errorf("Second field should be SystemID. Got %s", got[1].Name)
	}

	if got[2].Name != ProfileFieldMentionStatus {
		t.Errorf("Third field should be MentionStatus. Got %s", got[2].Name)
	}

	if got[3].Name != ProfileFieldLastUpdated {
		t.Errorf("Fourth field should be LastUpdated. Got %s", got[3].Name)
	}

	if got[4].Name != ProfileFieldModelName {
		t.Errorf("Fifth field should be ModelName. Got %s", got[4].Name)
	}
	if got[4].Value != "gemini-1.5-pro" {
		t.Errorf("ModelName value incorrect. Got %s", got[4].Value)
	}
}

func TestExtractCleanProfileNote(t *testing.T) {
	const DisclaimerText = "\n\n※このアカウントの投稿には事実に基づく内容が含まれることもありますが、すべての正確性は保証できません。"
	c := &Client{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Note with disclaimer",
			input: "This is a profile." + DisclaimerText,
			want:  "This is a profile.",
		},
		{
			name:  "Note with HTML and disclaimer",
			input: "<p>This is a <b>profile</b>.</p>" + DisclaimerText,
			want:  "This is a profile.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.ExtractCleanProfileNote(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpdateProfilePublishesNoteAndFieldsTogether(t *testing.T) {
	var updates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/accounts/verify_credentials":
			_, _ = io.WriteString(w, `{"id":"bot","fields":[{"name":"Existing","value":"keep"},{"name":"SystemID","value":"old"}]}`)
		case "PATCH /api/v1/accounts/update_credentials":
			updates.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			want := map[string]string{
				"note":                        "new profile" + DisclaimerText,
				"fields_attributes[0][name]":  "Existing",
				"fields_attributes[0][value]": "keep",
				"fields_attributes[1][name]":  ProfileFieldSystemID,
				"fields_attributes[1][value]": "new",
				"fields_attributes[2][value]": MentionStatusStopped,
			}
			for field, value := range want {
				if got := r.PostForm.Get(field); got != value {
					t.Errorf("%s = %q, want %q", field, got, value)
				}
			}
			_, _ = io.WriteString(w, `{"id":"bot"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL})
	if err := client.UpdateProfileWithFields(t.Context(), &config.Config{Timezone: "UTC"}, "new profile", "new"); err != nil {
		t.Fatal(err)
	}
	if got := updates.Load(); got != 1 {
		t.Fatalf("profile updates = %d, want 1", got)
	}
}
