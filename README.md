# Mastodon Claude Bot

MastodonでClaude APIを使用した会話ボットです。

## 機能

- Mastodonのメンション通知をリアルタイムで受信
- Claude APIを使用して応答を生成（公式SDKを使用）
- ユーザーごとの会話履歴を保持（JSON形式で永続化）
- 履歴が長くなると自動的に要約して圧縮（累積要約方式）
- キャラクター設定によるBot人格のカスタマイズ
- 長い応答の自動分割投稿（480文字単位、改行位置で分割）
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

2. 依存関係のインストール

```bash
go mod tidy
```

3. ビルド（リリース用）

```bash
go build -o claude_bot main.go
```

## デプロイ

自動化されたデプロイスクリプトを使用して、リモートサーバーにアプリケーションをデプロイします。

### デプロイコマンド

```bash
# 通常のデプロイ（同期なし）
./deploy.sh

# .envファイルを同期してデプロイ
./deploy.sh --env
./deploy.sh -e

# sessions.jsonを同期してデプロイ
./deploy.sh --sessions
./deploy.sh -s

# 両方のファイルを同期してデプロイ
./deploy.sh --env --sessions
./deploy.sh -e -s

# 同期のみ（デプロイはしない）
./deploy.sh --sync-only
```

### デプロイスクリプトの機能

- **自動ビルド**: Linux向けバイナリを自動でビルド
- **ファイル同期**: .envとsessions.jsonを双方向で同期（タイムスタンプ比較）
- **Supervisor管理**: デプロイ前に停止し、後に自動で開始
- **エラーハンドリング**: ビルド失敗やファイル存在チェック
- **macOS対応**: statコマンドのプラットフォーム互換性

### 同期の仕組み

- ローカルとリモートのファイルタイムスタンプを比較
- 新しい方のファイルを優先して転送
- タイムスタンプが同じ場合は何もしない
- macOSとLinuxの両方で動作

### コマンドラインオプション

- `--env, -e`: .envファイルを同期する
- `--sessions, -s`: sessions.jsonファイルを同期する
- `--sync-only`: 同期のみ行い、デプロイはしない（両ファイル対象）
- `--help, -h`: ヘルプを表示する

## ローカル実行

### Claude APIとの疎通確認（オプション）

```bash
# .envファイルが自動的に読み込まれます
./claude_bot -test

# カスタムメッセージで確認
./claude_bot -test -message "こんにちは"
```

### 通常実行

```bash
# .envファイルが自動的に読み込まれます
./claude_bot
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
- 要約は累積方式で1つに統合（過去の要約 + 新しいメッセージを再要約）
- プログラム再起動後も履歴を保持
- 応答生成失敗時や投稿失敗時は自動的にロールバック

## 応答の分割投稿

- 長い応答は自動的に分割して複数の投稿として送信
- 分割は480文字を基準に、可能な限り改行位置で実施
- 分割された投稿はツリー状に連続投稿（前の投稿への返信として投稿）
- MaxTokens: 1024トークンで生成

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
    maxResponseTokens = 1024            // Claude APIの最大トークン数（通常応答）
    maxSummaryTokens = 1024             // Claude APIの最大トークン数（要約生成）
    maxPostChars = 480                  // 投稿の最大文字数（バッファ含む）
    historyCompressThreshold = 20       // 履歴圧縮の閾値
    detailedMessageCount = 10           // 詳細保持するメッセージ数
)
```

## 注意事項

- Botは自分自身のメンションには反応しません
- 空のメンション（メンションのみで本文がない）には反応しません
- 返信は元の投稿と同じ公開範囲で投稿されます
- リモートユーザーからのメンションはデフォルトで無視されます（`ALLOW_REMOTE_USERS=true`で有効化）
