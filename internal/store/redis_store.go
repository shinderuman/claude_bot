package store

import (
	"claude_bot/internal/model"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	RedisPrefix = "claude_bot:facts"
	TimelineKey = ":timeline"
	TargetsKey  = ":targets"
)

type RedisFactStore struct {
	client *redis.Client
	prefix string
}

func NewRedisFactStore(url, prefix string) (*RedisFactStore, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	if prefix == "" {
		prefix = RedisPrefix
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisFactStore{
		client: client,
		prefix: prefix,
	}, nil
}

func computeFactHash(fact model.Fact) string {
	valStr := fmt.Sprintf("%v", fact.Value)
	input := fact.Key + ":" + valStr
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}

func (s *RedisFactStore) Add(ctx context.Context, fact model.Fact) error {
	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now()
	}

	factHash := computeFactHash(fact)
	factJSON, err := json.Marshal(fact)
	if err != nil {
		return fmt.Errorf("failed to marshal fact: %w", err)
	}

	targetKey := fmt.Sprintf("%s:%s", s.prefix, fact.Target)
	memberID := fmt.Sprintf("%s:%s", fact.Target, factHash)

	pipe := s.client.Pipeline()

	pipe.HSet(ctx, targetKey, factHash, factJSON)

	timelineKey := s.prefix + TimelineKey
	pipe.ZAdd(ctx, timelineKey, redis.Z{
		Score:  float64(fact.Timestamp.UnixNano()),
		Member: memberID,
	})

	targetsKey := s.prefix + TargetsKey
	pipe.SAdd(ctx, targetsKey, fact.Target)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute redis pipeline: %w", err)
	}

	return nil
}

func (s *RedisFactStore) GetByTarget(ctx context.Context, target string) ([]model.Fact, error) {
	targetKey := fmt.Sprintf("%s:%s", s.prefix, target)

	valMap, err := s.client.HGetAll(ctx, targetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get facts for target %s: %w", target, err)
	}

	facts := make([]model.Fact, 0, len(valMap))
	for _, data := range valMap {
		var f model.Fact
		if err := json.Unmarshal([]byte(data), &f); err != nil {
			continue
		}
		facts = append(facts, f)
	}

	return facts, nil
}

func (s *RedisFactStore) GetRecent(ctx context.Context, limit int) ([]model.Fact, error) {
	timelineKey := s.prefix + TimelineKey
	members, err := s.client.ZRevRange(ctx, timelineKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline: %w", err)
	}

	if len(members) == 0 {
		return []model.Fact{}, nil
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(members))

	for i, member := range members {
		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			cmds[i] = nil
			continue
		}
		target, hash := parts[0], parts[1]
		targetKey := fmt.Sprintf("%s:%s", s.prefix, target)
		cmds[i] = pipe.HGet(ctx, targetKey, hash)
	}

	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
	}

	facts := make([]model.Fact, 0, len(members))
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		data, err := cmd.Result()
		if err != nil {
			continue
		}

		var f model.Fact
		if err := json.Unmarshal([]byte(data), &f); err == nil {
			facts = append(facts, f)
		}
	}

	return facts, nil
}

func (s *RedisFactStore) SearchFuzzy(ctx context.Context, targets []string, keys []string) ([]model.Fact, error) {
	var candidateFacts []model.Fact

	candidateFacts, err := s.GetAllFacts(ctx)
	if err != nil {
		return nil, err
	}

	var results []model.Fact
	for _, fact := range candidateFacts {
		targetMatch := false
		if len(targets) == 0 {
			targetMatch = true
		} else {
			for _, t := range targets {
				if fact.Target == t || fact.TargetUserName == t {
					targetMatch = true
					break
				}
				if len(t) >= MinTargetUserNameFuzzyLength {
					if strings.HasPrefix(fact.TargetUserName, t) || strings.HasSuffix(fact.TargetUserName, t) {
						targetMatch = true
						break
					}
				}
			}
		}

		if !targetMatch {
			continue
		}

		if len(keys) == 0 {
			results = append(results, fact)
			continue
		}

		for _, key := range keys {
			if strings.Contains(fact.Key, key) || strings.Contains(key, fact.Key) {
				results = append(results, fact)
				break
			}
			if strings.HasPrefix(fact.Key, model.SystemFactKeyPrefix) {
				valStr := fmt.Sprintf("%v", fact.Value)
				if strings.Contains(valStr, key) {
					results = append(results, fact)
					break
				}
			}
		}
	}

	return results, nil
}

