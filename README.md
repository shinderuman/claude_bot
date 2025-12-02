# Mastodon Claude Bot

Mastodonのメンション通知にリアルタイムで応答する、Claude API搭載の会話ボットです。
文脈を理解した自然な会話、長期記憶、キャラクター設定など、高度な対話機能を提供します。

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

### ⚙️ 柔軟な制御
- **キャラクター設定**: プロンプトで人格を自由にカスタマイズ可能。
- **リモート制御**: 他インスタンスからのメンション受け入れ可否を設定可能。

---

## 設定・カスタマイズ

`data/.env` ファイルで以下の設定を行います。

### 必須設定
| 変数名 | 説明 |
| :--- | :--- |
| `MASTODON_SERVER` | MastodonインスタンスのURL |
| `MASTODON_ACCESS_TOKEN` | Mastodonのアクセストークン |
| `ANTHROPIC_AUTH_TOKEN` | Claude APIキー |

### カスタマイズ設定
| 変数名 | デフォルト | 説明 |
| :--- | :--- | :--- |
| `CHARACTER_PROMPT` | (なし) | Botの人格設定プロンプト |
| `ALLOW_REMOTE_USERS` | `false` | 外部インスタンスのユーザーに応答するか |
| `ENABLE_FACT_STORE` | `true` | 事実記憶機能を有効にするか |

### パラメータ調整
| 変数名 | デフォルト | 説明 |
| :--- | :--- | :--- |
| `MAX_RESPONSE_TOKENS` | `512` | 応答の最大トークン数 |
| `CONVERSATION_IDLE_HOURS` | `3` | 会話を要約するまでのアイドル時間(h) |
| `CONVERSATION_RETENTION_HOURS` | `24` | 会話を保持する最大時間(h) |

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
├── cmd/            # エントリーポイント
├── internal/       # アプリケーションロジック
│   ├── bot/        # Bot本体・要約ロジック
│   ├── llm/        # Claude API連携
│   ├── mastodon/   # Mastodon API連携
│   ├── model/      # データ構造
│   └── store/      # データ永続化 (JSON)
└── data/           # 設定・データファイル
```
