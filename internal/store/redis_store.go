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
	RedisPrefix = "claude_bot:facts" // default fallback
	TimelineKey = ":timeline"
	TargetsKey  = ":targets"
)

// RedisFactStore implements FactStorage using Redis
type RedisFactStore struct {
	client *redis.Client
	prefix string
}

// NewRedisFactStore creates a new RedisFactStore
func NewRedisFactStore(url, prefix string) (*RedisFactStore, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	if prefix == "" {
		prefix = RedisPrefix
	}

	client := redis.NewClient(opts)

	// Verify connection
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

// computeFactHash generates a unique hash for a fact's content
// Uniqueness: Key + Value (Target is part of the Redis Key structure)
func computeFactHash(fact model.Fact) string {
	valStr := fmt.Sprintf("%v", fact.Value)
	input := fact.Key + ":" + valStr
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}

// Add adds a new fact or updates an existing one
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

	// 1. Store Fact in Hash
	pipe.HSet(ctx, targetKey, factHash, factJSON)

	// 2. Add to Timeline (Global)
	timelineKey := s.prefix + TimelineKey
	pipe.ZAdd(ctx, timelineKey, redis.Z{
		Score:  float64(fact.Timestamp.UnixNano()),
		Member: memberID,
	})

	// 3. Add to Targets Set
	targetsKey := s.prefix + TargetsKey
	pipe.SAdd(ctx, targetsKey, fact.Target)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to execute redis pipeline: %w", err)
	}

	return nil
}

// GetByTarget returns all facts for a specific target
func (s *RedisFactStore) GetByTarget(ctx context.Context, target string) ([]model.Fact, error) {
	targetKey := fmt.Sprintf("%s:%s", s.prefix, target)

	// Get all fields from Hash
	valMap, err := s.client.HGetAll(ctx, targetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get facts for target %s: %w", target, err)
	}

	facts := make([]model.Fact, 0, len(valMap))
	for _, data := range valMap {
		var f model.Fact
		if err := json.Unmarshal([]byte(data), &f); err != nil {
			// Skip corrupted data or log?
			continue
		}
		facts = append(facts, f)
	}

	return facts, nil
}

// GetRecent returns the most recent n facts
func (s *RedisFactStore) GetRecent(ctx context.Context, limit int) ([]model.Fact, error) {
	// Get recent members from timeline
	timelineKey := s.prefix + TimelineKey
	members, err := s.client.ZRevRange(ctx, timelineKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get timeline: %w", err)
	}

	if len(members) == 0 {
		return []model.Fact{}, nil
	}

	// Fetch facts from Hashes
	// Pipeline HGETs
	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringCmd, len(members))

	for i, member := range members {
		// member format: "{target}:{factHash}"
		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			cmds[i] = nil // Invalid format
			continue
		}
		target, hash := parts[0], parts[1]
		targetKey := fmt.Sprintf("%s:%s", s.prefix, target)
		cmds[i] = pipe.HGet(ctx, targetKey, hash)
	}

	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil { // allow some misses
		// Inspect results for failures
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

// SearchFuzzy searches facts based on targets and keys
// Redis does not support efficient partial JSON matching, so we scan facts.
func (s *RedisFactStore) SearchFuzzy(ctx context.Context, targets []string, keys []string) ([]model.Fact, error) {
	var candidateFacts []model.Fact

	// Global search required for fuzzy matching on TargetUserName or Value content in Key/Value
	// Since targets arg can contain UserNames (not just IDs), we cannot strictly filter by Redis Key.
	// We scan all facts.
	candidateFacts, err := s.GetAllFacts(ctx)
	if err != nil {
		return nil, err
	}

	// Filter in memory (same logic as MemoryStore)
	var results []model.Fact
	for _, fact := range candidateFacts {
		// Target checking
		targetMatch := false
		if len(targets) == 0 {
			targetMatch = true // No target constraint
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
			if strings.HasPrefix(fact.Key, "system:") {
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

// Remove removes facts based on a filter function
func (s *RedisFactStore) Remove(ctx context.Context, target string, filter func(model.Fact) bool) (int, error) {
	// 1. Get All Facts for Target
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

	// 2. Delete from Redis
	targetKey := fmt.Sprintf("%s:%s", s.prefix, target)
	pipe := s.client.Pipeline()

	// Del from Hash
	pipe.HDel(ctx, targetKey, toRemove...)

	// Del from Timeline
	var timelineMembers []interface{}
	for _, hash := range toRemove {
		timelineMembers = append(timelineMembers, fmt.Sprintf("%s:%s", target, hash))
	}
	timelineKey := s.prefix + TimelineKey
	pipe.ZRem(ctx, timelineKey, timelineMembers...)

	// We don't remove from TargetsKey set to avoid race conditions.
	// Dead targets are acceptable.

	_, err = pipe.Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to delete facts: %w", err)
	}

	return deletedCount, nil
}

// Replace replaces specific facts for a target atomically
func (s *RedisFactStore) Replace(ctx context.Context, target string, remove []model.Fact, add []model.Fact) error {
	var toRemove []string
	for _, f := range remove {
		toRemove = append(toRemove, computeFactHash(f))
	}

	pipe := s.client.Pipeline()
	targetKey := fmt.Sprintf("%s:%s", s.prefix, target)

	// 1. Remove
	if len(toRemove) > 0 {
		pipe.HDel(ctx, targetKey, toRemove...)
		// Also remove from timeline
		var timelineMembers []interface{}
		for _, hash := range toRemove {
			timelineMembers = append(timelineMembers, fmt.Sprintf("%s:%s", target, hash))
		}
		timelineKey := s.prefix + TimelineKey
		pipe.ZRem(ctx, timelineKey, timelineMembers...)
	}

	// 2. Add
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

// GetAllFacts returns all facts (for backup/migration)
func (s *RedisFactStore) GetAllFacts(ctx context.Context) ([]model.Fact, error) {
	// 1. Get all targets
	targetsKey := s.prefix + TargetsKey
	targets, err := s.client.SMembers(ctx, targetsKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get targets: %w", err)
	}

	var allFacts []model.Fact

	// 2. Iterate targets
	for _, target := range targets {
		facts, err := s.GetByTarget(ctx, target)
		if err != nil {
			// Log and continue?
			continue
		}
		allFacts = append(allFacts, facts...)
	}

	return allFacts, nil
}

// EnforceMaxFacts keeps only the most recent maxFacts facts, removing older ones
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
	// Get oldest members to remove
	members, err := s.client.ZRange(ctx, timelineKey, 0, int64(toRemoveCount-1)).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get oldest facts: %w", err)
	}

	if len(members) == 0 {
		return 0, nil
	}

	// Prepare removals from Hashes
	targetHashes := make(map[string][]string)
	for _, member := range members {
		// member format: "{target}:{factHash}"
		parts := strings.SplitN(member, ":", 2)
		if len(parts) != 2 {
			continue
		}
		target, hash := parts[0], parts[1]
		targetHashes[target] = append(targetHashes[target], hash)
	}

	pipe := s.client.Pipeline()

	// 1. Remove from Timeline
	pipe.ZRemRangeByRank(ctx, timelineKey, 0, int64(toRemoveCount-1))

	// 2. Remove from Hashes
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

// Close cleans up resources
func (s *RedisFactStore) Close() error {
	return s.client.Close()
}
