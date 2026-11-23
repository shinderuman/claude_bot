package facttool

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"claude_bot/internal/cli"
	"claude_bot/internal/config"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
	"claude_bot/internal/util"
)

func Migrate(args []string) error {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	envFile := flags.String("env", "", "Path to the instance environment file (required)")
	factsFile := flags.String("facts", "", "Path to facts.json (required)")
	if err := cli.Parse(flags, args); err != nil {
		return err
	}
	if err := config.LoadEnvironment(*envFile); err != nil {
		return err
	}

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
	facts, err := util.ReadJSONArray[model.Fact](*factsFile)
	if err != nil {
		return err
	}
	log.Printf("Loaded %d facts from JSON.", len(facts))

	redisStore, err := store.NewRedisFactStore(redisURL, prefix)
	if err != nil {
		return err
	}
	defer redisStore.Close()

	log.Printf("Migrating facts to Redis (prefix: %s)...", prefix)
	start := time.Now()
	if err := redisStore.ReplaceAllFacts(context.Background(), facts); err != nil {
		return err
	}
	log.Printf("Migration completed in %v. Success: %d/%d.", time.Since(start), len(facts), len(facts))
	return nil
}
