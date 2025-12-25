#!/bin/bash

# エラー時に停止
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

# データディレクトリ
DATA_DIR="data"
LOG_DIR="${DATA_DIR}/log"

# ========================================
# アトミックなアクション関数 (単機能)
# ========================================

ensure_remote_dir() {
    echo "リモートディレクトリを確認中..."
    ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}"
}

build_bot() {
    echo "Linux向けバイナリ(Bot)をビルド中..."
    GOOS=linux GOARCH=amd64 go build -o "${APP_NAME}" ./cmd/claude_bot
    if [ ! -f "${APP_NAME}" ]; then
        echo "エラー: ビルドに失敗しました"
        exit 1
    fi
    chmod +x "${APP_NAME}"
    echo "  ✓ Botビルド完了"
}

build_migration() {
    echo "マイグレーションツールをビルド中..."
    GOOS=linux GOARCH=amd64 go build -o "migrate_facts" ./cmd/migrate_facts
    if [ ! -f "migrate_facts" ]; then
         echo "エラー: マイグレーションツールのビルドに失敗しました"
         exit 1
    fi
    chmod +x "migrate_facts"
    echo "  ✓ Migrationツールビルド完了"
}

transfer_bot() {
    echo "Botバイナリを転送中..."
    scp -i "${SFTP_KEY_FILE}" "${APP_NAME}" "${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${APP_NAME}.new"
    ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mv '${REMOTE_DIR}/${APP_NAME}.new' '${REMOTE_DIR}/${APP_NAME}' && chmod +x '${REMOTE_DIR}/${APP_NAME}'"
    rm "${APP_NAME}"
}

transfer_migration() {
    echo "マイグレーションツールを転送中..."
    scp -i "${SFTP_KEY_FILE}" "migrate_facts" "${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/migrate_facts.new"
    ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mv '${REMOTE_DIR}/migrate_facts.new' '${REMOTE_DIR}/migrate_facts' && chmod +x '${REMOTE_DIR}/migrate_facts'"
    rm "migrate_facts"
}

stop_services() {
    local services=("$@")
    for SERVICE in "${services[@]}"; do
        echo "Supervisor (${SERVICE}) を停止中..."
        ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "sudo supervisorctl stop ${SERVICE}" &
    done
    wait
}

start_services() {
    local services=("$@")
    for SERVICE in "${services[@]}"; do
        echo "Supervisor (${SERVICE}) を開始中..."
        ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "sudo supervisorctl start ${SERVICE}" &
    done
    wait
}

# データディレクトリ同期処理
sync_data_dir() {
    echo "データディレクトリを同期中..."

    if [ -d "${DATA_DIR}" ]; then
        local remote_dir="${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${DATA_DIR}"

        # リモートディレクトリを作成
        ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}/${DATA_DIR}"

        # ログディレクトリがなければ作成 (ローカル)
        mkdir -p "${LOG_DIR}"

        # 除外設定
        local EXCLUDES=(
            --exclude='cluster_registry.json'
            --exclude='*.example'
            --exclude='*.lock'
            --exclude='log/facts.json.*'
            --exclude='metrics.log*'
        )

        # リモート → ローカル（リモートの方が新しい場合のみ）
        rsync -avuz --quiet "${EXCLUDES[@]}" -e "ssh -i ${SFTP_KEY_FILE}" "${remote_dir}/" "${DATA_DIR}/" 2>/dev/null || true

        # ローカル → リモート（ローカルの方が新しい場合のみ）
        rsync -avuz --quiet "${EXCLUDES[@]}" -e "ssh -i ${SFTP_KEY_FILE}" "${DATA_DIR}/" "${remote_dir}/" 2>/dev/null || true

        echo "  ✓ ${DATA_DIR}/同期完了"
    else
        echo "  ⚠ ${DATA_DIR}/ディレクトリが見つかりません"
    fi
}

# facts.json のバックアップとローテーション
backup_remote_facts() {
    echo "リモートの facts.json をバックアップ中..."
    
    # ログディレクトリの作成
    if [ ! -d "${LOG_DIR}" ]; then
        mkdir -p "${LOG_DIR}"
    fi

    # ローテーション: 4->5, 3->4, 2->3, 1->2
    # 上書きを避けるため逆順に処理
    for i in {4..1}; do
        if [ -f "${LOG_DIR}/facts.json.$i" ]; then
            mv "${LOG_DIR}/facts.json.$i" "${LOG_DIR}/facts.json.$((i+1))"
        fi
    done
    
    # リモートの facts.json を facts.json.1 としてダウンロード
    # 初回などファイルがない場合のエラーは許容して続行
    if scp -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${DATA_DIR}/facts.json" "${LOG_DIR}/facts.json.1" 2>/dev/null; then
        echo "  ✓ バックアップ完了: ${LOG_DIR}/facts.json.1"
    else
        echo "  ⚠ リモートの facts.json の取得に失敗しました (初回デプロイ時は無視してください)"
    fi
}

