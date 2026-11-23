package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"claude_bot/internal/model"

	"github.com/redis/go-redis/v9"
)

func (s *RedisFactStore) GetByTarget(ctx context.Context, target string) ([]model.Fact, error) {
	entries, err := s.client.HGetAll(ctx, s.targetKey(target)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get facts for target %s: %w", target, err)
	}

	facts := make([]model.Fact, 0, len(entries))
	for hash, data := range entries {
		var fact model.Fact
		if err := json.Unmarshal([]byte(data), &fact); err != nil {
			return nil, fmt.Errorf("failed to decode fact %s for target %s: %w", hash, target, err)
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

func (s *RedisFactStore) GetRecent(ctx context.Context, limit int) ([]model.Fact, error) {
	if limit <= 0 {
		return []model.Fact{}, nil
	}
	members, err := s.client.ZRevRange(ctx, s.prefix+TimelineKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline: %w", err)
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, 0, len(members))
	for _, member := range members {
		if target, hash, ok := splitTimelineMember(member); ok {
			cmds = append(cmds, pipe.HGet(ctx, s.targetKey(target), hash))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to read timeline facts: %w", err)
	}

	facts := make([]model.Fact, 0, len(cmds))
	for _, cmd := range cmds {
		data, err := cmd.Bytes()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read a timeline fact: %w", err)
		}
		var fact model.Fact
		if err := json.Unmarshal(data, &fact); err != nil {
			return nil, fmt.Errorf("failed to decode a timeline fact: %w", err)
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

func (s *RedisFactStore) SearchFuzzy(ctx context.Context, targets, keys []string) ([]model.Fact, error) {
	facts, err := s.GetAllFacts(ctx)
	if err != nil {
		return nil, err
	}
	return filterFuzzyFacts(facts, targets, keys), nil
}

func (s *RedisFactStore) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	targets, err := s.client.SMembers(ctx, s.prefix+TargetsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get targets: %w", err)
	}

	var allFacts []model.Fact
	for _, target := range targets {
		facts, err := s.GetByTarget(ctx, target)
		if err != nil {
			return nil, err
		}
		allFacts = append(allFacts, facts...)
	}
	return allFacts, nil
}
