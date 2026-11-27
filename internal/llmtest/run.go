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

var errEmptyReply = errors.New("LLMからの応答がありません")

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
	envPath := flags.Arg(0)
	if *mode == "profile" {
		cfg, err := config.LoadProfileTestConfig(envPath)
		if err != nil {
			return err
		}
		return runProfileUpdate(cfg)
	}

	if os.Getenv("RUN_MAINTENANCE") == "" {
		if err := os.Setenv("RUN_MAINTENANCE", "false"); err != nil {
			return err
		}
	}
	cfg, err := config.LoadLLMTestConfig(envPath)
	if err != nil {
		return err
	}
	log.Printf("設定ファイルを読み込みました: %s", envPath)
	if cfg.LLMProvider == config.LLMProviderOpenAI {
		if *mode == "auto-post" {
			return errors.New("ChatGPTプランではBot用のファクトを使う auto-post モードを実行できません")
		}
	}
	printConfig(cfg)
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		return err
	}

	switch *mode {
	case "response":
		var factService *facts.FactService
		if cfg.EnableFactStore {
			factsFile := "data/facts.json"
			if *testFacts {
				factsFile = "data/facts_test.json"
				log.Printf("テストモード: %s を使用します", factsFile)
			}
			factStore, err := loadMemoryFacts(factsFile)
			if err != nil {
				return err
			}
			factService = facts.NewFactService(cfg, factStore, llmClient, nil, nil, nil)
		}
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
		return runGenerateImage(cfg, image.NewImageGenerator(cfg, llmClient), *message)
	case "error":
		return runErrorMessageGeneration(cfg, llmClient, *message)
	default:
		return fmt.Errorf("不明なモード: %s (使用可能: response, summary, fact, raw-image, auto-post, conversation, generate-image, error, profile)", *mode)
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
