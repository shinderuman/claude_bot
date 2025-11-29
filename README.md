# Mastodon Claude Bot

MastodonでClaude APIを使用した会話ボットです。

## 機能

- Mastodonのメンション通知をリアルタイムで受信
- Claude APIを使用して応答を生成（公式SDKを使用）
- ユーザーごとの会話履歴を保持（JSON形式で永続化）
- 履歴が長くなると自動的に要約して圧縮（最大30件の要約を保持）
- キャラクター設定によるBot人格のカスタマイズ
- 文字数制限の自動チェックとリトライ機能
- 投稿失敗時の自動エラーメッセージ生成
- リモートユーザーからのメンション制御

## 必要な環境変数

以下の環境変数を設定してください：

```bash
# Mastodon設定
MASTODON_SERVER=https://mastodon.example.com
MASTODON_ACCESS_TOKEN=your_access_token_here
BOT_USERNAME=your_bot_username

# Claude API設定
ANTHROPIC_AUTH_TOKEN=your_anthropic_api_key
ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic
ANTHROPIC_DEFAULT_MODEL=glm-4.6

# キャラクター設定
CHARACTER_PROMPT=あなたは親切で丁寧なアシスタントです。

# リモートユーザー設定（オプション）
# true: 他サーバーからのメンションも受け付ける
# false: 同一サーバーからのメンションのみ受け付ける（デフォルト）
ALLOW_REMOTE_USERS=false
```

## セットアップ

1. `.env.example`を`.env`にコピーして環境変数を設定

```bash
cp .env.example .env
# .envファイルを編集して適切な値を設定
```

2. ビルド

```bash
go build -o mastodon-bot main.go
```

3. 依存関係のインストール

```bash
go mod tidy
```

4. Claude APIとの疎通確認（オプション）

```bash
# .envファイルが自動的に読み込まれます
./mastodon-bot -test

# カスタムメッセージで確認
./mastodon-bot -test -message "こんにちは"
```

5. 実行

```bash
# .envファイルが自動的に読み込まれます
./mastodon-bot
```

または直接環境変数を指定：

```bash
MASTODON_SERVER=https://... MASTODON_ACCESS_TOKEN=... ./mastodon-bot
```

## コマンドラインオプション

- `-test`: Claude APIとの疎通確認モード（Mastodonに接続せずClaudeのみテスト）
- `-message`: テストモードで送信するメッセージ（デフォルト: "Hello"）

```bash
# 使用例
go run main.go -test
go run main.go -test -message "動作確認"
```

## Mastodon Access Tokenの取得方法

1. Mastodonインスタンスにログイン
2. 設定 → 開発 → 新規アプリケーション
3. アプリケーション名を入力（例：Claude Bot）
4. 必要な権限を選択：
   - `read:notifications` - 通知の読み取り（メンション受信に必要）
   - `read:statuses` - 投稿の読み取り（メンション内容の取得に必要）
   - `write:statuses` - 投稿の作成（返信投稿に必要）
5. 「送信」をクリックしてアプリケーションを作成
6. 作成されたアプリケーションの詳細ページでアクセストークンをコピー

## 会話履歴の管理

- ユーザーごとに会話履歴を`sessions.json`に保存
- メッセージが20件を超えると自動的に古い履歴を要約
- 最新10件のメッセージは詳細を保持、それ以前は要約
- 要約は最大30件まで保持（古いものから削除）
- プログラム再起動後も履歴を保持

## 応答の文字数制限

- 応答は450文字以内に制限（Mastodonの500文字制限に対応）
- MaxTokens: 700トークンで物理的に制限
- 文字数超過時は最大3回自動リトライ
- リトライ失敗時はエラーメッセージを投稿

## カスタマイズ

### キャラクター設定

`CHARACTER_PROMPT`環境変数でBotの人格を設定できます：

```bash
CHARACTER_PROMPT="あなたは猫のような口調で話すアシスタントです。語尾に「にゃん」をつけてください。"
```

### 定数の変更

`main.go`の定数を変更することで動作をカスタマイズできます：

```go
const (
    maxResponseChars = 450              // 応答の文字数制限
    maxResponseTokens = 700             // Claude APIの最大トークン数
    maxRetries = 3                      // リトライ回数
    historyCompressThreshold = 20       // 履歴圧縮の閾値
    detailedMessageCount = 10           // 詳細保持するメッセージ数
    maxSummaries = 30                   // 要約の最大保持件数
)
```

## 注意事項

- Botは自分自身のメンションには反応しません
- 空のメンション（メンションのみで本文がない）には反応しません
- 返信は元の投稿と同じ公開範囲で投稿されます
- リモートユーザーからのメンションはデフォルトで無視されます（`ALLOW_REMOTE_USERS=true`で有効化）
