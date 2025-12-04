# Mastodon Claude Bot

Mastodonのメンション通知にリアルタイムで応答する、Claude API搭載の会話ボットです。
文脈を理解した自然な会話、長期記憶、画像認識、キャラクター設定など、高度な対話機能を提供します。

## クイックスタート

### 1. 前提条件
- **Go**: ビルドに必要
- **rsync**: デプロイに必要（macOSは標準搭載）

### 2. セットアップ
```bash
# リポジトリのクローン
git clone <repository-url>
cd claude_bot

# 設定ファイルの準備
cp data/.env.example data/.env
# data/.env を編集してAPIキー等を設定してください

# 依存関係のインストール
go mod tidy
```

### 3. 動作確認
Claude APIとの接続を確認するための専用コマンドが用意されています。

```bash
# ビルドと実行
go build -o test_claude ./cmd/test_claude
./test_claude -message "こんにちは"

# 画像認識のテスト（オプション）
./test_claude -message "この画像は何ですか？" -image /path/to/image.png
```

### 4. ボットの起動
```bash
go run ./cmd/claude_bot
```

---

## 主な機能

### 🗣️ 高度な会話機能
- **リプライツリー管理**: スレッドごとの文脈を個別に保持し、並行した会話が可能。
- **自動要約**: 会話が長くなると自動的に要約し、トークンを節約しつつ文脈を維持。
- **分割投稿**: 長文の応答は480文字単位で自然な位置で分割して連投。

### 🧠 記憶・学習機能
- **事実データベース (Facts)**: ユーザーの属性や好みを自動抽出し、永続的に記憶。
  - 例: 「私はコーヒーが好き」→ 次回の会話で「コーヒーはいかがですか？」と提案。
- **長期記憶**: 過去の会話要約を保持し、セッションを超えて文脈を継承。
- **タイムラインからの自動学習**: ストリーミングAPIで連合/ホームタイムラインを監視し、外部リンクから自動的にファクトを収集（オプション）。
- **Fediverse投稿の自動除外**: `.well-known/nodeinfo`を使用してFediverseサーバーを判定し、ローカル投稿URLを除外。

### 🖼️ 画像認識（実験的）
- **画像の理解**: メンションに添付された画像を認識し、内容を踏まえた応答を生成（Claude API使用時のみ）。
- **MIMEタイプ自動判定**: JPEG、PNG、WebPなど、様々な画像形式に対応。
- **オンオフ切り替え**: `.env`で簡単に有効/無効を切り替え可能。

### ⚙️ 柔軟な制御
- **キャラクター設定**: プロンプトで人格を自由にカスタマイズ可能。
- **リモート制御**: 他インスタンスからのメンション受け入れ可否を設定可能。

---

## 設定・カスタマイズ

`data/.env` ファイルで以下の設定を行います。

> [!IMPORTANT]
> **すべての設定値は明示的に指定する必要があります。**  
> コード内にデフォルト値を持たせず、すべて設定ファイルで管理する設計思想を採用しています。
> これにより、どの設定値が実際に使用されているかが常に明確になります。

### Mastodon接続設定（必須）
| 変数名 | 説明 |
| :--- | :--- |
| `MASTODON_SERVER` | MastodonインスタンスのURL（例: `https://mastodon.social`） |
| `MASTODON_ACCESS_TOKEN` | Mastodonのアクセストークン |
| `BOT_USERNAME` | Botのユーザー名（メンション判定に使用） |

### Claude API設定（必須）
| 変数名 | 説明 |
| :--- | :--- |
| `ANTHROPIC_AUTH_TOKEN` | Claude APIキー |
| `ANTHROPIC_BASE_URL` | Claude APIのベースURL（例: `https://api.anthropic.com`） |
| `ANTHROPIC_DEFAULT_MODEL` | 使用するモデル名（例: `claude-3-5-sonnet-20241022`） |

### Bot動作設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `CHARACTER_PROMPT` | (任意) | Botの人格設定プロンプト。空文字列も可 |
| `ALLOW_REMOTE_USERS` | `false` | `true`: 他インスタンスからのメンションも受け付ける<br>`false`: 同一インスタンスのみ |
| `ENABLE_FACT_STORE` | `true` | `true`: ユーザー情報を記憶する<br>`false`: 記憶機能を無効化 |
| `ENABLE_IMAGE_RECOGNITION` | `false` | `true`: 画像認識を有効化（Claude API推奨）<br>`false`: 画像認識を無効化 |

