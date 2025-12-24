package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/image"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"claude_bot/internal/store"
)

func main() {
	mode := flag.String("mode", "response", "テストモード: response, summary, fact, profile")
	message := flag.String("message", "Hello", "テストメッセージ")
	imagePath := flag.String("image", "", "画像ファイルパス (responseモード用)")
	existingSummary := flag.String("existing-summary", "", "既存の要約（summaryモード用）")
	testFacts := flag.Bool("test-facts", false, "テスト用facts.jsonを使用（data/facts_test.json）")
	flag.Parse()

	// テスト用: .envを読み込む（引数で指定またはデフォルト）
	var envPath string
	if flag.NArg() > 0 {
		envPath = flag.Arg(0)
	}

	// 環境変数の読み込み（共通設定ファイル .env の読み込みも含む）
	config.LoadEnvironment(envPath)
	if envPath != "" {
		log.Printf("設定ファイルを読み込みました: %s", envPath)
	} else {
		log.Printf("デフォルト設定ファイルを読み込みました")
	}

	// config.LoadConfig()内で環境変数は参照される

	cfg := config.LoadConfig()

	// 設定情報を出力
	printConfig(cfg)

	llmClient := llm.NewClient(cfg)

	// ファクトストア初期化（テストモードなら別ファイル）
	factsFile := "data/facts.json"
	if *testFacts {
		factsFile = "data/facts_test.json"
		log.Printf("テストモード: %s を使用します", factsFile)
	}
	factStore := store.NewFactStore(factsFile, slack.NewClient("", "", "", ""))
	factService := facts.NewFactService(cfg, factStore, llmClient, nil, nil)

	switch *mode {
	case "response":
		testResponse(cfg, llmClient, factService, *message, *imagePath)
	case "summary":
		testSummary(cfg, llmClient, *message, *existingSummary)
	case "fact":
		testFactExtraction(cfg, llmClient, *message)
	case "raw-image":
		testRawImage(cfg, llmClient, *message, *imagePath)
	case "auto-post":
		testAutoPost(cfg, llmClient)
	case "conversation":
		testConversation(cfg, llmClient, *message)
	case "generate-image":
		imageGen := image.NewImageGenerator(cfg, llmClient)
		testGenerateImage(imageGen, *message)
	case "error":
		testErrorMessageGeneration(cfg, llmClient, *message)
	case "profile":
		testProfileUpdate(cfg, factService)
	default:
		log.Fatalf("不明なモード: %s (使用可能: response, summary, fact, raw-image, auto-post, conversation, generate-image, error)", *mode)
	}
}

func printConfig(cfg *config.Config) {
	log.Println("=== 設定情報 ===")
	log.Printf("Mastodonサーバー: %s", cfg.MastodonServer)
	log.Printf("Botユーザー名: @%s", cfg.BotUsername)
	log.Printf("リモートユーザー許可: %t", cfg.AllowRemoteUsers)
	log.Printf("事実ストア有効: %t", cfg.EnableFactStore)
	log.Printf("画像認識有効: %t", cfg.EnableImageRecognition)
	log.Println()
	log.Println("=== 会話管理設定 ===")
	log.Printf("メッセージ圧縮しきい値: %d件", cfg.ConversationMessageCompressThreshold)
	log.Printf("保持メッセージ数: %d件", cfg.ConversationMessageKeepCount)
	log.Printf("アイドル時間: %d時間", cfg.ConversationIdleHours)
	log.Printf("保持時間: %d時間", cfg.ConversationRetentionHours)
	log.Printf("最小保持数: %d件", cfg.ConversationMinKeepCount)
	log.Println()
	log.Println("=== LLM & 投稿設定 ===")
	log.Printf("プロバイダー: %s", cfg.LLMProvider)
	switch cfg.LLMProvider {
	case "claude":
		log.Printf("Claude API: %s", cfg.AnthropicBaseURL)
		log.Printf("Claudeモデル: %s", cfg.AnthropicModel)
	case "gemini":
		log.Printf("Geminiモデル: %s", cfg.GeminiModel)
	}
	log.Printf("最大応答トークン: %d", cfg.MaxResponseTokens)
	log.Printf("最大要約トークン: %d", cfg.MaxSummaryTokens)
	log.Printf("最大ファクトトークン: %d", cfg.MaxFactTokens)
	log.Printf("最大画像生成トークン: %d", cfg.MaxImageTokens)
	log.Printf("最大投稿文字数: %d", cfg.MaxPostChars)
	log.Println()
	log.Println("=== ファクト収集設定 ===")
	log.Printf("ファクト収集有効: %t", cfg.FactCollectionEnabled)
	if cfg.FactCollectionEnabled {
		log.Printf("連合タイムライン: %t", cfg.FactCollectionFederated)
		log.Printf("ホームタイムライン: %t", cfg.FactCollectionHome)
		log.Printf("投稿本文から収集: %t", cfg.FactCollectionFromPostContent)
		log.Printf("最大並列処理数: %d", cfg.FactCollectionMaxWorkers)
		log.Printf("1時間あたり最大処理数: %d", cfg.FactCollectionMaxPerHour)
	}
	log.Println()
	log.Println("==================")
	log.Println()
}

