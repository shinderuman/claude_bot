#!/bin/bash

set -e

# ========================================
# 定数・グローバル変数
# ========================================

# 設定
REMOTE_HOST="kenji.asmodeus.jp"
REMOTE_DIR="/home/ubuntu/claude_bot"
APP_NAME="claude_bot"
SFTP_USER="ubuntu"
SFTP_KEY_FILE="$HOME/.ssh/mastodon.pem"

# デフォルト値
COPY_ENV=true
COPY_SESSIONS=true
DEPLOY_PROGRAM=true

# ステップ番号管理
STEP_NUM=1

# ========================================
# 関数定義
# ========================================

# ステップ表示ヘルパー
show_step() {
    echo "${STEP_NUM}. $1"
    STEP_NUM=$((STEP_NUM + 1))
}

# 共通質問処理
ask_question() {
    local question="$1"
    local var_name="$2"

    echo -n "${question} [Y/n]: "
    read -r response
    case $response in
        [nN][oO]|[nN])
            printf -v "$var_name" '%s' false
            echo "  → ${question%%しますか？}しません"
            ;;
        *)
            printf -v "$var_name" '%s' true
            ;;
    esac
}

# 共通表示処理
show_plan() {
    local task="$1"
    local var_name="${task%%:*}"
    local description="${task##*:}"

    [ "${!var_name}" = true ] && echo "  ✓ ${description}" || echo "  ✗ ${description}（スキップ）"
}

# rclone の共通オプション
get_rclone_opts() {
    echo "--sftp-host=${REMOTE_HOST}"
    echo "--sftp-user=${SFTP_USER}"
    echo "--sftp-key-file=${SFTP_KEY_FILE}"
    echo "--quiet"
}

# .env ファイルの同期（一方向: ローカル → リモート）
sync_env_file() {
    if [ ! -f ".env" ]; then
        echo "エラー: .envファイルが見つかりません"
        exit 1
    fi

    local remote_path=":sftp:${REMOTE_DIR}/"

    rclone copy $(get_rclone_opts) .env "${remote_path}" 2>&1 | grep -v "^$" || true
    echo "  ✓ .env同期完了"
}

# sessions.json の同期（双方向: 新しい方を優先）
sync_sessions_file() {
    local remote_path=":sftp:${REMOTE_DIR}/"
    local rclone_opts=$(get_rclone_opts)

    # リモート → ローカル（リモートの方が新しい場合のみ）
    rclone copy $rclone_opts --update "${remote_path}sessions.json" ./ 2>&1 | grep -v "^$" || true

    # ローカル → リモート（ローカルの方が新しい場合のみ）
    if [ -f "sessions.json" ]; then
        rclone copy $rclone_opts --update sessions.json "${remote_path}" 2>&1 | grep -v "^$" || true
    fi

    echo "  ✓ sessions.json同期完了"
}

# 設定ファイル同期処理
sync_config_files() {
    # .envファイルの同期
    if [ "$COPY_ENV" = true ]; then
        show_step ".envファイルを同期中..."
        sync_env_file
    fi

    # sessions.jsonの同期
    if [ "$COPY_SESSIONS" = true ]; then
        show_step "sessions.jsonを同期中..."
        sync_sessions_file
    fi
}

# ========================================
# メイン処理
# ========================================

main() {
    # rclone の存在確認
    if ! command -v rclone &> /dev/null; then
        echo "エラー: rclone がインストールされていません"
        echo "インストール方法: brew install rclone"
        exit 1
    fi

    echo "=== Mastodon Claude Bot デプロイスクリプト（対話式）==="
    echo ""

    # 質問と計画の実行
    ask_question ".envファイルを同期しますか？" "COPY_ENV"
    ask_question "sessions.jsonファイルを同期しますか？" "COPY_SESSIONS"
    ask_question "プログラムをデプロイしますか？" "DEPLOY_PROGRAM"

    show_plan "COPY_ENV:.envファイルの同期"
    show_plan "COPY_SESSIONS:sessions.jsonファイルの同期"
    show_plan "DEPLOY_PROGRAM:プログラムのビルドとデプロイ"

    echo ""

    # 共通前処理: リモートディレクトリ作成
    show_step "リモートディレクトリを準備中..."
    ssh "${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}"

    # ========================================
    # フェーズ1: ビルド（プログラムデプロイ時のみ）
    # ========================================
    if [ "$DEPLOY_PROGRAM" = true ]; then
        show_step "Linux向けバイナリをビルド中..."
        GOOS=linux GOARCH=amd64 go build -o "${APP_NAME}" .

        if [ ! -f "${APP_NAME}" ]; then
            echo "エラー: ビルドに失敗しました"
            exit 1
        fi

        # 実行権限の付与
        chmod +x "${APP_NAME}"
        echo "  ✓ ビルド完了（実行権限付与済み）"
    fi

    # ========================================
    # フェーズ2: ファイル同期（常に実行）
    # ========================================
    sync_config_files

    # ========================================
    # フェーズ3: デプロイ（プログラムデプロイ時のみ）
    # ========================================
    if [ "$DEPLOY_PROGRAM" = true ]; then
        # Supervisorの停止
        show_step "Supervisorを停止中..."
        ssh "${REMOTE_HOST}" "sudo supervisorctl stop ${APP_NAME}"

        # バイナリの転送
        show_step "バイナリを転送中..."
        scp "${APP_NAME}" "${REMOTE_HOST}:${REMOTE_DIR}/"

        # Supervisorの開始
        show_step "Supervisorを開始中..."
        ssh "${REMOTE_HOST}" "sudo supervisorctl start ${APP_NAME}"
        echo "  ✓ Supervisor開始完了"

        # ローカルのバイナリを削除
        rm "${APP_NAME}"

        echo ""
        echo "=== デプロイ完了 ==="
        echo "ステータス確認: ssh ${REMOTE_HOST} 'supervisorctl status ${APP_NAME}'"
    else
        echo ""
        echo "=== ファイル同期完了 ==="
        echo "注意: プログラムのデプロイは実行されていません"
    fi
}

# ========================================
# スクリプト実行
# ========================================

main "$@"
