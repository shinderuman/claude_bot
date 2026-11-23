package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claude_bot/internal/model"

	"github.com/redis/go-redis/v9"
)

const (
	RedisPrefix = "claude_bot:facts"
	TimelineKey = ":timeline"
	TargetsKey  = ":targets"
)

type RedisFactStore struct {
	client     *redis.Client
	prefix     string
	expiration time.Duration
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
		client.Close()
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &RedisFactStore{
		client: client,
		prefix: prefix,
	}, nil
}

func computeFactHash(fact model.Fact) string {
	input := fact.Key + ":" + fact.Value
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash)
}

func (s *RedisFactStore) targetKey(target string) string {
	return s.prefix + ":" + target
}

func timelineMember(target, hash string) string {
	return target + ":" + hash
}

func splitTimelineMember(member string) (target, hash string, ok bool) {
	separator := strings.LastIndexByte(member, ':')
	if separator < 0 {
		return "", "", false
	}
	return member[:separator], member[separator+1:], true
}

func (s *RedisFactStore) Add(ctx context.Context, fact model.Fact) error {
	pipe := s.client.TxPipeline()
	if err := s.queueFact(ctx, pipe, fact); err != nil {
		return err
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to add fact: %w", err)
	}
	return nil
}

func (s *RedisFactStore) queueFact(ctx context.Context, pipe redis.Pipeliner, fact model.Fact) error {
	if fact.Timestamp.IsZero() {
		fact.Timestamp = time.Now()
	}

	key := s.targetKey(fact.Target)
	timelineKey := s.prefix + TimelineKey
	targetsKey := s.prefix + TargetsKey
	if key == timelineKey || key == targetsKey {
		return fmt.Errorf("fact target %q conflicts with a Redis index", fact.Target)
	}

	data, err := json.Marshal(fact)
	if err != nil {
		return fmt.Errorf("failed to marshal fact: %w", err)
	}
	hash := computeFactHash(fact)
	pipe.HSet(ctx, key, hash, data)
	pipe.ZAdd(ctx, timelineKey, redis.Z{
		Score:  float64(fact.Timestamp.UnixNano()),
		Member: timelineMember(fact.Target, hash),
	})
	pipe.SAdd(ctx, targetsKey, fact.Target)
	if s.expiration > 0 {
		for _, key := range []string{key, timelineKey, targetsKey} {
			pipe.Expire(ctx, key, s.expiration)
		}
	}
	return nil
}

func (s *RedisFactStore) Replace(ctx context.Context, target string, remove, add []model.Fact) error {
	pipe := s.client.TxPipeline()
	hashes := make([]string, 0, len(remove))
	for _, fact := range remove {
		hashes = append(hashes, computeFactHash(fact))
	}
	s.queueRemoval(ctx, pipe, target, hashes)
	for _, fact := range add {
		if err := s.queueFact(ctx, pipe, fact); err != nil {
			return err
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("failed to replace facts: %w", err)
	}
	return nil
}

func (s *RedisFactStore) queueRemoval(ctx context.Context, pipe redis.Pipeliner, target string, hashes []string) {
	if len(hashes) == 0 {
		return
	}
	members := make([]any, 0, len(hashes))
	for _, hash := range hashes {
		members = append(members, timelineMember(target, hash))
	}
	pipe.HDel(ctx, s.targetKey(target), hashes...)
	pipe.ZRem(ctx, s.prefix+TimelineKey, members...)
}

func (s *RedisFactStore) Close() error {
	return s.client.Close()
}
