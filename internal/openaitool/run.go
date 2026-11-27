package openaitool

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"claude_bot/internal/cli"
	"claude_bot/internal/config"
	"claude_bot/internal/siwc"
)

const (
	loginTimeout   = 5 * time.Minute
	requestTimeout = 60 * time.Second
)

func Run(args []string) error {
	if len(args) == 0 {
		return errors.New("使用方法: openai_auth <login|models> [オプション]")
	}
	command, commandArgs := args[0], args[1:]
	switch command {
	case "login":
		return runLogin(commandArgs)
	case "models":
		return runModels(commandArgs)
	default:
		return fmt.Errorf("不明なコマンド: %s (login, models)", command)
	}
}

func resolveCredentialPath(envPath, credentialPath string) (string, error) {
	if envPath != "" {
		if err := config.LoadInstanceEnvironment(envPath); err != nil {
			return "", err
		}
	}
	if credentialPath == "" {
		credentialPath = os.Getenv("OPENAI_CREDENTIAL_FILE")
	}
	if credentialPath == "" {
		return "", errors.New("認証情報ファイルのパスを -credential または OPENAI_CREDENTIAL_FILE で指定してください")
	}
	return siwc.ResolveCredentialPath(credentialPath)
}

func runLogin(args []string) error {
	flags := flag.NewFlagSet("openai_auth login", flag.ContinueOnError)
	envPath := flags.String("env", "", "環境設定ファイルのパス (省略時はプロセス環境変数を使用)")
	credentialPath := flags.String("credential", "", "認証情報ファイルのパス (絶対パス。省略時はOPENAI_CREDENTIAL_FILE)")
	if err := cli.Parse(flags, args); err != nil {
		return err
	}
	path, err := resolveCredentialPath(*envPath, *credentialPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	flow := siwc.NewLoginFlow(siwc.OfficialEndpoints(), siwc.NewHTTPClient(requestTimeout), siwc.DefaultPort)
	credential, err := flow.Run(ctx, path, func(authorizeURL string) {
		log.Printf("ブラウザで開く認可URL:\n%s", authorizeURL)
	})
	if err != nil {
		return err
	}
	log.Printf("ChatGPTプラン接続: %s", credential.Email)
	return nil
}

func runModels(args []string) error {
	flags := flag.NewFlagSet("openai_auth models", flag.ContinueOnError)
	envPath := flags.String("env", "", "環境設定ファイルのパス (省略時はプロセス環境変数を使用)")
	credentialPath := flags.String("credential", "", "認証情報ファイルのパス (絶対パス。省略時はOPENAI_CREDENTIAL_FILE)")
	if err := cli.Parse(flags, args); err != nil {
		return err
	}
	path, err := resolveCredentialPath(*envPath, *credentialPath)
	if err != nil {
		return err
	}

	endpoints := siwc.OfficialEndpoints()
	httpClient := siwc.NewHTTPClient(requestTimeout)
	session := siwc.NewSession(path, endpoints, httpClient)
	ctx := context.Background()
	accessToken, err := session.EnsureAccessToken(ctx)
	if err != nil {
		return err
	}
	models, err := siwc.ListModels(ctx, endpoints, httpClient, accessToken)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		log.Println("利用可能なOpenAIモデルがありません")
		return nil
	}
	for _, modelEntry := range models {
		log.Printf("%s (%s)", modelEntry.Slug, modelEntry.DisplayName)
	}
	return nil
}
