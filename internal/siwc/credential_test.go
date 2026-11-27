package siwc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndLoadCredentialRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	credential := &Credential{
		Email:          "user@example.com",
		Issuer:         "https://auth.openai.com",
		Subject:        "subject-1",
		ClientID:       "oaiapp_test",
		ExtAgentHostID: "urn:uuid:test-host",
		IDToken:        "id-token",
		AccessToken:    "access-token",
		RefreshToken:   "refresh-token",
		TokenType:      "Bearer",
		ExpiresIn:      3600,
		Scopes:         strings.Fields(RequestScopes),
		SavedAt:        time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}

	if err := SaveCredential(path, credential); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode = %v, want 0600", info.Mode().Perm())
	}

	loaded, err := LoadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	loadedData, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	wantData, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if string(loadedData) != string(wantData) {
		t.Fatalf("loaded credential = %s, want %s", loadedData, wantData)
	}
}

func TestLoadCredentialReportsMissingFile(t *testing.T) {
	if _, err := LoadCredential(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("loading an absent credential file succeeded")
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		credential Credential
		want       bool
	}{
		{
			name:       "fresh token",
			credential: Credential{AccessToken: "token", SavedAt: now.Add(-time.Minute), ExpiresIn: 3600},
			want:       false,
		},
		{
			name:       "token inside refresh margin",
			credential: Credential{AccessToken: "token", SavedAt: now.Add(-time.Hour), ExpiresIn: 3600},
			want:       true,
		},
		{
			name:       "expired token",
			credential: Credential{AccessToken: "token", SavedAt: now.Add(-2 * time.Hour), ExpiresIn: 3600},
			want:       true,
		},
		{
			name:       "cleared token",
			credential: Credential{},
			want:       true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.credential.NeedsRefresh(now); got != test.want {
				t.Fatalf("NeedsRefresh() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestClearTokensKeepsRegistration(t *testing.T) {
	credential := Credential{
		Email:        "user@example.com",
		Subject:      "subject-1",
		ClientID:     "oaiapp_test",
		AccessToken:  "access",
		RefreshToken: "refresh",
		IDToken:      "id",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		Scopes:       []string{PlanUsageScope},
	}
	credential.ClearTokens()

	if credential.ClientID == "" || credential.Subject == "" || credential.Email == "" {
		t.Fatalf("registration fields were cleared: %+v", credential)
	}
	if credential.AccessToken != "" || credential.RefreshToken != "" || credential.IDToken != "" || credential.ExpiresIn != 0 {
		t.Fatalf("tokens were not cleared: %+v", credential)
	}
}

func TestLoadOrCreateHostIDPersistsUUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	hostID, err := LoadOrCreateHostID(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hostID, "urn:uuid:") || len(hostID) != len("urn:uuid:")+36 {
		t.Fatalf("host ID = %q, want a urn:uuid value", hostID)
	}
	info, err := os.Stat(HostIDPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("host ID file mode = %v, want 0600", info.Mode().Perm())
	}

	again, err := LoadOrCreateHostID(path)
	if err != nil {
		t.Fatal(err)
	}
	if again != hostID {
		t.Fatalf("host ID = %q on reload, want %q", again, hostID)
	}
}

func TestLoadOrCreateHostIDRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(HostIDPath(path), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostID(path); err == nil || !strings.Contains(err.Error(), "ホストIDファイルが空") {
		t.Fatalf("LoadOrCreateHostID() = %v, want empty file error", err)
	}
	data, err := os.ReadFile(HostIDPath(path))
	if err != nil || len(data) != 0 {
		t.Fatalf("host ID file was changed: %q, %v", data, err)
	}
}

func TestHasPlanUsageScope(t *testing.T) {
	if (&Credential{Scopes: []string{"openid", "email"}}).HasPlanUsageScope() {
		t.Fatal("plan usage scope was accepted when missing")
	}
	if !(&Credential{Scopes: strings.Fields(RequestScopes)}).HasPlanUsageScope() {
		t.Fatal("plan usage scope was rejected when present")
	}
}

func TestPkceCodeChallenge(t *testing.T) {
	verifier := "test-verifier-value-used-for-a-known-digest-1234567890"
	if got := pkceCodeChallenge(verifier); got != "T9B5q6BPaHm3pqccygNmX97v64OWAAaoaMZtcsoZie4" {
		t.Fatalf("pkceCodeChallenge() = %q", got)
	}
}
