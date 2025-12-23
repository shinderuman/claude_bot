# Mastodon Claude / Gemini Bot

Mastodonのメンション通知にリアルタイムで応答する、LLM (Claude / Gemini) 搭載の会話ボットです。
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
LLM APIとの接続を確認するための専用コマンドが用意されています。

```bash
# ビルドと実行
go build -o test_claude ./cmd/test_claude
./test_claude -message "こんにちは"

# 画像認識のテスト（オプション）
./test_claude -message "この画像は何ですか？" -image /path/to/image.png

# 画像生成のテスト（オプション）
./test_claude -mode generate-image -message "赤い丸と青い四角を描いて"

# 会話モード（インタラクティブ）
./test_claude -mode conversation
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
- **収集範囲の制限**: 公開設定（Public）の投稿のみを収集対象とし、未収載（Unlisted）や非公開（Private）の投稿からは学習しません（Bot自身のUnlisted投稿は例外）。
- **自己学習機能**: 自分自身の過去の投稿を分析し、自分の性格や振る舞いに関するファクトを蓄積・強化します。

### 🤖 Bot間連携 (Peer Bot Recognition)
- **同僚Botの認識**: 同じネットワーク内で稼働している他のBot（同僚）を自動的に検出し、認識します。
- **知識の共有**: 他のBotに関する情報を「同僚ファクト」として蓄積し、会話の中で言及したり、関係性を理解したりすることが可能です。
- **クラスタ管理**: `cluster_registry.json` を使用して稼働中のBotを管理し、重複起動の防止や連携を制御します。

### 🆔 プロフィール自動更新
- **自己紹介の自動生成**: 蓄積されたファクトに基づいて、Bot自身のプロフィール（自己紹介文）を定期的に生成・更新します。
- **免責事項の付与**: プロフィールには自動的に「※このアカウントの投稿には事実に基づく内容が含まれることもありますが、すべての正確性は保証できません。」という免責事項が付与されます。
- **文字数制限の考慮**: Mastodonの500文字制限に収まるように、免責事項を優先しつつ内容を適切に調整します。
- **ステータス表示の自動化**:
  - **メンション受付状況**: `ALLOW_REMOTE_USERS` の設定に基づき、「外部サーバーからのメンション受付: 許可/停止中」を自動表示。
  - **最終更新日時**: プロフィール更新時のタイムスタンプを自動的に付与。

### 🖼️ 画像認識（実験的）
- **画像の理解**: メンションに添付された画像を認識し、内容を踏まえた応答を生成（Claude / Gemini 両対応）。
- **MIMEタイプ自動判定**: JPEG、PNG、WebPなど、様々な画像形式に対応。
- **オンオフ切り替え**: `.env`で簡単に有効/無効を切り替え可能。

### 🎨 画像生成（SVG）
- **SVGイラスト生成**: ユーザーのリクエストに応じてSVG形式のイラストや図形を生成。
- **メディア添付**: 生成されたSVGは画像としてMastodonに投稿されます。
- **軽量・高品質**: ベクター形式なので軽量かつ拡大しても劣化しません。

### 📢 一斉送信コマンド
- **`!all` コマンド**: Botがフォローしているユーザー（信頼済みユーザー）が、投稿の先頭に `!all` （設定可）を付けて投稿すると、全Botが一斉に応答します。
    - **リプライ対応**: メンション（リプライ）の中でもコマンドを使用可能です。
    - **スレッド継続**: 短時間（10分以内）の連続したコマンド使用は、同じ会話スレッドとして扱われます。
- **ファクト除外**: このコマンドによる投稿はファクト収集（学習）の対象から自動的に除外されます。
- **自動フォロー**: Botからフォローされていないユーザーは、まず「@bot フォローして」とリクエストすることで、Botにフォローバックさせ、このコマンド権限を獲得できます。

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

### 環境設定ファイルの種類

本アプリケーションでは以下の順序で環境変数を読み込みます（上位が優先）。

1. **個別設定ファイル** (例: `data/.env.gemini`) - コマンドライン引数 `-env` で指定
2. **共通設定ファイル** (`data/.env`) - `data/` ディレクトリに配置

**共通設定ファイル (`data/.env`)** には、Slack設定やタイムゾーンなど、すべてのBotで共通する設定を記述することをお勧めします。個別設定ファイルでは、APIキーやBotユーザー名など、固有の設定のみを記述します。

### Mastodon接続設定（必須）
| 変数名 | 説明 |
| :--- | :--- |
| `MASTODON_SERVER` | MastodonインスタンスのURL（例: `https://mastodon.social`） |
| `MASTODON_ACCESS_TOKEN` | Mastodonのアクセストークン |
| `BOT_USERNAME` | Botのユーザー名（メンション判定に使用） |

### Slack連携設定
| 変数名 | 説明 |
| :--- | :--- |
| `SLACK_BOT_TOKEN` | Slack Bot User OAuth Token (例: `xoxb-...`) |
| `SLACK_CHANNEL_ID` | 通知送信用チャンネルID |
| `SLACK_ERROR_CHANNEL_ID` | エラーログ送信用チャンネルID（JSON修復失敗時などに通知） |

### Claude/Gemini API設定（必須）
`LLM_PROVIDER` で `claude` または `gemini` を指定します。

