# Mastodon Claude Bot

MastodonでClaude APIを使用した会話ボットです。

## 機能

- Mastodonのメンション通知をリアルタイムで受信
- Claude APIを使用して応答を生成（公式SDKを使用）
- リプライツリーごとに独立した会話履歴を管理
- ユーザーごとの会話履歴を保持（JSON形式で永続化）
- 履歴が長くなると自動的に要約して圧縮（累積要約方式）
- 事実データベースによるユーザー情報の永続化と活用
- キャラクター設定によるBot人格のカスタマイズ
- 長い応答の自動分割投稿（480文字単位、改行位置で分割）
- 投稿失敗時の自動エラーメッセージ生成
- リモートユーザーからのメンション制御

## 前提条件

### 依存ツール

- **Go**: プログラミング言語（ビルドに必要）
- **rsync**: ファイル転送ツール（デプロイに必須、macOSには標準で含まれています）

#### rsyncのインストール

デプロイスクリプト実行にはrsyncが必須です：

```bash
# macOS: 標準で含まれているため不要

# Ubuntu/Debian
sudo apt-get install rsync
```

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

# LLM & 投稿設定
MAX_RESPONSE_TOKENS=512  # 通常応答の最大トークン数
MAX_SUMMARY_TOKENS=512   # 要約生成の最大トークン数
MAX_POST_CHARS=480       # 投稿の最大文字数
```

## セットアップ

1. `data/.env.example`を`data/.env`にコピーして環境変数を設定

```bash
cp data/.env.example data/.env
# data/.envファイルを編集して適切な値を設定
```

2. 依存関係のインストール

```bash
go mod tidy
```

3. ビルド（リリース用）

```bash
# ボット本体
go build -o claude_bot ./cmd/claude_bot

# 疎通確認コマンド
go build -o test_claude ./cmd/test_claude
```

## デプロイ

シンプルなデプロイスクリプトを使用して、リモートサーバーにアプリケーションをデプロイします。

### デプロイコマンド

```bash
# デプロイスクリプトの実行
./deploy.sh
```

実行すると、以下の質問が表示されます：

- `プログラムをデプロイしますか？ [y/N]:`

### デプロイスクリプトの機能

- **設定ファイル自動同期**: data/ディレクトリを常に同期
- **プログラムデプロイ選択**: 明示的に指定した場合のみプログラムをデプロイ
- **自動ビルド**: Linux向けバイナリを自動でビルド（プログラムデプロイ時）
- **rsync同期**: 高速・安全なファイル転送と双方向同期
- **Supervisor管理**: デプロイ前に停止し、後に自動で開始
- **エラーハンドリング**: ビルド失敗やファイル存在チェック
- **シンプル設計**: 余計な関数や複雑な処理を排除し、メンテナンス性を向上

### 設定変数

スクリプト上部で以下の設定を変更できます：

```bash
# サーバー設定
REMOTE_HOST="kenji.asmodeus.jp"
REMOTE_DIR="/home/ubuntu/claude_bot"
APP_NAME="claude_bot"

# SFTP接続設定
SFTP_USER="ubuntu"
SFTP_KEY_FILE="$HOME/.ssh/mastodon.pem"

# 同期対象ディレクトリ
DATA_DIR="data"

# デフォルト値
DEPLOY_PROGRAM=false  # 安全のためデフォルトで非デプロイ
```

### 同期の仕組み

- **設定ファイル**: 常時双方向同期（新しい方のファイルを優先）
- **rsync使用**: 高速で安全なファイル転送と同期管理
- **自動検出**: ファイルの存在を自動チェックしてエラーを防止

### 実行フロー

1. **環境チェック**: rsyncのインストールを確認
2. **プログラムデプロイ選択**: y/Nプロンプトで選択（デフォルトはN）
3. **リモート準備**: リモートディレクトリを作成
4. **ビルド処理**: Linux向けバイナリをビルド（yを選択時）
5. **データディレクトリ同期**: 常にdata/ディレクトリを同期
6. **デプロイ実行**: Supervisorを停止・転送・開始（yを選択時）

### 事実データベースの同期

`facts.json`はユーザー情報を永続化する新しい機能です：

- **事実情報**: ユーザーの属性、所有物、固定的な好みを保存
- **自動抽出**: ユーザーの発言から重要な事実を自動的に抽出
- **応答活用**: 保存された情報を基に、よりパーソナライズされた応答を生成
- **30日間保持**: 30日経過したデータは自動的に削除され、鮮度を維持

## プロジェクト構成

```
claude_bot/
├── cmd/
│   ├── claude_bot/     # ボット本体のエントリーポイント
│   └── test_claude/    # 疎通確認コマンド
├── internal/
│   ├── bot/           # Mastodonボットロジック
│   ├── config/        # 設定管理
│   ├── llm/           # Claude API連携・プロンプト管理
│   ├── mastodon/      # Mastodon APIクライアント
│   ├── model/         # データモデル
│   └── store/         # データ永続化
├── data/              # 設定・データファイル
│   ├── .env           # 環境変数設定
│   ├── sessions.json  # 会話履歴
│   └── facts.json     # 事実データベース
└── deploy.sh          # デプロイスクリプト
```

## ローカル実行

### Claude APIとの疎通確認

専用の`test_claude`コマンドで疎通確認ができます：

```bash
# ビルドして実行
go build -o test_claude ./cmd/test_claude
./test_claude

