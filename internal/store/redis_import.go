package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"time"

	"claude_bot/internal/model"

	"github.com/redis/go-redis/v9"
)

var promoteFacts = redis.NewScript(`
local function matchingKeys(pattern)
    local keys, cursor = {}, "0"
    repeat
        local page = redis.call("SCAN", cursor, "MATCH", pattern, "COUNT", 1000)
        cursor = page[1]
        for _, key in ipairs(page[2]) do keys[key] = true end
    until cursor == "0"
    return keys
end

local staging, live = ARGV[1], ARGV[2]
if redis.call("ZCARD", staging .. ":timeline") ~= tonumber(ARGV[5]) then
    return redis.error_reply("staged facts count does not match input")
end
local staged = matchingKeys(ARGV[3])
local existing = matchingKeys(ARGV[4])
for key in pairs(existing) do redis.call("DEL", key) end
for key in pairs(staged) do
    local destination = live .. string.sub(key, #staging + 1)
    redis.call("RENAME", key, destination)
    redis.call("PERSIST", destination)
end
return 1
`)

func namespacePattern(prefix string) string {
	escapeGlob := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[")
	return escapeGlob.Replace(prefix) + ":*"
}

func (s *RedisFactStore) ReplaceAllFacts(ctx context.Context, facts []model.Fact) error {
	staging := &RedisFactStore{client: s.client, prefix: "claude-migrate-" + rand.Text(), expiration: 24 * time.Hour}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := staging.deleteAllFacts(cleanup); err != nil {
			log.Printf("移行用Redisデータの削除エラー: %v", err)
		}
	}()

	type factID struct{ target, hash string }
	unique := make(map[factID]struct{}, len(facts))
	for i, fact := range facts {
		if strings.TrimSpace(fact.Target) == "" || strings.TrimSpace(fact.Key) == "" || strings.TrimSpace(fact.Value) == "" {
			return fmt.Errorf("移行ファクト %d: target、key、value が必要です", i+1)
		}
		if err := staging.Add(ctx, fact); err != nil {
			return fmt.Errorf("移行ファクト %d の一時保存: %w", i+1, err)
		}
		unique[factID{target: fact.Target, hash: computeFactHash(fact)}] = struct{}{}
	}

	err := promoteFacts.Run(ctx, s.client, nil, staging.prefix, s.prefix,
		namespacePattern(staging.prefix), namespacePattern(s.prefix), strconv.Itoa(len(unique))).Err()
	if err != nil {
		return fmt.Errorf("Redisファクトの切り替え: %w", err)
	}
	return nil
}

func (s *RedisFactStore) deleteAllFacts(ctx context.Context) (int, error) {
	pattern := namespacePattern(s.prefix)
	var factKeys []string
	var cursor uint64
	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return 0, fmt.Errorf("Scan failed: %w", err)
		}
		factKeys = append(factKeys, keys...)
		if nextCursor == 0 {
			break
		}
		cursor = nextCursor
	}

	var deleted int
	for batch := range slices.Chunk(factKeys, 100) {
		count, err := s.client.Del(ctx, batch...).Result()
		if err != nil {
			return deleted, fmt.Errorf("failed to delete Redis fact keys: %w", err)
		}
		deleted += int(count)
	}
	return deleted, nil
}