func testResponse(cfg *config.Config, client *llm.Client, factService *facts.FactService, message, imagePath string) {
	log.Printf("=== 通常応答テスト ===")
	log.Printf("テストメッセージ: %s", message)
	if imagePath != "" {
		log.Printf("画像ファイル: %s", imagePath)
	}
	log.Println()

	if cfg.LLMProvider == "claude" && cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}
	if cfg.LLMProvider == "gemini" && cfg.GeminiAPIKey == "" {
		log.Fatal("エラー: GEMINI_API_KEY環境変数が設定されていません")
	}

	// テスト用セッション作成
	// テストユーザー: asmodeus (facts.jsonにデータがあるユーザー)
	testUser := "asmodeus"
	testUserName := "グレートマグマカッター"

	session := &model.Session{
		Conversations: []model.Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
	}
	conversation := &model.Conversation{
		RootStatusID: "test",
		CreatedAt:    time.Now(),
		LastUpdated:  time.Now(),
		Messages:     []model.Message{{Role: "user", Content: message}},
	}
	session.Conversations = append(session.Conversations, *conversation)

	var currentImages []model.Image
	if imagePath != "" {
		img, err := loadImage(imagePath)
		if err != nil {
			log.Fatalf("画像読み込みエラー: %v", err)
		}
		currentImages = append(currentImages, *img)
	}

	ctx := context.Background()

	// 事実検索
	log.Println("事実を検索中...")
	relevantFacts := factService.QueryRelevantFacts(ctx, testUser, testUserName, message)
	if relevantFacts != "" {
		log.Println("--- 関連する事実 ---")
		log.Println(relevantFacts)
		log.Println("------------------")
	} else {
		log.Println("関連する事実は見つかりませんでした")
	}

	response := client.GenerateResponse(ctx, session, conversation, relevantFacts, "", currentImages)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")

	// ファクト抽出と保存
	if cfg.EnableFactStore {
		log.Println()
		log.Println("ファクトを抽出中...")
		factService.ExtractAndSaveFacts(ctx, "test_source_id", "user", "TestUser", message, model.SourceTypeTest, "", "test_post_author", "TestPostUserName")
		log.Println("ファクト抽出完了")
	}
}

func testSummary(cfg *config.Config, client *llm.Client, newMessages, existingSummary string) {
	log.Printf("=== 要約生成テスト ===")
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// 新しいメッセージをフォーマット
	formattedMessages := newMessages

	// 要約プロンプトを構築
	summaryPrompt := llm.BuildSummaryPrompt(formattedMessages, existingSummary)

	log.Println("--- 要約プロンプト ---")
	log.Println(summaryPrompt)
	log.Println("--------------------")
	log.Println()

	// 要約生成
	messages := []model.Message{{Role: "user", Content: summaryPrompt}}
	ctx := context.Background()
	summary := client.GenerateSummary(ctx, messages, existingSummary)

	if summary == "" {
		log.Fatal("エラー: 要約生成に失敗しました")
	}

	log.Println("成功: 要約を生成しました")
	log.Println()
	log.Println("--- 生成された要約 ---")
	log.Println(summary)
	log.Println("---------------------")
	log.Printf("要約の長さ: %d文字", len(summary))
}