func (s *RedisFactStore) Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error) {
	facts, err := s.GetByTarget(ctx, target)
	if err != nil {
		return 0, err
	}

	var toRemove []string
	deletedCount := 0

	for _, f := range facts {
		if filter(f) {
			toRemove = append(toRemove, computeFactHash(f))
			deletedCount++
		}
	}

	if len(toRemove) == 0 {
		return 0, nil
	}

	targetKey := fmt.Sprintf("%s:%s", s.prefix, target)
	pipe := s.client.Pipeline()

	pipe.HDel(ctx, targetKey, toRemove...)

	var timelineMembers []interface{}
	for _, hash := range toRemove {
		timelineMembers = append(timelineMembers, fmt.Sprintf("%s:%s", target, hash))
	}
	timelineKey := s.prefix + TimelineKey
	pipe.ZRem(ctx, timelineKey, timelineMembers...)

	// Retain empty targets to avoid racing a concurrent insert.

	_, err = pipe.Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to delete facts: %w", err)
	}

	return deletedCount, nil
}

func (s *RedisFactStore) Replace(ctx context.Context, target string, remove []model.Fact, add []model.Fact) error {
	var toRemove []string
	for _, f := range remove {
		toRemove = append(toRemove, computeFactHash(f))
	}

	pipe := s.client.Pipeline()
	targetKey := fmt.Sprintf("%s:%s", s.prefix, target)

	if len(toRemove) > 0 {
		pipe.HDel(ctx, targetKey, toRemove...)
		var timelineMembers []interface{}
		for _, hash := range toRemove {
			timelineMembers = append(timelineMembers, fmt.Sprintf("%s:%s", target, hash))
		}
		timelineKey := s.prefix + TimelineKey
		pipe.ZRem(ctx, timelineKey, timelineMembers...)
	}

	for _, f := range add {
		if f.Timestamp.IsZero() {
			f.Timestamp = time.Now()
		}
		factHash := computeFactHash(f)
		factJSON, err := json.Marshal(f)
		if err != nil {
			return fmt.Errorf("failed to marshal fact: %w", err)
		}
		memberID := fmt.Sprintf("%s:%s", f.Target, factHash)

		pipe.HSet(ctx, targetKey, factHash, factJSON)

		timelineKey := s.prefix + TimelineKey
		pipe.ZAdd(ctx, timelineKey, redis.Z{
			Score:  float64(f.Timestamp.UnixNano()),
			Member: memberID,
		})
		targetsKey := s.prefix + TargetsKey
		pipe.SAdd(ctx, targetsKey, f.Target)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute replace pipeline: %w", err)
	}
	return nil
}

func (s *RedisFactStore) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	targetsKey := s.prefix + TargetsKey
	targets, err := s.client.SMembers(ctx, targetsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get targets: %w", err)
	}

	var allFacts []model.Fact

	for _, target := range targets {
		facts, err := s.GetByTarget(ctx, target)
		if err != nil {
			continue
		}
		allFacts = append(allFacts, facts...)
	}

	return allFacts, nil
}

func (s *RedisFactStore) EnforceMaxFacts(ctx context.Context, maxFacts int) (int, error) {
	timelineKey := s.prefix + TimelineKey
	count, err := s.client.ZCard(ctx, timelineKey).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get timeline count: %w", err)
	}

	if int(count) <= maxFacts {
		return 0, nil
	}

	toRemoveCount := int(count) - maxFacts
	members, err := s.client.ZRange(ctx, timelineKey, 0, int64(toRemoveCount-1)).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get oldest facts: %w", err)
	}

	if len(members) == 0 {
		return 0, nil
	}

	targetHashes := make(map[string][]string)
	for _, member := range members {
		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			continue
		}
		target, hash := parts[0], parts[1]
		targetHashes[target] = append(targetHashes[target], hash)
	}

	pipe := s.client.Pipeline()

	pipe.ZRemRangeByRank(ctx, timelineKey, 0, int64(toRemoveCount-1))

	for target, hashes := range targetHashes {
		targetKey := fmt.Sprintf("%s:%s", s.prefix, target)
		pipe.HDel(ctx, targetKey, hashes...)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to execute enforce max facts pipeline: %w", err)
	}

	return len(members), nil
}

func (s *RedisFactStore) Close() error {
	return s.client.Close()
}
