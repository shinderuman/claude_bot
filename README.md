# Mastodon Claude / Gemini Bot

MastodonのメンションにClaudeまたはGeminiで応答するBotです。会話履歴の要約、ファクトの記憶、画像認識・SVG生成に対応します。

## 起動

Go 1.24以降とRedisを用意し、設定ファイルをコピーします。

```sh
cp data/.env.example data/.env
```

Mastodonの「設定 → 開発 → 新規アプリケーション」でアクセストークンを発行します。Mastodonの接続情報、利用するLLMのAPI設定、Redisの接続先を `data/.env` に記入します。設定項目は [設定例](data/.env.example) を参照してください。

```sh
RUN_MAINTENANCE=false go run ./cmd/claude_bot -env data/.env
```

Botごとに設定を変える場合は、`-env` に個別のファイルを指定します。そのファイルの設定が共通の `data/.env` より優先されます。

## 動作確認

```sh
go run ./cmd/test_claude -message "こんにちは" data/.env
```

会話履歴付きの応答を試すには `-mode conversation`、画像を渡すには `-image <画像パス>` を使います。

ChatGPT Plusは本人用のローカルCLIで利用できます。認証ファイルは `data/` の外に置き、`login` が表示するURLを同じマシンのブラウザーで開きます。 [利用規約](https://openai.com/policies/sign-in-with-chatgpt-terms/)

```sh
mkdir -p -m 700 ~/.config/claude_bot
go run ./cmd/openai_auth login -credential ~/.config/claude_bot/openai.json
go run ./cmd/openai_auth models -credential ~/.config/claude_bot/openai.json
```

`data/` の外に `openai.env` を作り、`LLM_PROVIDER=openai`、`OPENAI_MODEL` に一覧のモデルID、`OPENAI_CREDENTIAL_FILE` に認証ファイルの絶対パスを記入します。上の `test_claude` にこの設定ファイルを渡します。プラン枠内で使うには、ChatGPTの使用状況でアプリによるクレジット使用を無効にします。 [クレジット設定](https://help.openai.com/en/articles/20001542-using-your-chatgpt-plan-in-other-apps-and-sites)

## デプロイ

`deploy.sh` の接続先を設定し、デプロイするBotの `data/.env.<名前>` を用意します。例えば `touch data/.env.claude` で空のファイルを作ると、起動時と同じ共通設定を使えます。この場合はサーバーのSupervisorに `claude_bot_claude` を登録し、`-env .env.claude` を渡します。 [Supervisor設定例](data/supervisord.conf.example)

`./deploy.sh` で「通常デプロイ」を選ぶと、Linux用にビルドして `data/` を同期し、SupervisorでBotを再起動します。同期にはrsyncが必要です。

配備先には `flock` が必要です。SSH切断後に `.deploy.lock` が残った場合は、サーバー側でデプロイ処理の終了を確認してから解除します。

メンテナンスデプロイでは、SSHの接続ユーザーとSupervisorがBotを起動するユーザーを揃えます。

## 開発

テストは `go test ./...` で実行します。

## ライセンス

MIT License