func testFactExtraction(cfg *config.Config, client *llm.Client, message string) {
	log.Printf("=== 事実抽出テスト ===")
	log.Printf("テストメッセージ: %s", message)
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// 事実抽出プロンプトを構築
	authorUserName := "testuser"
	author := "testuser@example.com"
	prompt := llm.BuildFactExtractionPrompt(authorUserName, author, message)

	log.Println("--- 事実抽出プロンプト ---")
	log.Println(prompt)
	log.Println("------------------------")
	log.Println()

	// 事実抽出
	messages := []model.Message{{Role: "user", Content: prompt}}
	ctx := context.Background()
	response := client.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, cfg.MaxResponseTokens, nil, llm.TemperatureSystem)

	if response == "" {
		log.Fatal("エラー: 事実抽出に失敗しました")
	}

	log.Println("成功: 事実を抽出しました")
	log.Println()
	log.Println("--- 抽出された事実 (JSON) ---")
	log.Println(response)
	log.Println("----------------------------")
}

func testRawImage(cfg *config.Config, client *llm.Client, message, imagePath string) {
	log.Printf("=== 画像認識テスト（最小プロンプト） ===")
	log.Printf("テストメッセージ: %s", message)
	if imagePath == "" {
		log.Fatal("エラー: 画像ファイルパスが必要です (-image オプション)")
	}
	log.Printf("画像ファイル: %s", imagePath)
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// 画像読み込み
	img, err := loadImage(imagePath)
	if err != nil {
		log.Fatalf("画像読み込みエラー: %v", err)
	}
	currentImages := []model.Image{*img}

	// メッセージ作成
	messages := []model.Message{{Role: "user", Content: message}}

	// API呼び出し（システムプロンプトなし）
	ctx := context.Background()
	response := client.GenerateText(ctx, messages, "", cfg.MaxResponseTokens, currentImages, cfg.LLMTemperature)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")
}

func testAutoPost(cfg *config.Config, client *llm.Client) {
	log.Printf("=== 自動投稿テスト ===")
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// Storeの初期化
	_ = store.InitializeHistory(cfg)
	factStore := store.InitializeFactStore(cfg, slack.NewClient(cfg.SlackBotToken, cfg.SlackChannelID, cfg.SlackErrorChannelID, cfg.BotUsername))

	// ファクトバンドル取得
	facts, err := factStore.GetRandomGeneralFactBundle(5)
	if err != nil {
		log.Fatalf("ファクト取得エラー: %v", err)
	}
	if len(facts) == 0 {
		log.Fatal("エラー: 一般知識のファクトが見つかりません。facts.jsonを確認してください。")
	}

	log.Printf("取得したファクト数: %d件", len(facts))
	log.Printf("情報源: %s", facts[0].TargetUserName)
	for _, f := range facts {
		log.Printf("- %s: %v", f.Key, f.Value)
	}
	log.Println()

	// プロンプト作成
	prompt := llm.BuildAutoPostPrompt(facts)
	log.Println("--- 生成されたプロンプト ---")
	log.Println(prompt)
	log.Println("--------------------------")
	log.Println()

	// システムプロンプト（キャラクター設定のみ）
	systemPrompt := llm.BuildSystemPrompt(cfg, "", "", "", true)

	// API呼び出し
	ctx := context.Background()
	response := client.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, int64(cfg.MaxPostChars), nil, cfg.LLMTemperature)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: 自動投稿文を生成しました")
	log.Println()
	log.Println("--- 生成された投稿文 ---")
	log.Println(response)
	log.Println("----------------------")
	log.Printf("文字数: %d文字", len([]rune(response)))
}

func loadImage(path string) (*model.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("not an image: %s", mimeType)
	}

	log.Printf("画像読み込み成功: MIMEタイプ=%s, サイズ=%d bytes", mimeType, len(data))

	return &model.Image{
		Data:      base64.StdEncoding.EncodeToString(data),
		MediaType: mimeType,
	}, nil
}

