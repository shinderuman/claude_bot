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

# データディレクトリ
DATA_DIR="data"

# デフォルト値
DEPLOY_PROGRAM=false

# ========================================
# 関数定義
# ========================================

# データディレクトリ同期処理
sync_data_dir() {
    echo "データディレクトリを同期中..."

    if [ -d "${DATA_DIR}" ]; then
        local remote_dir="${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${DATA_DIR}"

        # リモートディレクトリを作成
        ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}/${DATA_DIR}"

        # リモート → ローカル（リモートの方が新しい場合のみ）
        rsync -avuz --quiet --exclude='*.example' -e "ssh -i ${SFTP_KEY_FILE}" "${remote_dir}/" "${DATA_DIR}/" 2>/dev/null || true

        # ローカル → リモート（ローカルの方が新しい場合のみ）
        rsync -avuz --quiet --exclude='*.example' -e "ssh -i ${SFTP_KEY_FILE}" "${DATA_DIR}/" "${remote_dir}/" 2>/dev/null || true

        echo "  ✓ ${DATA_DIR}/同期完了"
    else
        echo "  ⚠ ${DATA_DIR}/ディレクトリが見つかりません"
    fi
}

# キー入力取得関数
read_key() {
    local key
    old_stty_cfg=$(stty -g)
    stty raw -echo
    key=$(dd bs=1 count=1 2>/dev/null)
    stty "$old_stty_cfg"
    
    # エスケープシーケンスの処理
    if [ "$key" = $'\x1b' ]; then
        stty raw -echo
        local next_char=$(dd bs=1 count=2 2>/dev/null)
        stty "$old_stty_cfg"
        key="${key}${next_char}"
    # UTF-8 3バイト文字の処理 (例: 全角スペース \xe3\x80\x80)
    elif [ "$key" = $'\xe3' ]; then
        stty raw -echo
        local next_char=$(dd bs=1 count=2 2>/dev/null)
        stty "$old_stty_cfg"
        key="${key}${next_char}"
    fi
    echo -n "$key"
}


# Yes/No 選択プロンプト（矢印キー対応・縦配置）
confirm_action() {
    local prompt="$1"
    local default_yes="${2:-false}" # true for Yes default, false for No default
    local selected=$([ "$default_yes" = true ] && echo 0 || echo 1) # 0: Yes, 1: No
    
    # カーソル非表示
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
            $'\x1b\x5b\x41'|$'\x1b\x5b\x42') # Up or Down Arrow
                selected=$((1 - selected))
                ;;
            "") # Enter
                break
                ;;
            $'\x0a'|$'\x0d') # Enter
                break
                ;;
        esac
        
        # カーソルをメニューの先頭に戻す (2行分)
        printf "\033[2A" >&2
    done
    
    # カーソル表示再開と改行
    tput cnorm >&2
    echo "" >&2
    
    if [ "$selected" -eq 0 ]; then
        return 0 # True (Yes)
    else
        return 1 # False (No)
    fi
}

