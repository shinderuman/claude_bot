#!/bin/bash

set -e

echo "=== Mastodon Claude Bot デプロイスクリプト ==="

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

echo "✓ ビルド完了"

# .envファイルの確認
if [ ! -f ".env" ]; then
    echo "エラー: .envファイルが見つかりません"
    exit 1
fi

echo "✓ .envファイル確認完了"

# リモートディレクトリの作成
echo "2. リモートディレクトリを準備中..."
ssh ${REMOTE_HOST} "mkdir -p ${REMOTE_DIR}"

# ファイルの転送
echo "3. ファイルを転送中..."
scp ${APP_NAME} ${REMOTE_HOST}:${REMOTE_DIR}/
scp .env ${REMOTE_HOST}:${REMOTE_DIR}/

echo "✓ ファイル転送完了"

# 実行権限の付与
echo "4. 実行権限を付与中..."
ssh ${REMOTE_HOST} "chmod +x ${REMOTE_DIR}/${APP_NAME}"

echo "✓ 実行権限付与完了"

# Supervisorの再起動
echo "5. Supervisorを再起動中..."
ssh ${REMOTE_HOST} "sudo supervisorctl restart ${APP_NAME}"

echo "✓ Supervisor再起動完了"

# ローカルのバイナリを削除
rm ${APP_NAME}

echo ""
echo "=== デプロイ完了 ==="
echo "ステータス確認: ssh ${REMOTE_HOST} 'supervisorctl status ${APP_NAME}'"