func testGenerateImage(imageGen *image.ImageGenerator, prompt string) {
	log.Printf("=== SVG画像生成テスト ===")
	log.Printf("プロンプト: %s", prompt)
	log.Println()

	ctx := context.Background()
	svg, err := imageGen.GenerateSVG(ctx, prompt)
	if err != nil {
		log.Fatalf("画像生成エラー: %v", err)
	}

	// SVGファイルとして保存
	filename := fmt.Sprintf("generated_image_%d.svg", time.Now().Unix())
	if err := imageGen.SaveSVGToFile(svg, filename); err != nil {
		log.Fatalf("ファイル保存エラー: %v", err)
	}

	log.Printf("成功: 画像を保存しました: %s", filename)
	log.Printf("ファイルサイズ: %d bytes", len(svg))

	// Base64エンコード版も保存
	encoded := base64.StdEncoding.EncodeToString([]byte(svg))
	base64Filename := fmt.Sprintf("generated_image_%d.base64.txt", time.Now().Unix())
	if err := os.WriteFile(base64Filename, []byte(encoded), 0644); err != nil {
		log.Printf("Base64ファイル保存エラー: %v", err)
	} else {
		log.Printf("Base64版も保存しました: %s", base64Filename)
	}
}

func testErrorMessageGeneration(cfg *config.Config, client *llm.Client, errorDetail string) {
	log.Printf("=== エラーメッセージ生成テスト ===")
	log.Printf("エラー詳細: %s", errorDetail)
	log.Println()

	if cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// プロンプト作成
	prompt := llm.BuildErrorMessagePrompt(errorDetail)
	log.Println("--- 生成されたプロンプト ---")
	log.Println(prompt)
	log.Println("--------------------------")
	log.Println()

	// システムプロンプト（キャラクター設定のみ）
	systemPrompt := llm.BuildSystemPrompt(cfg, "", "", "", true)

	// API呼び出し
	ctx := context.Background()
	response := client.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, cfg.MaxResponseTokens, nil, cfg.LLMTemperature)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: エラーメッセージを生成しました")
	log.Println()
	log.Println("--- 生成されたメッセージ ---")
	log.Println(response)
	log.Println("--------------------------")
}

func testConversation(cfg *config.Config, client *llm.Client, lastMessage string) {
	log.Printf("=== 会話履歴テスト ===")
	log.Println()

	if cfg.LLMProvider == "claude" && cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}
	if cfg.LLMProvider == "gemini" && cfg.GeminiAPIKey == "" {
		log.Fatal("エラー: GEMINI_API_KEY環境変数が設定されていません")
	}

	// 履歴の構築
	messages := []model.Message{
		{Role: "user", Content: "ねぇルナ、今日の晩御飯なにがいいと思う？"},
		{Role: "assistant", Content: "ふん、ご主人様ったら、そんなことも自分で決められないのですか？...まあ、今日は魔力が満ちているので、激辛麻婆豆腐なんていかがでしょう。深淵なる赤色が食欲をそそりますわよ。"},
		{Role: "user", Content: lastMessage},
	}

	log.Println("--- 会話履歴 ---")
	for _, msg := range messages {
		log.Printf("[%s]: %s", msg.Role, msg.Content)
	}
	log.Println("----------------")
	log.Println()

	// システムプロンプト（キャラクター設定 + 要約なし）
	systemPrompt := llm.BuildSystemPrompt(cfg, "", "", "", true)

	// API呼び出し
	ctx := context.Background()
	response := client.GenerateText(ctx, messages, systemPrompt, cfg.MaxResponseTokens, nil, cfg.LLMTemperature)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: 応答を受信しました")
	log.Println()
	log.Println("--- 応答 ---")
	log.Println(response)
	log.Println("------------------")
}

