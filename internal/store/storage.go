package store

import (
	"context"

	"claude_bot/internal/model"
)

type FactStorage interface {
	Add(ctx context.Context, fact model.Fact) error

	GetByTarget(ctx context.Context, target string) ([]model.Fact, error)

	GetRecent(ctx context.Context, limit int) ([]model.Fact, error)

	SearchFuzzy(ctx context.Context, targets []string, keys []string) ([]model.Fact, error)

	Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error)

	Replace(ctx context.Context, target string, remove []model.Fact, add []model.Fact) error

	GetAllFacts(ctx context.Context) ([]model.Fact, error)

	EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error)

	Close() error
}

const (
	MinTargetUserNameFuzzyLength = 5
)