# カスタムメッセージで確認
./test_claude -message "こんにちは"

# または直接実行
go run ./cmd/test_claude -message "動作確認"
```

### 通常実行

```bash
# ビルドして実行
go build -o claude_bot ./cmd/claude_bot
./claude_bot

# または直接実行
go run ./cmd/claude_bot
```

または直接環境変数を指定：

```bash
MASTODON_SERVER=https://... MASTODON_ACCESS_TOKEN=... ./claude_bot
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

### リプライツリーごとの会話管理

- 各リプライツリー（会話スレッド）を独立して管理
- リプライチェーンのルートを自動検出して会話を識別
- 異なるリプライツリーの会話は互いに影響しない
- ユーザーごとに複数の並行会話を保持可能

### 会話履歴の保存と圧縮

- ユーザーごとの全会話を`sessions.json`に保存
- 各リプライツリー内でメッセージが20件を超えると自動圧縮
- 圧縮後は最新10件のメッセージを保持、それ以前は要約
- 3時間以上更新がない会話（4往復以上）は自動的に要約に移行
- 24時間以上経過した会話も自動的に要約に移行（最低3つの会話は保持）
- 要約は累積方式で1つに統合（過去の要約 + 新しいメッセージを再要約）
- プログラム再起動後も履歴を保持
- 応答生成失敗時や投稿失敗時は自動的にロールバック

## 応答の分割投稿

- 長い応答は自動的に分割して複数の投稿として送信
- 分割は480文字を基準に、可能な限り改行位置で実施
- 分割された投稿はツリー状に連続投稿（前の投稿への返信として投稿）
- MaxTokens: 512トークンで生成

## カスタマイズ

### キャラクター設定

`CHARACTER_PROMPT`環境変数でBotの人格を設定できます：

```bash
CHARACTER_PROMPT="あなたは猫のような口調で話すアシスタントです。語尾に「にゃん」をつけてください。"
```

### 定数の変更

`.env`ファイルまたは環境変数で以下の設定をカスタマイズできます：

```bash
# 会話管理設定
CONVERSATION_MESSAGE_COMPRESS_THRESHOLD=20  # この件数を超えたら圧縮
CONVERSATION_MESSAGE_KEEP_COUNT=10          # 圧縮後に保持するメッセージ件数
CONVERSATION_IDLE_HOURS=3                   # この時間更新がない会話を要約化
CONVERSATION_RETENTION_HOURS=24             # この時間を超えた会話を削除
CONVERSATION_MIN_KEEP_COUNT=3               # 最低限保持する会話数

# 事実データベース設定
ENABLE_FACT_STORE=true                      # 事実保存機能の有効/無効

# LLM & 投稿設定
MAX_RESPONSE_TOKENS=512                     # 通常応答の最大トークン数
MAX_SUMMARY_TOKENS=512                      # 要約生成の最大トークン数
MAX_POST_CHARS=480                          # 投稿の最大文字数
```

## 注意事項

- Botは自分自身のメンションには反応しません
- 空のメンション（メンションのみで本文がない）には反応しません
- 返信は元の投稿と同じ公開範囲で投稿されます
- リモートユーザーからのメンションはデフォルトで無視されます（`ALLOW_REMOTE_USERS=true`で有効化）