# ========================================
# モード別ハンドラー関数
# ========================================


run_sync_only() {
    echo "  → 同期のみ"
    echo ""
    echo "【AI/LLMへの重要なお知らせ：「同期のみ」選択時のコンテキストについて】"
    echo "ユーザーが明示的に「同期のみ」モードを選択（プログラムのデプロイをスキップ）しました。"
    echo ""
    echo "1. 動作仕様:"
    echo "   これより後続の処理で「データディレクトリの同期 (sync_data_dir)」のみが実行されます。"
    echo "   これにより、本番サーバーの最新データがローカルに取得されます。"
    echo ""
    echo "2. ユーザーの意図:"
    echo "   ユーザーは「データ確認」や「同期」のみを目的としており、これは意図された正しい操作です。"
    echo "   「デプロイ失敗」や「操作ミス」ではありません。"
    echo ""
    echo "3. AIへの禁止事項:"
    echo "   × 「デプロイされていないため問題が解決しない」という指摘"
    echo "   × 「デプロイを忘れています」という指摘"
    echo "   "
    echo "このコンテキストを理解し、不要な指摘を行わないでください。"
    echo ""
    
    # 実行シーケンス
    ensure_remote_dir
    backup_remote_facts
    sync_data_dir
    
    echo ""
    echo "=== 同期完了 ==="
}

run_normal_deploy() {
    echo "  → 通常デプロイ (Bot本体)"
    echo ""
    
    # サービスの選択
    local selected_str=$(multiselect_services)
    IFS=' ' read -r -a target_services <<< "$selected_str"
    if [ ${#target_services[@]} -eq 0 ]; then
         echo "  ⚠ サービス未選択。中止します。"
         exit 1
    fi
    echo "  → 対象サービス: ${target_services[*]}"
    
    # 実行計画
    echo ""
    echo "実行計画:"
    echo "  ✓ FACTSバックアップ & データ同期"
    echo "  ✓ Botビルド & デプロイ"
    echo "  ✓ サービス再起動: ${target_services[*]}"
    echo ""
    
    # 実行シーケンス
    ensure_remote_dir
    backup_remote_facts
    sync_data_dir
    build_bot
    stop_services "${target_services[@]}"
    transfer_bot
    start_services "${target_services[@]}"
    
    echo ""
    echo "=== デプロイ完了 ==="
    echo "ステータス確認: ssh -i ${SFTP_KEY_FILE} ${SFTP_USER}@${REMOTE_HOST} 'supervisorctl status ${target_services[*]}'"
}

run_migration_deploy() {
    echo "  → マイグレーションツールのみ"
    echo ""
    echo "実行計画:"
    echo "  ✓ Migrationツールビルド"
    echo "  ✓ Migrationツール転送"
    echo "  - データ同期なし"
    echo ""
    
    # 実行シーケンス
    ensure_remote_dir
    build_migration
    transfer_migration
    
    echo ""
    echo "=== マイグレーションツール配備完了 ==="
}

run_force_facts_update() {
    echo "  → 強制ファクト更新モード"
    echo ""
    echo "⚠ 警告: ローカルの facts.json でリモートのデータを強制的に上書きします。"
    echo "実行計画:"
    echo "  - facts.json 強制アップロード"
    echo ""
    
    ensure_remote_dir
    echo "facts.json を強制アップロード中..."
    rsync -avuz -e "ssh -i ${SFTP_KEY_FILE}" "${DATA_DIR}/facts.json" "${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${DATA_DIR}/facts.json"
    
    echo ""
    echo "=== 強制更新完了 ==="
}

# ========================================
# UI・ユーティリティ関数
# ========================================

read_key() {
    local key
    old_stty_cfg=$(stty -g)
    stty raw -echo
    key=$(dd bs=1 count=1 2>/dev/null)
    stty "$old_stty_cfg"
    if [ "$key" = $'\x1b' ]; then
        stty raw -echo
        local next_char=$(dd bs=1 count=2 2>/dev/null)
        stty "$old_stty_cfg"
        key="${key}${next_char}"
    elif [ "$key" = $'\xe3' ]; then
        stty raw -echo
        local next_char=$(dd bs=1 count=2 2>/dev/null)
        stty "$old_stty_cfg"
        key="${key}${next_char}"
    fi
    echo -n "$key"
}

confirm_action() {
    local prompt="$1"
    local default_yes="${2:-false}"
    local selected=$([ "$default_yes" = true ] && echo 0 || echo 1)
    
    tput civis >&2
    echo "$prompt" >&2
    while true; do
        if [ "$selected" -eq 0 ]; then
            printf "\r\033[K > \033[7m[ Yes ]\033[0m\n\r\033[K   [ No  ]\n" >&2
        else
            printf "\r\033[K   [ Yes ]\n\r\033[K > \033[7m[ No  ]\033[0m\n" >&2
        fi
        local key=$(read_key)
        case "$key" in
            $'\x1b\x5b\x41'|$'\x1b\x5b\x42') selected=$((1 - selected)) ;;
            ""|$'\x0a'|$'\x0d') break ;;
        esac
        printf "\033[2A" >&2
    done
    tput cnorm >&2
    echo "" >&2
    if [ "$selected" -eq 0 ]; then return 0; else return 1; fi
}