### 会話管理パラメータ
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `CONVERSATION_MESSAGE_COMPRESS_THRESHOLD` | `20` | この数を超えたら会話履歴を圧縮 |
| `CONVERSATION_MESSAGE_KEEP_COUNT` | `10` | 圧縮後に保持する最新メッセージ数 |
| `CONVERSATION_MIN_KEEP_COUNT` | `3` | 最低限保持するメッセージ数 |
| `CONVERSATION_IDLE_HOURS` | `3` | この時間アイドル状態なら要約 |
| `CONVERSATION_RETENTION_HOURS` | `24` | 会話を完全に削除するまでの時間 |

### LLM・投稿パラメータ
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `MAX_RESPONSE_TOKENS` | `512` | 応答生成の最大トークン数 |
| `MAX_SUMMARY_TOKENS` | `512` | 要約生成の最大トークン数 |
| `MAX_FACT_TOKENS` | `1024` | ファクト抽出の最大トークン数（URL事実抽出では多くのトークンが必要） |
| `MAX_POST_CHARS` | `480` | 1投稿あたりの最大文字数（分割投稿の閾値） |

### 自動投稿設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `AUTO_POST_INTERVAL_HOURS` | `0` | 自動投稿の間隔（時間単位）。`0`で無効化 |
| `AUTO_POST_VISIBILITY` | `unlisted` | 自動投稿の公開範囲（`public`, `unlisted`, `private`） |

### ファクト管理設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `FACT_RETENTION_DAYS` | `30` | ファクト保持期間（日数） |
| `MAX_FACTS` | `10000` | 最大ファクト数 |

### ファクト収集設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `FACT_COLLECTION_ENABLED` | `false` | `true`: タイムラインから自動収集<br>`false`: メンションのみ |
| `FACT_COLLECTION_FEDERATED` | `true` | 連合タイムラインから収集するか |
| `FACT_COLLECTION_HOME` | `true` | ホームタイムラインから収集するか |
| `FACT_COLLECTION_FROM_POST_CONTENT` | `false` | 投稿本文からもファクトを抽出するか（`false`推奨） |
| `URL_BLACKLIST` | `localhost,*.local,*.localhost` | メタデータ取得をスキップするURLパターン（カンマ区切り、ワイルドカード可） |
| `FACT_COLLECTION_MAX_WORKERS` | `3` | LLMリクエストの並列数（ファクト収集時のLLM呼び出しを制限）<br>**注**: メンション応答には影響しません |
| `FACT_COLLECTION_MAX_PER_HOUR` | `100` | 1時間あたりの最大処理数（レート制限） |

<details>
<summary>Mastodon Access Tokenの取得方法</summary>

1. Mastodonインスタンスの設定 → 開発 → 新規アプリケーション
2. 権限: `read:notifications`, `read:statuses`, `write:statuses` を選択
3. 作成後にアクセストークンをコピー
</details>

---

## デプロイ

付属の `deploy.sh` で簡単にデプロイできます。

```bash
./deploy.sh
```

- **機能**: 自動ビルド(Linux用)、設定ファイル同期、Supervisor再起動
- **設定**: スクリプト内の変数を環境に合わせて編集してください（`REMOTE_HOST`, `REMOTE_DIR` 等）

---

## 開発者向け情報

### プロジェクト構成
```
claude_bot/
├── cmd/              # エントリーポイント
│   ├── claude_bot/   # メインBot
│   └── test_claude/  # Claude API接続テスト
├── internal/         # アプリケーションロジック
│   ├── bot/          # Bot本体・応答生成
│   ├── collector/    # タイムラインからのファクト収集
│   ├── config/       # 設定管理
│   ├── facts/        # ファクト抽出・検索
│   ├── fetcher/      # URLメタデータ取得・NodeInfo判定
│   ├── llm/          # Claude API連携
│   ├── mastodon/     # Mastodon API連携
│   ├── model/        # データ構造
│   ├── store/        # データ永続化 (JSON)
│   └── utils/        # ユーティリティ関数
└── data/             # 設定・データファイル
```

### 主要コンポーネント

- **bot**: メンション処理、応答生成、会話管理
- **collector**: ストリーミングAPIを使用したタイムラインからの自動ファクト収集
- **facts**: ファクトの抽出、保存、検索、重複排除
- **fetcher**: URLメタデータ取得、NodeInfoによるFediverseサーバー判定
- **llm**: Claude APIとの通信、プロンプト管理、画像送信
- **mastodon**: Mastodon APIとの通信、ストリーミング、画像ダウンロード
- **store**: 会話履歴とファクトのJSON永続化

---

## ライセンス

MIT License
