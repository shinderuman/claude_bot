package facttool

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

func Migrate(args []string) error {
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	envFile := flags.String("env", "", "Path to .env file")
	factsFile := flags.String("facts", "", "Path to facts.json (required)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config.LoadEnvironment(*envFile)

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		return errors.New("REDIS_URL is not set in environment variables.")
	}
	prefix := os.Getenv("REDIS_FACTS_KEY")
	if prefix == "" {
		return errors.New("REDIS_FACTS_KEY is not set in environment variables.")
	}
	if *factsFile == "" {
		return errors.New("-facts flag is required.")
	}

	log.Printf("Loading facts from %s...", *factsFile)
	facts, err := ReadFile[model.Fact](*factsFile)
	if err != nil {
		return err
	}
	log.Printf("Loaded %d facts from JSON.", len(facts))

	redisStore, err := store.NewRedisFactStore(redisURL, prefix)
	if err != nil {
		return err
	}
	defer redisStore.Close()

	ctx := context.Background()
	log.Printf("Cleaning up existing Redis data (prefix: %s)...", prefix)
	deleted, err := redisStore.DeleteAllFacts(ctx)
	if err != nil {
		return err
	}
	log.Printf("Deleted %d fact hash keys and global sets.", deleted)

	log.Println("Migrating facts to Redis...")
	start := time.Now()
	successCount := 0
	for i, fact := range facts {
		if i > 0 && i%500 == 0 {
			log.Printf("Processed %d/%d facts...", i, len(facts))
		}
		if err := redisStore.Add(ctx, fact); err != nil {
			log.Printf("Failed to add fact %d: %v", i, err)
		} else {
			successCount++
		}
	}
	log.Printf("Migration completed in %v. Success: %d/%d.", time.Since(start), successCount, len(facts))
	return nil
}
