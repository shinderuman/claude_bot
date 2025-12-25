package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

func main() {
	envFile := flag.String("env", "", "Path to .env file")
	factsFile := flag.String("facts", "", "Path to facts.json (required)")
	flag.Parse()

	// 1. Load Config
	config.LoadEnvironment(*envFile)
	// 1. Load Config (Environment only)
	config.LoadEnvironment(*envFile)

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		log.Fatal("REDIS_URL is not set in environment variables.")
	}

	redisFactsKey := os.Getenv("REDIS_FACTS_KEY")
	if redisFactsKey == "" {
		log.Fatal("REDIS_FACTS_KEY is not set in environment variables.")
	}

	// 2. Load facts.json
	filePath := *factsFile
	if filePath == "" {
		log.Fatal("-facts flag is required.")
	}

	log.Printf("Loading facts from %s...", filePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read facts file: %v", err)
	}

	var facts []model.Fact
	if err := json.Unmarshal(data, &facts); err != nil {
		log.Fatalf("Failed to parse facts JSON: %v", err)
	}
	log.Printf("Loaded %d facts from JSON.", len(facts))

	// 3. Connect to Redis (Raw client for cleanup)
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Invalid Redis URL: %v", err)
	}
	rdb := redis.NewClient(opt)
	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// 4. Cleanup old keys (Targeted deletion)
	prefix := redisFactsKey

	log.Printf("Cleaning up existing Redis data (prefix: %s)...", prefix)

	// Delete specific known keys first (Sets/ZSets)
	keysToDelete := []string{
		prefix + ":timeline",
		prefix + ":targets",
		prefix + ":fact_hashes",
	}
	rdb.Del(ctx, keysToDelete...).Err() // Ignore error if not exist

	// Scan and delete Hash keys
	scanMatch := prefix + ":*"
	var cursor uint64
	var n int
	for {
		var keys []string
		var err error
		keys, cursor, err = rdb.Scan(ctx, cursor, scanMatch, 100).Result()
		if err != nil {
			log.Fatalf("Scan failed: %v", err)
		}

		if len(keys) > 0 {
			rdb.Del(ctx, keys...)
			n += len(keys)
		}

		if cursor == 0 {
			break
		}
	}
	log.Printf("Deleted %d fact hash keys and global sets.", n)

	// 5. Initialize RedisFactStore for insertion
	redisStore, err := store.NewRedisFactStore(redisURL, redisFactsKey)
	if err != nil {
		log.Fatalf("Failed to create RedisFactStore: %v", err)
	}
	defer redisStore.Close()

	// 6. Insert Facts
	log.Println("Migrating facts to Redis...")
	start := time.Now()
	successCount := 0

	for i, f := range facts {
		if i > 0 && i%500 == 0 {
			log.Printf("Processed %d/%d facts...", i, len(facts))
		}
		if err := redisStore.Add(ctx, f); err != nil {
			log.Printf("Failed to add fact %d: %v", i, err)
		} else {
			successCount++
		}
	}

	duration := time.Since(start)
	log.Printf("Migration completed in %v. Success: %d/%d.", duration, successCount, len(facts))
}