| 変数名 | 説明 |
| :--- | :--- |
| `LLM_PROVIDER` | `claude` または `gemini` |
| `ANTHROPIC_AUTH_TOKEN` | (Claude用) APIキー |
| `ANTHROPIC_BASE_URL` | (Claude用) APIのベースURL |
| `ANTHROPIC_DEFAULT_MODEL` | (Claude用) 使用モデル（例: `claude-3-5-sonnet-20241022`） |
| `GEMINI_API_KEY` | (Gemini用) APIキー |
| `GEMINI_MODEL` | (Gemini用) 使用モデル（例: `gemini-1.5-pro`） |

### Bot動作設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `CHARACTER_PROMPT` | (任意) | Botの人格設定プロンプト。空文字列も可 |
| `LLM_TEMPERATURE` | `1.0` | LLMの創造性パラメータ（0.0-1.0）。高いほど創造的 |
| `ALLOW_REMOTE_USERS` | `false` | `true`: 他インスタンスからのメンションも受け付ける<br>`false`: 同一インスタンスのみ |
| `ENABLE_FACT_STORE` | `true` | `true`: ユーザー情報を記憶する<br>`false`: 記憶機能を無効化 |
| `ENABLE_IMAGE_RECOGNITION` | `false` | `true`: 画像認識を有効化（ Claude/Gemini 共に対応）<br>`false`: 画像認識を無効化 |
| `ENABLE_IMAGE_GENERATION` | `false` | `true`: SVG画像生成機能を有効化<br>`false`: 画像生成機能を無効化 |

### コマンド設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `BROADCAST_COMMAND` | `!all` | Botがフォローしているユーザーが使用できる一斉呼び出しコマンド（Botはファクト収集を行わず即座に応答します） |

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
| `MAX_SUMMARY_TOKENS` | `2048` | 要約生成の最大トークン数 |
| `MAX_FACT_TOKENS` | `1024` | ファクト抽出の最大トークン数（URL事実抽出では多くのトークンが必要） |
| `MAX_IMAGE_TOKENS` | `2048` | 画像生成の最大トークン数 |
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

### 🛡️ データ整合性と信頼性
- **アトミック書き込み**: データの破損を防ぐため、保存時は一時ファイルへの書き込みとリネームによるアトミック操作を行います。
- **ファクト同期機能**: ディスク上の変更を検知し、メモリ上のデータを安全に更新する `SyncFromDisk` 機構を搭載しています。
- **JSON自動修復**: 
    - LLMからの応答が不正なJSONの場合でも、自動的に修復して処理を継続するロバストな仕組みを備えています。
    - **日本語・全角文字対応**: 全角コロンや日本語引用符などの表記ゆれも強力に補正します。
- **エラー監視**: JSON修復失敗などのクリティカルなエラー発生時には、即座にSlackへ通知を行い、ログの消失を防ぎます。

### ファイルパス・システム設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `SESSION_FILE` | `data/session.json` | 会話履歴の保存先 |
| `FACT_STORE_FILE` | `data/facts.json` | ファクトデータの保存先 |
| `BOT_PROFILE_FILE` | `data/Profile.txt` | 生成されたプロフィールの保存先 |
| `TIMEZONE` | `Asia/Tokyo` | ログ出力や時間管理に使用するタイムゾーン |

### メトリクス・ログ設定
| 変数名 | 推奨値 | 説明 |
| :--- | :--- | :--- |
| `METRICS_LOG_FILE` | `metrics.log` | メトリクス（JSON形式）の出力先 |
| `METRICS_LOG_INTERVAL_MINUTES` | `5` | メトリクス出力間隔（分） |

<details>
<summary>Mastodon Access Tokenの取得方法</summary>

1. Mastodonインスタンスの設定 → 開発 → 新規アプリケーション
2. 権限: `read:notifications`, `read:statuses`, `write:statuses`, `read:follows`, `write:follows` を選択
   - ※ `follows` 権限は自動フォロー機能に必要です
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
│   ├── discovery/    # Bot間連携・ディスカバリー (Cluster)
│   ├── facts/        # ファクト抽出・検索
│   ├── fetcher/      # URLメタデータ取得・NodeInfo判定
│   ├── image/        # 画像生成・変換
│   ├── llm/          # Claude API連携
│   ├── mastodon/     # Mastodon API連携
│   ├── model/        # データ構造
│   ├── store/        # データ永続化 (JSON)
│   └── util/         # ユーティリティ関数
└── data/             # 設定・データファイル
```

### 主要コンポーネント

- **bot**: メンション処理、応答生成、会話管理
- **collector**: ストリーミングAPIを使用したタイムラインからの自動ファクト収集
- **discovery**: 複数Bot稼働時の連携、存在確認（ハートビート）、ロック管理
- **facts**: ファクトの抽出、保存、検索、重複排除
- **fetcher**: URLメタデータ取得、NodeInfoによるFediverseサーバー判定
- **image**: SVG画像の生成とPNGへの変換処理
- **llm**: LLM (Claude / Gemini) APIとの通信、プロンプト管理、画像送信
- **mastodon**: Mastodon APIとの通信、ストリーミング、画像ダウンロード
- **store**: 会話履歴とファクトのJSON永続化

---

## ライセンス

MIT License
