#!/bin/bash

set -e

echo "=== Mastodon Claude Bot デプロイスクリプト ==="

# 引数の解析
COPY_ENV=false
COPY_SESSIONS=false
for arg in "$@"; do
    case $arg in
        --env)
        COPY_ENV=true
        shift
        ;;
        -e)
        COPY_ENV=true
        shift
        ;;
        --sessions)
        COPY_SESSIONS=true
        shift
        ;;
        -s)
        COPY_SESSIONS=true
        shift
        ;;
        --help|-h)
        echo "使い方: $0 [オプション]"
        echo "オプション:"
        echo "  --env, -e       .envファイルを同期する"
        echo "  --sessions, -s  sessions.jsonファイルを同期する"
        echo "  --help, -h      このヘルプを表示する"
        exit 0
        ;;
        *)
        echo "不明なオプション: $arg"
        echo "--helpで使い方を確認してください"
        exit 1
        ;;
    esac
done

# 設定
REMOTE_HOST="kenji.asmodeus.jp"
REMOTE_DIR="/home/ubuntu/claude_bot"
APP_NAME="claude_bot"

# ビルド
echo "1. Linux向けバイナリをビルド中..."
GOOS=linux GOARCH=amd64 go build -o ${APP_NAME} .

if [ ! -f "${APP_NAME}" ]; then
    echo "エラー: ビルドに失敗しました"
    exit 1
fi

# 実行権限の付与
chmod +x ${APP_NAME}

echo "✓ ビルド完了（実行権限付与済み）"

# .envファイルの確認（--envオプション時のみ）
if [ "$COPY_ENV" = true ]; then
    if [ ! -f ".env" ]; then
        echo "エラー: .envファイルが見つかりません"
        exit 1
    fi
    echo "✓ .envファイル確認完了（同期対象）"
else
    echo "ℹ .envファイルはスキップされます（--envオプションで有効化）"
fi

# sessions.jsonの確認（--sessionsオプション時のみ）
if [ "$COPY_SESSIONS" = true ]; then
    if [ -f "sessions.json" ]; then
        echo "✓ sessions.json確認完了（同期対象）"
    else
        echo "ℹ sessions.jsonがありません（新規作成されます）"
    fi
else
    echo "ℹ sessions.jsonはスキップされます（--sessionsオプションで有効化）"
fi

# リモートディレクトリの作成
echo "2. リモートディレクトリを準備中..."
ssh ${REMOTE_HOST} "mkdir -p ${REMOTE_DIR}"

# .envファイルの同期（--envオプション時のみ）
if [ "$COPY_ENV" = true ]; then
    echo "3. .envファイルを同期中..."

    # リモートのファイルタイムスタンプを取得
    REMOTE_ENV_TIMESTAMP=$(ssh ${REMOTE_HOST} "stat -c %Y ${REMOTE_DIR}/.env 2>/dev/null || echo 0")
    LOCAL_ENV_TIMESTAMP=0

    if [ -f ".env" ]; then
        # macOSとLinuxでstatコマンドのオプションが異なるため両方対応
        if [[ "$OSTYPE" == "darwin"* ]]; then
            LOCAL_ENV_TIMESTAMP=$(stat -f %m .env 2>/dev/null || echo 0)
        else
            LOCAL_ENV_TIMESTAMP=$(stat -c %Y .env 2>/dev/null || echo 0)
        fi
    fi

    if [ $LOCAL_ENV_TIMESTAMP -gt $REMOTE_ENV_TIMESTAMP ]; then
        # ローカルの方が新しい場合は転送
        if [ -f ".env" ]; then
            scp .env ${REMOTE_HOST}:${REMOTE_DIR}/
            echo "✓ .envファイル転送完了（ローカルの方が新しい）"
        fi
    elif [ $REMOTE_ENV_TIMESTAMP -gt 0 ]; then
        # リモートの方が新しい場合はダウンロード
        scp ${REMOTE_HOST}:${REMOTE_DIR}/.env ./
        echo "✓ .envファイルダウンロード完了（リモートの方が新しい）"
    else
        # どちらにも存在しない場合はエラー
        echo "エラー: .envファイルが見つかりません"
        exit 1
    fi
fi

# sessions.jsonの同期（--sessionsオプション時のみ）
if [ "$COPY_SESSIONS" = true ]; then
    echo "   sessions.jsonを同期中..."

    # リモートのファイルタイムスタンプを取得
    REMOTE_TIMESTAMP=$(ssh ${REMOTE_HOST} "stat -c %Y ${REMOTE_DIR}/sessions.json 2>/dev/null || echo 0")
    LOCAL_TIMESTAMP=0

    if [ -f "sessions.json" ]; then
        # macOSとLinuxでstatコマンドのオプションが異なるため両方対応
        if [[ "$OSTYPE" == "darwin"* ]]; then
            LOCAL_TIMESTAMP=$(stat -f %m sessions.json 2>/dev/null || echo 0)
        else
            LOCAL_TIMESTAMP=$(stat -c %Y sessions.json 2>/dev/null || echo 0)
        fi
    fi

    if [ $LOCAL_TIMESTAMP -gt $REMOTE_TIMESTAMP ]; then
        # ローカルの方が新しい場合は転送
        if [ -f "sessions.json" ]; then
            scp sessions.json ${REMOTE_HOST}:${REMOTE_DIR}/
            echo "✓ sessions.json転送完了（ローカルの方が新しい）"
        fi
    elif [ $REMOTE_TIMESTAMP -gt 0 ]; then
        # リモートの方が新しい場合はダウンロード
        scp ${REMOTE_HOST}:${REMOTE_DIR}/sessions.json ./
        echo "✓ sessions.jsonダウンロード完了（リモートの方が新しい）"
    else
        # どちらにも存在しない場合は何もしない
        echo "ℹ sessions.jsonは存在しません"
    fi
fi

# Supervisorの停止
echo "4. Supervisorを停止中..."
ssh ${REMOTE_HOST} "sudo supervisorctl stop ${APP_NAME}"


# バイナリの転送（最後に転送）
echo "5. バイナリを転送中..."
scp ${APP_NAME} ${REMOTE_HOST}:${REMOTE_DIR}/

# Supervisorの開始
echo "6. Supervisorを開始中..."
ssh ${REMOTE_HOST} "sudo supervisorctl start ${APP_NAME}"

echo "✓ Supervisor開始完了"

# ローカルのバイナリを削除
rm ${APP_NAME}

echo ""
echo "=== デプロイ完了 ==="
echo "ステータス確認: ssh ${REMOTE_HOST} 'supervisorctl status ${APP_NAME}'"