# サービス多重選択メニュー
multiselect_services() {
    local -a files=()
    local -a services=()
    local -a selected=()
    
    # .envファイルの検出とリスト化
    # data/.env* -> claude_bot OR claude_bot_suffix
    for f in data/.env*; do
        [ -e "$f" ] || continue
        [[ "$f" == *"example"* ]] && continue # exampleは除外
        
        local filename=$(basename "$f")
        local suffix="${filename#.env}"
        local svc_name="claude_bot${suffix//./_}"
        
        files+=("$f")
        services+=("$svc_name")
        selected+=(true)
    done
    
    if [ ${#services[@]} -eq 0 ]; then
        echo "デプロイ可能なサービスが見つかりません" >&2
        return
    fi
    
    local cursor=0
    local key=""
    
    # カーソル非表示
    tput civis >&2
    
    echo "デプロイするサービスを選択してください (Space: 切替, Enter: 決定):" >&2
    
    # 選択ループ
    while true; do
        # リストの描画
        for i in "${!services[@]}"; do
            local prefix="   "
            if [ "$i" -eq "$cursor" ]; then
                prefix=" > "
            fi
            
            local checkbox="[ ]"
            if [ "${selected[$i]}" = true ]; then
                checkbox="[x]"
            fi
            
            # 色付きで表示（選択行はハイライト）
            if [ "$i" -eq "$cursor" ]; then
                printf "\r\033[K%s\033[7m%s %s\033[0m\n" "$prefix" "$checkbox" "${services[$i]}" >&2
            else
                printf "\r\033[K%s%s %s\n" "$prefix" "$checkbox" "${services[$i]}" >&2
            fi
        done
        
        # キー入力待機
        key=$(read_key)
        
        case "$key" in
            $'\x1b\x5b\x41') # Up Arrow
                if [ "$cursor" -gt 0 ]; then
                    cursor=$((cursor - 1))
                fi
                ;;
            $'\x1b\x5b\x42') # Down Arrow
                if [ "$cursor" -lt $((${#services[@]} - 1)) ]; then
                    cursor=$((cursor + 1))
                fi
                ;;
            " "|$'\xe3\x80\x80') # Space (半角/全角)
                if [ "${selected[$cursor]}" = true ]; then
                    selected[$cursor]=false
                else
                    selected[$cursor]=true
                fi
                ;;
            "") # Enter
                break
                ;;
            $'\x0a'|$'\x0d') # Enter
                break
                ;;
        esac
        
        # カーソルをリストの先頭に戻す
        printf "\033[%dA" "${#services[@]}" >&2
    done
    
    # カーソル表示再開
    tput cnorm >&2
    
    # 選択されたサービスを出力
    local output_services=()
    for i in "${!services[@]}"; do
        if [ "${selected[$i]}" = true ]; then
            output_services+=("${services[$i]}")
        fi
    done
    
    echo "${output_services[*]}"
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

    # 引数チェック: --force-facts
    if [[ "$1" == "--force-facts" ]]; then
        echo "⚠ 強制ファクト更新モード (--force-facts)"
        echo "ローカルの facts.json でリモートを強制上書きします。"
        echo "他のデプロイ・同期処理はスキップされます。"
        echo ""
        
        if confirm_action "本当に実行しますか？" false; then
            echo "facts.json を強制アップロード中..."
            rsync -avuz -e "ssh -i ${SFTP_KEY_FILE}" "${DATA_DIR}/facts.json" "${SFTP_USER}@${REMOTE_HOST}:${REMOTE_DIR}/${DATA_DIR}/facts.json"
            echo "完了しました。"
            exit 0
        else
            echo "中止しました。"
            exit 1
        fi
    fi

    # デプロイ対象サービスの初期化
    local target_services=()

    # 質問と計画の実行
    if confirm_action "プログラムをデプロイしますか？"; then
        DEPLOY_PROGRAM=true
        echo ""
        
        # 多重選択メニューの実行
        local selected_str=$(multiselect_services)
        IFS=' ' read -r -a target_services <<< "$selected_str"
        
        if [ ${#target_services[@]} -eq 0 ]; then
             echo "  ⚠ サービスが選択されませんでした。デプロイを中止します。"
             exit 1
        fi
        
        echo ""
        echo "  → 対象サービス: ${target_services[*]}"
    else
        DEPLOY_PROGRAM=false
    fi

    echo ""
    echo "実行計画:"
    if [ "$DEPLOY_PROGRAM" = true ]; then
        echo "  ✓ プログラムのビルドとデプロイ"
    else
        echo "  ✗ プログラムのビルドとデプロイ（スキップ）"
    fi
    echo "  ✓ データディレクトリの同期"

    echo ""

    # 共通前処理: リモートディレクトリ作成
    echo "リモートディレクトリを準備中..."
    ssh "${REMOTE_HOST}" "mkdir -p ${REMOTE_DIR}"

    # ========================================
    # フェーズ1: ビルド（プログラムデプロイ時のみ）
    # ========================================
    if [ "$DEPLOY_PROGRAM" = true ]; then
        echo "Linux向けバイナリをビルド中..."
        GOOS=linux GOARCH=amd64 go build -o "${APP_NAME}" ./cmd/claude_bot

        if [ ! -f "${APP_NAME}" ]; then
            echo "エラー: ビルドに失敗しました"
            exit 1
        fi

        # 実行権限の付与
        chmod +x "${APP_NAME}"
        echo "  ✓ ビルド完了（実行権限付与済み）"
    fi

    # ========================================
    # フェーズ2: データディレクトリ同期
    # ========================================
    sync_data_dir

    # ========================================
    # フェーズ3: デプロイ（プログラムデプロイ時のみ）
    # ========================================
    if [ "$DEPLOY_PROGRAM" = true ]; then
        # Supervisorの停止
        for SERVICE in "${target_services[@]}"; do
            echo "Supervisor (${SERVICE}) を停止中..."
            ssh "${REMOTE_HOST}" "sudo supervisorctl stop ${SERVICE}" || echo "  ⚠ ${SERVICE} の停止に失敗しました（プロセスが存在しない可能性があります）"
        done

        # バイナリの転送 (Text file busy回避のため一時ファイル経由)
        echo "バイナリを転送中..."
        scp "${APP_NAME}" "${REMOTE_HOST}:${REMOTE_DIR}/${APP_NAME}.new"
        
        # バイナリの置き換え (アトミック操作)
        ssh "${REMOTE_HOST}" "mv '${REMOTE_DIR}/${APP_NAME}.new' '${REMOTE_DIR}/${APP_NAME}' && chmod +x '${REMOTE_DIR}/${APP_NAME}'"

        # Supervisorの開始
        for SERVICE in "${target_services[@]}"; do
            echo "Supervisor (${SERVICE}) を開始中..."
            ssh "${REMOTE_HOST}" "sudo supervisorctl start ${SERVICE}"
        done
        echo "  ✓ Supervisor操作完了"

        # ローカルのバイナリを削除
        rm "${APP_NAME}"

        echo ""
        echo "=== デプロイ完了 ==="
        echo "ステータス確認: ssh ${REMOTE_HOST} 'supervisorctl status ${target_services[*]}'"
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
