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

# 設定ファイルリスト
CONFIG_FILES=(".env" "sessions.json" "facts.json")

# デフォルト値
DEPLOY_PROGRAM=false

# ========================================
# 関数定義
# ========================================

# 設定ファイル同期処理
sync_config_files() {
    echo "設定ファイルを同期中..."

    for file in "${CONFIG_FILES[@]}"; do
        if [ -f "$file" ]; then
            local remote_file="${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${file}"

            # リモート → ローカル（リモートの方が新しい場合のみ）
            rsync -avuz --quiet -e "ssh -i ${SFTP_KEY_FILE}" "${remote_file}" ./ 2>/dev/null || true

            # ローカル → リモート（ローカルの方が新しい場合のみ）
            rsync -avuz --quiet -e "ssh -i ${SFTP_KEY_FILE}" "${file}" "${remote_file}" 2>/dev/null || true

            echo "  ✓ ${file}同期完了"
        fi
    done
}

# ========================================
# メイン処理
# ========================================

main() {
    # rsync の存在確認
    if ! command -v rsync &> /dev/null; then
        echo "エラー: rsync がインストールされていません"
        echo ""
        echo "インストール方法:"
        echo "  Ubuntu/Debian: sudo apt-get install rsync"
        echo ""
        echo "注意: macOSには標準で含まれています"
        exit 1
    fi

    echo "=== Mastodon Claude Bot デプロイスクリプト ==="
    echo ""

    # 質問と計画の実行
    echo -n "プログラムをデプロイしますか？ [y/N]: "
    read -r response
    case $response in
        [yY]|[yY][eE][sS])
            DEPLOY_PROGRAM=true
            echo "  → プログラムをデプロイします"
            ;;
        *)
            DEPLOY_PROGRAM=false
            ;;
    esac

    echo ""
    echo "実行計画:"
    if [ "$DEPLOY_PROGRAM" = true ]; then
        echo "  ✓ プログラムのビルドとデプロイ"
    else
        echo "  ✗ プログラムのビルドとデプロイ（スキップ）"
    fi
    echo "  ✓ 設定ファイルの同期"

    echo ""

    # 共通前処理: リモートディレクトリ作成
    echo "リモートディレクトリを準備中..."
    ssh "${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}"

    # ========================================
    # フェーズ1: ビルド（プログラムデプロイ時のみ）
    # ========================================
    if [ "$DEPLOY_PROGRAM" = true ]; then
        echo "Linux向けバイナリをビルド中..."
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
    # フェーズ2: ファイル同期
    # ========================================
    sync_config_files

    # ========================================
    # フェーズ3: デプロイ（プログラムデプロイ時のみ）
    # ========================================
    if [ "$DEPLOY_PROGRAM" = true ]; then
        # Supervisorの停止
        echo "Supervisorを停止中..."
        ssh "${REMOTE_HOST}" "sudo supervisorctl stop ${APP_NAME}"

        # バイナリの転送
        echo "バイナリを転送中..."
        scp "${APP_NAME}" "${REMOTE_HOST}:${REMOTE_DIR}/"

        # Supervisorの開始
        echo "Supervisorを開始中..."
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
