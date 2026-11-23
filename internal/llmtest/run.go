package llmtest

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"claude_bot/internal/cli"
	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/image"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
	"claude_bot/internal/util"
)

var (
	errMissingAnthropicToken = errors.New("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	errEmptyClaudeReply      = errors.New("エラー: Claudeからの応答がありません")
)

func Run(args []string) error {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	mode := flags.String("mode", "response", "テストモード: response, summary, fact, profile")
	message := flags.String("message", "Hello", "テストメッセージ")
	imagePath := flags.String("image", "", "画像ファイルパス (responseモード用)")
	existingSummary := flags.String("existing-summary", "", "既存の要約（summaryモード用）")
	testFacts := flags.Bool("test-facts", false, "テスト用facts.jsonを使用（data/facts_test.json）")
	if err := cli.Parse(flags, args); err != nil {
		return err
	}

	if os.Getenv("RUN_MAINTENANCE") == "" {
		if err := os.Setenv("RUN_MAINTENANCE", "false"); err != nil {
			return err
		}
	}
	envPath := flags.Arg(0)
	if err := config.LoadEnvironment(envPath); err != nil {
		return err
	}
	if envPath != "" {
		log.Printf("設定ファイルを読み込みました: %s", envPath)
	} else {
		log.Println("デフォルト設定ファイルを読み込みました")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	printConfig(cfg)
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		return err
	}

	factsFile := "data/facts.json"
	if *testFacts {
		factsFile = "data/facts_test.json"
		log.Printf("テストモード: %s を使用します", factsFile)
	}
	factStore, err := loadMemoryFacts(factsFile)
	if err != nil {
		return err
	}
	factService := facts.NewFactService(cfg, factStore, llmClient, nil, nil, nil)

	switch *mode {
	case "response":
		return runResponse(cfg, llmClient, factService, *message, *imagePath)
	case "summary":
		return runSummary(cfg, llmClient, *message, *existingSummary)
	case "fact":
		return runFactExtraction(cfg, llmClient, *message)
	case "raw-image":
		return runRawImage(cfg, llmClient, *message, *imagePath)
	case "auto-post":
		return runAutoPost(cfg, llmClient)
	case "conversation":
		return runConversation(cfg, llmClient, *message)
	case "generate-image":
		return runGenerateImage(image.NewImageGenerator(cfg, llmClient), *message)
	case "error":
		return runErrorMessageGeneration(cfg, llmClient, *message)
	case "profile":
		return runProfileUpdate(cfg)
	default:
		return fmt.Errorf("不明なモード: %s (使用可能: response, summary, fact, raw-image, auto-post, conversation, generate-image, error)", *mode)
	}
}

func loadMemoryFacts(path string) (*store.FactStore, error) {
	memoryStore := store.NewMemoryFactStore()
	loadedFacts, err := util.ReadJSONArray[model.Fact](path)
	if errors.Is(err, os.ErrNotExist) {
		log.Printf("Facts file not found, starting with empty memory store: %s", path)
	} else if err != nil {
		return nil, err
	}
	ctx := context.Background()
	for _, fact := range loadedFacts {
		if err := memoryStore.Add(ctx, fact); err != nil {
			return nil, err
		}
	}
	return store.NewFactStore(memoryStore, path), nil
}