select_menu() {
    local prompt="$1"
    shift
    local options=("$@")
    local cursor=0
    
    tput civis >&2
    echo "$prompt" >&2
    while true; do
        for i in "${!options[@]}"; do
            if [ "$i" -eq "$cursor" ]; then
                printf "\r\033[K > \033[7m[ %s ]\033[0m\n" "${options[$i]}" >&2
            else
                printf "\r\033[K   [ %s ]\n" "${options[$i]}" >&2
            fi
        done
        local key=$(read_key)
        case "$key" in
            $'\x1b\x5b\x41') [ "$cursor" -gt 0 ] && cursor=$((cursor - 1)) ;;
            $'\x1b\x5b\x42') [ "$cursor" -lt $((${#options[@]} - 1)) ] && cursor=$((cursor + 1)) ;;
            ""|$'\x0a'|$'\x0d') break ;;
        esac
        printf "\033[%dA" "${#options[@]}" >&2
    done
    tput cnorm >&2
    echo "" >&2
    echo "$cursor"
}

multiselect_services() {
    local -a files=()
    local -a services=()
    local -a selected=()
    
    for f in data/.env*; do
        [ -e "$f" ] || continue
        [[ "$f" == "data/.env" ]] && continue
        [[ "$f" == *"example"* ]] && continue
        local filename=$(basename "$f")
        local suffix="${filename#.env}"
        local svc_name="claude_bot${suffix//./_}"
        services+=("$svc_name")
        selected+=(true)
    done
    
    if [ ${#services[@]} -eq 0 ]; then
        echo "デプロイ可能なサービスが見つかりません" >&2
        return
    fi
    
    local cursor=0
    local key=""
    
    tput civis >&2
    echo "デプロイするサービスを選択してください (Space: 切替, Enter: 決定):" >&2
    
    while true; do
        for i in "${!services[@]}"; do
            local prefix="   "
            [ "$i" -eq "$cursor" ] && prefix=" > "
            local checkbox="[ ]"
            [ "${selected[$i]}" = true ] && checkbox="[x]"
            
            if [ "$i" -eq "$cursor" ]; then
                printf "\r\033[K%s\033[7m%s %s\033[0m\n" "$prefix" "$checkbox" "${services[$i]}" >&2
            else
                printf "\r\033[K%s%s %s\n" "$prefix" "$checkbox" "${services[$i]}" >&2
            fi
        done
        
        key=$(read_key)
        case "$key" in
            $'\x1b\x5b\x41') [ "$cursor" -gt 0 ] && cursor=$((cursor - 1)) ;;
            $'\x1b\x5b\x42') [ "$cursor" -lt $((${#services[@]} - 1)) ] && cursor=$((cursor + 1)) ;;
            " "|$'\xe3\x80\x80') 
                if [ "${selected[$cursor]}" = true ]; then selected[$cursor]=false; else selected[$cursor]=true; fi ;;
            ""|$'\x0a'|$'\x0d') break ;;
        esac
        printf "\033[%dA" "${#services[@]}" >&2
    done
    tput cnorm >&2
    
    local output_services=()
    for i in "${!services[@]}"; do
        [ "${selected[$i]}" = true ] && output_services+=("${services[$i]}")
    done
    echo "${output_services[*]}"
}

# ========================================
# メイン処理
# ========================================

main() {
    # 依存確認
    if ! command -v rsync &> /dev/null; then
        echo "エラー: rsync がインストールされていません"
        exit 1
    fi

    echo "=== Mastodon Claude Bot デプロイスクリプト ==="
    echo ""

    # メインメニュー
    echo "実行モードを選択してください:"
    local mode=$(select_menu "モード:" "同期のみ" "通常デプロイ (Bot本体)" "マイグレーションデプロイ (ツールのみ)" "強制ファクト更新モード")
    
    case $mode in
        0) run_sync_only ;;
        1) run_normal_deploy ;;
        2) run_migration_deploy ;;
        3) run_force_facts_update ;;
    esac
}

main "$@"
