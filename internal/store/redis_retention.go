package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"claude_bot/internal/model"

	"github.com/redis/go-redis/v9"
)

func (s *RedisFactStore) Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error) {
	key := s.targetKey(target)
	var removed int
	err := s.watchFactKeys(ctx, func(tx *redis.Tx) error {
		entries, err := tx.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		var hashes []string
		for hash, data := range entries {
			var fact model.Fact
			if err := json.Unmarshal([]byte(data), &fact); err != nil {
				return fmt.Errorf("failed to decode fact %s for target %s: %w", hash, target, err)
			}
			if filter(fact) {
				hashes = append(hashes, hash)
			}
		}
		if len(hashes) == 0 {
			return nil
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			s.queueRemoval(ctx, pipe, target, hashes)
			return nil
		})
		removed = len(hashes)
		return err
	}, key)
	if err != nil {
		return 0, fmt.Errorf("failed to delete facts: %w", err)
	}
	return removed, nil
}

func (s *RedisFactStore) EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error) {
	timelineKey := s.prefix + TimelineKey
	var removed int
	err := s.watchFactKeys(ctx, func(tx *redis.Tx) error {
		count, err := tx.ZCard(ctx, timelineKey).Result()
		if err != nil || count <= int64(max(0, maxFacts)) {
			return err
		}
		members, err := tx.ZRange(ctx, timelineKey, 0, count-int64(max(0, maxFacts))-1).Result()
		if err != nil || len(members) == 0 {
			return err
		}

		hashesByTarget := make(map[string][]string)
		timelineMembers := make([]any, 0, len(members))
		for _, member := range members {
			timelineMembers = append(timelineMembers, member)
			if target, hash, ok := splitTimelineMember(member); ok {
				hashesByTarget[target] = append(hashesByTarget[target], hash)
			}
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.ZRem(ctx, timelineKey, timelineMembers...)
			for target, hashes := range hashesByTarget {
				pipe.HDel(ctx, s.targetKey(target), hashes...)
			}
			return nil
		})
		removed = len(members)
		return err
	}, timelineKey)
	if err != nil {
		return 0, fmt.Errorf("failed to enforce the fact limit: %w", err)
	}
	return removed, nil
}

func (s *RedisFactStore) watchFactKeys(ctx context.Context, update func(*redis.Tx) error, keys ...string) error {
	var err error
	for range 5 {
		err = s.client.Watch(ctx, update, keys...)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return err
}
