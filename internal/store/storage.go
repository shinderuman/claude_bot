package store

import (
	"context"

	"claude_bot/internal/model"
)

// FactStorage defines the interface for fact storage backends
type FactStorage interface {
	// Add adds a new fact or updates an existing one
	Add(ctx context.Context, fact model.Fact) error

	// GetByTarget returns all facts for a specific target
	GetByTarget(ctx context.Context, target string) ([]model.Fact, error)

	// GetRecent returns the most recent n facts
	GetRecent(ctx context.Context, limit int) ([]model.Fact, error)

	// SearchFuzzy searches facts based on targets and keys
	SearchFuzzy(ctx context.Context, targets []string, keys []string) ([]model.Fact, error)

	// Remove removes facts based on a filter function
	Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error)

	// Replace replaces specific facts for a target atomically
	Replace(ctx context.Context, target string, remove []model.Fact, add []model.Fact) error

	// GetAllFacts returns all facts (for backup/migration)
	GetAllFacts(ctx context.Context) ([]model.Fact, error)

	// EnforceMaxFacts keeps only the most recent maxFacts facts, removing older ones
	EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error)

	// Close cleans up resources
	Close() error
}

const (
	MinTargetUserNameFuzzyLength = 5
)