func testProfileUpdate(cfg *config.Config, factService *facts.FactService) {
	log.Println("=== プロフィール生成テスト（モックモード） ===")
	log.Println("現在のFactsから自己紹介文（Profile.txt）を生成し、モックサーバーへの更新を検証します。")
	log.Println()

	if cfg.LLMProvider == "claude" && cfg.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	// プロファイル生成による上書き防止のため、一時ファイルに出力先を変更
	// ユーザー要望により、生成されたファイルは確認後に削除する
	originalProfileFile := cfg.BotProfileFile
	tempProfileFile := fmt.Sprintf("Profile_TEST_%d.txt", time.Now().Unix())
	cfg.BotProfileFile = tempProfileFile
	defer func() {
		if err := os.Remove(tempProfileFile); err != nil {
			log.Printf("警告: テスト用プロファイルファイルの削除に失敗しました: %v", err)
		} else {
			log.Printf("テスト用プロファイルファイルを削除しました: %s", tempProfileFile)
		}
		cfg.BotProfileFile = originalProfileFile
	}()

	// 1. Mastodon APIのモックサーバーを起動
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[MockServer] Request Received: %s %s", r.Method, r.URL.Path)

		// 期待されるAPIエンドポイント
		if r.Method == "PATCH" && r.URL.Path == "/api/v1/accounts/update_credentials" {
			log.Println("[MockServer] プロフィール更新リクエスト受信成功")

			// リクエストボディの解析（必要に応じて検証）
			body, _ := io.ReadAll(r.Body)
			log.Printf("[MockServer] Body: %s", string(body))

			// 成功レスポンス（最小限のJSON）
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": "123456", "username": "bot", "note": "updated profile"}`)
			return
		}

		// ユーザー情報確認（verify_credentials）のモック
		if r.Method == "GET" && r.URL.Path == "/api/v1/accounts/verify_credentials" {
			log.Println("[MockServer] ユーザー情報確認リクエスト受信成功")
			w.Header().Set("Content-Type", "application/json")
			// Fieldsは空配列または適当な値を返す
			fmt.Fprintln(w, `{"id": "123456", "username": "bot", "display_name": "Test Bot", "fields": []}`)
			return
		}

		// ステータス投稿（トゥート）のモック
		if r.Method == "POST" && r.URL.Path == "/api/v1/statuses" {
			log.Println("[MockServer] ステータス投稿リクエスト受信成功")
			body, _ := io.ReadAll(r.Body)
			log.Printf("[MockServer] Body: %s", string(body))
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": "789012", "content": "status posted"}`)
			return
		}

		// その他のリクエストは404
		http.NotFound(w, r)
	}))
	defer ts.Close()
	log.Printf("[MockServer] Started at %s", ts.URL)

	// 2. モック用Mastodonクライアントの作成
	mockConfig := mastodon.Config{
		Server:           ts.URL,        // モックサーバーのURL
		AccessToken:      "dummy_token", // ダミー
		BotUsername:      "bot",
		AllowRemoteUsers: true,
	}
	mockClient := mastodon.NewClient(mockConfig)

	// 生成されたRawテキストを想定
	dummyNoteRaw := "これはテスト用のプロフィール本文です。\n\n整形前です。"
	dummyAuthKey := "dummy_auth_key"

	ctx := context.Background()

	formattedBody := mockClient.FormatProfileBody(dummyNoteRaw)
	safeBody := mockClient.TruncateToSafeProfileBody(formattedBody)

	log.Println("UpdateProfileWithFields呼び出し（シミュレーション）...")
	if err := mockClient.UpdateProfileWithFields(ctx, cfg, safeBody, dummyAuthKey); err != nil {
		log.Fatalf("UpdateProfileWithFieldsエラー: %v", err)
	}

	log.Println("PostStatus呼び出し（シミュレーション）...")
	if _, err := mockClient.PostStatus(ctx, safeBody, cfg.AutoPostVisibility); err != nil {
		log.Fatalf("PostStatusエラー: %v", err)
	}

	log.Println()
	log.Println("成功: プロフィール生成およびモックリクエスト処理が完了しました")

	// 生成されたファイルの内容を表示
	if cfg.BotProfileFile != "" {
		content, err := os.ReadFile(cfg.BotProfileFile)
		if err != nil {
			log.Printf("警告: 生成されたファイル(%s)の読み込みに失敗しました: %v", cfg.BotProfileFile, err)
			return
		}
		log.Println("--- 生成されたProfile.txt ---")
		log.Println(string(content))
		log.Println("-----------------------------")
	}
}
