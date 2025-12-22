package bot

import (
	"claude_bot/internal/model"
	"reflect"
	"testing"
)

func TestCalculateFactStats(t *testing.T) {
	facts := []model.Fact{
		// Source: home
		{SourceType: "home", Target: "__general__"},
		{SourceType: "home", Target: "assistant"},
		{SourceType: "home", Target: "bot1"},

		// Source: federated
		{SourceType: "federated", Target: "userA"},
		{SourceType: "federated", Target: "userB"},
		{SourceType: "federated", Target: "userA"}, // userA count = 2

		// Source: archive
		{SourceType: "archive", Target: "userC"},

		// Unknown source
		{SourceType: "", Target: "userD"},
	}

	botUsernames := []string{"bot1", "bot2"}

	expectedSource := map[string]int{
		"home":      3,
		"federated": 3,
		"archive":   1,
		"unknown":   1,
	}

	expectedTarget := map[string]int{
		"general":   1,
		"assistant": 1,
		"bot1":      1,
		"users":     5, // userA(2) + userB(1) + userC(1) + userD(1)
	}

	stats := calculateFactStats(facts, botUsernames)

	if stats.Total != 8 {
		t.Errorf("Expected Total 8, got %d", stats.Total)
	}

	if !reflect.DeepEqual(stats.BySource, expectedSource) {
		t.Errorf("BySource mismatch.\nExpected: %v\nGot: %v", expectedSource, stats.BySource)
	}

	if !reflect.DeepEqual(stats.ByTarget, expectedTarget) {
		t.Errorf("ByTarget mismatch.\nExpected: %v\nGot: %v", expectedTarget, stats.ByTarget)
	}
}
