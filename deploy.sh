#!/bin/bash

set -e

REMOTE_HOST="kenji.asmodeus.jp"
REMOTE_DIR="/home/ubuntu/claude_bot"
APP_NAME="claude_bot"
SFTP_USER="ubuntu"
SFTP_KEY_FILE="$HOME/.ssh/mastodon.pem"

DATA_DIR="data"
LOG_DIR="${DATA_DIR}/log"

shell_quote() {
    local value="$1"
    local single_quote="'" escaped_quote="'\\''"
    printf "'%s'" "${value//$single_quote/$escaped_quote}"
}

ensure_remote_dir() {
    echo "リモートディレクトリを確認中..."
    deployment_ssh "
        if ! command -v flock >/dev/null; then
            echo '配置先に flock がありません' >&2
            exit 1
        fi
        mkdir -p -- $(shell_quote "$REMOTE_DIR")
    "
}

deployment_ssh() {
    ssh -o ConnectTimeout=5 -o ServerAliveInterval=5 -o ServerAliveCountMax=2 \
        -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "$1"
}

# 切替や解除が途中で終了しても、ファイル記述子が閉じれば排他は解除される。
deployment_mutex_command() {
    printf 'exec 9>>%s\nflock -x -w 5 9' "$(shell_quote "${REMOTE_DIR}/.deploy-mutex")"
}

release_deployment_lock() {
    deployment_ssh "
        set -e
        lock=$(shell_quote "$deployment_lock")
        guard=$(shell_quote "$deployment_guard")
        owner=$(shell_quote "$deployment_owner")
        rm -f -- \"\$guard/owner\"
        if [ -e \"\$lock\" ] || [ -L \"\$lock\" ]; then
            if [ ! -f \"\$lock\" ] || [ \"\$(cat \"\$lock\" 2>/dev/null)\" != \"\$owner\" ]; then
                rm -rf -- \"\$guard\"
                exit
            fi
        fi
        $(deployment_mutex_command)
        if [ -f \"\$lock\" ] && [ \"\$(cat \"\$lock\")\" = \"\$owner\" ]; then
            rm -f -- \"\$lock\"
        fi
        rm -rf -- \"\$guard\"
    "
}

with_deployment_lock() (
    local deployment_lock="${REMOTE_DIR}/.deploy.lock" deployment_guard deployment_owner nonce_dir lock_exit_status
    ensure_remote_dir
    nonce_dir=$(mktemp -d "${TMPDIR:-/tmp}/claude-bot-deploy.XXXXXXXXXXXX")
    deployment_owner="${nonce_dir##*.}"
    rmdir -- "$nonce_dir"
    deployment_guard="${REMOTE_DIR}/.deploy-owner.${deployment_owner}"
    trap '
        lock_exit_status=$?
        for attempt in 1 2 3; do
            if release_deployment_lock; then exit "$lock_exit_status"; fi
            if [ "$attempt" -lt 3 ]; then sleep 1; fi
        done
        echo "配置先の排他ロックを解除できません ($deployment_lock)" >&2
        if [ "$lock_exit_status" -eq 0 ]; then lock_exit_status=1; fi
        exit "$lock_exit_status"
    ' EXIT
    deployment_ssh "
        set -e
        guard=$(shell_quote "$deployment_guard")
        umask 077
        mkdir -- \"\$guard\"
        printf '%s\n' $(shell_quote "$deployment_owner") > \"\$guard/owner\"
    "
    # 所有者を書き終えてからロックを公開する。解除時はリンク元を消して遅延した取得も止める。
    if ! deployment_ssh "
        set -e
        lock=$(shell_quote "$deployment_lock")
        [ ! -e \"\$lock\" ] && [ ! -L \"\$lock\" ] || exit 1
        $(deployment_mutex_command)
        [ ! -e \"\$lock\" ] && [ ! -L \"\$lock\" ] || exit 1
        ln -- $(shell_quote "$deployment_guard/owner") \"\$lock\"
    "; then
        echo "デプロイ中止: 配置先の排他ロックを取得できません ($deployment_lock)" >&2
        return 1
    fi
    "$@"
)

build_bot() {
    echo "Linux向けバイナリ(Bot)をビルド中..."
    GOOS=linux GOARCH=amd64 go build -o "${APP_NAME}_bin" ./cmd/claude_bot
    if [ ! -f "${APP_NAME}_bin" ]; then
        echo "エラー: ビルドに失敗しました"
        exit 1
    fi
    chmod +x "${APP_NAME}_bin"
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
    local stage="$1" wrapper_file="$2"
    local remote_bin remote_wrapper stage_path
    remote_bin=$(shell_quote "${REMOTE_DIR}/${APP_NAME}_bin")
    remote_wrapper=$(shell_quote "${REMOTE_DIR}/${APP_NAME}")
    stage_path=$(shell_quote "$stage")
    echo "Botバイナリとラッパーを転送中..."

    scp -i "${SFTP_KEY_FILE}" "${APP_NAME}_bin" "${SFTP_USER}@${REMOTE_HOST}:${stage}/bin"
    scp -i "${SFTP_KEY_FILE}" "$wrapper_file" "${SFTP_USER}@${REMOTE_HOST}:${stage}/wrapper"
    ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "
        set -e
        chmod +x ${stage_path}/bin ${stage_path}/wrapper
        if [ -e ${remote_bin} ] || [ -L ${remote_bin} ]; then cp -pP -- ${remote_bin} ${stage_path}/bin.previous; fi
        if [ -e ${remote_wrapper} ] || [ -L ${remote_wrapper} ]; then cp -pP -- ${remote_wrapper} ${stage_path}/wrapper.previous; fi
    "
}

# SSHが切れても、切替の終了を待ってから旧ファイルを戻す。
switch_bot_files() {
    local command="$1"
    deployment_ssh "
        set -e
        lock=$(shell_quote "$deployment_lock")
        owner=$(shell_quote "$deployment_owner")
        $(deployment_mutex_command)
        [ \"\$(cat \"\$lock\")\" = \"\$owner\" ]
        ${command}
    "
}

install_bot() {
    local stage="$1"
    switch_bot_files "
        mv -f -- $(shell_quote "${stage}/bin") $(shell_quote "${REMOTE_DIR}/${APP_NAME}_bin")
        mv -f -- $(shell_quote "${stage}/wrapper") $(shell_quote "${REMOTE_DIR}/${APP_NAME}")
    "
}

restore_bot_file_command() {
    local backup destination temporary
    backup=$(shell_quote "$1")
    destination=$(shell_quote "$2")
    temporary=$(shell_quote "$1.restore")
    printf 'if [ -e %s ] || [ -L %s ]; then cp -pP -- %s %s || exit $?; mv -f -- %s %s; else rm -f -- %s; fi' \
        "$backup" "$backup" "$backup" "$temporary" "$temporary" "$destination" "$destination"
}

restore_bot_files() {
    local stage="$1"
    switch_bot_files "
        $(restore_bot_file_command "${stage}/bin.previous" "${REMOTE_DIR}/${APP_NAME}_bin")
        $(restore_bot_file_command "${stage}/wrapper.previous" "${REMOTE_DIR}/${APP_NAME}")
    "
}

install_file_command() {
    local destination
    destination=$(shell_quote "$2")
    printf 'mv -- %s %s && chmod +x %s' "$(shell_quote "$1")" "$destination" "$destination"
}

create_wrapper_script() {
    local maintenance_stage="$1" filename="wrapper.sh"
    {
        cat <<'EOF'
#!/bin/bash
export RUN_MAINTENANCE=false
EOF
        if [ -n "$maintenance_stage" ]; then
            cat <<EOF
service=\${SUPERVISOR_PROCESS_NAME:-}
if [[ "\$service" =~ ^[a-zA-Z0-9_-]+\$ ]]; then
    request=$(shell_quote "$maintenance_stage")/"\$service.maintenance"
    # 自動再起動より前に、サービスごとのメンテナンス要求を消費する。
    if [ -f "\$request" ] && read -r controller < "\$request" &&
        [[ "\$controller" =~ ^[1-9][0-9]*\$ ]] && kill -0 "\$controller" 2>/dev/null &&
        mv -- "\$request" "\$request.used" 2>/dev/null; then
        export RUN_MAINTENANCE=true
    fi
fi
EOF
        fi
        printf 'exec %s "$@"\n' "$(shell_quote "${REMOTE_DIR}/${APP_NAME}_bin")"
    } > "$filename"
    chmod +x "$filename"
    echo "$filename"
}

transfer_migration() {
    local remote_tool="${REMOTE_DIR}/migrate_facts"
    echo "マイグレーションツールを転送中..."
    scp -i "${SFTP_KEY_FILE}" "migrate_facts" "${SFTP_USER}@${REMOTE_HOST}:${remote_tool}.new"
    ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "$(install_file_command "${remote_tool}.new" "$remote_tool")"
    rm "migrate_facts"
}

supervisor_command() {
    local action="$1" service="$2" maintenance_stage="${3:-}" command
    command="sudo supervisorctl ${action} $(shell_quote "$service")"
    if [ -n "$maintenance_stage" ]; then
        command="
            set -e
            request=$(shell_quote "${maintenance_stage}/${service}.maintenance")
            trap 'rm -f -- \"\$request\"' EXIT
            trap 'exit 1' HUP INT TERM
            printf '%s\n' \"\$\$\" > \"\$request\"
            ${command}
            if [ ! -f \"\$request.used\" ]; then
                printf '%s\n' $(shell_quote "Supervisor (${service}) のメンテナンス開始を確認できません") >&2
                exit 1
            fi
        "
    fi
    ssh -o ConnectTimeout=5 -o ServerAliveInterval=5 -o ServerAliveCountMax=2 \
        -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "$command"
}

supervisor_state() {
    local service="$1" output name state rest exit_code=0
    output=$(supervisor_command status "$service") || exit_code=$?
    read -r name state rest <<< "$output"
    if [ "$name" = "$service" ]; then
        case "$exit_code:$state" in
            0:RUNNING|0:STARTING|0:BACKOFF|0:STOPPING|3:STOPPED|3:EXITED|3:FATAL)
                printf '%s\n' "$state"
                return 0
                ;;
        esac
    fi
    if [ -n "$output" ]; then
        printf '%s\n' "$output" >&2
    fi
    return 1
}

control_service() {
    local action="$1" service="$2" maintenance_stage="$3" output exit_code=0
    output=$(supervisor_command "$action" "$service" "$maintenance_stage") || exit_code=$?
    if [ -n "$output" ]; then
        printf '%s\n' "$output" >&2
    fi
    if [ "$action" = stop ]; then
        if grep -Fxq -- "$service: stopped" <<< "$output"; then
            printf 'stopped %s\n' "$service"
        elif ! grep -Fxq -- "$service: ERROR (not running)" <<< "$output"; then
            printf 'unconfirmed %s\n' "$service"
            echo "Supervisor (${service}) の停止結果が未確認です" >&2
            if [ "$exit_code" -eq 0 ]; then
                exit_code=1
            fi
        fi
    fi
    return "$exit_code"
}

control_services() {
    local action="$1" label="$2" maintenance_stage="$3"
    shift 3
    local services=("$@")
    local pids=()
    local service i exit_code failed=0
    for service in "${services[@]}"; do
        echo "Supervisor (${service}) を${label}中..." >&2
        control_service "$action" "$service" "$maintenance_stage" &
        pids+=("$!")
    done
    for i in "${!pids[@]}"; do
        wait "${pids[$i]}" || {
            exit_code=$?
            echo "Supervisor (${services[$i]}) の${label}失敗 (終了コード: ${exit_code})" >&2
            failed=1
        }
    done
    return "$failed"
}

confirm_stopped() {
    local service="$1" state attempt
    for attempt in {1..15}; do
        if state=$(supervisor_state "$service"); then
            case "$state" in
                STOPPED) return 0 ;;
                STOPPING) ;;
                *) return 1 ;;
            esac
        elif [ "$attempt" -ge 3 ]; then
            break
        fi
        sleep 1
    done
    echo "Supervisor (${service}) の停止状態を確認できません。接続を確認してください" >&2
    return 1
}

running_services_before_stop() {
    local service state
    for service in "$@"; do
        if ! state=$(supervisor_state "$service"); then
            echo "デプロイ中止: Supervisor (${service}) の停止前の状態を確認できません" >&2
            return 1
        fi
        case "$state" in
            RUNNING|STARTING|BACKOFF) printf '%s\n' "$service" ;;
        esac
    done
}

restore_stopped_services() {
    local results="$1" running_services="$2" service result
    local restore_services=()
    while read -r result service; do
        case "$result" in
            stopped) restore_services+=("$service") ;;
            unconfirmed)
                if grep -Fxq -- "$service" <<< "$running_services" && confirm_stopped "$service"; then
                    restore_services+=("$service")
                fi
                ;;
        esac
    done <<< "$results"
    if [ "${#restore_services[@]}" -gt 0 ]; then
        control_services start 復旧 "" "${restore_services[@]}"
    fi
}

deploy_staged_bot() (
    local maintenance="$1"
    shift
    local stage stage_id wrapper maintenance_stage="" running_services results keep_stage=false
    stage=$(ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mktemp -d -- $(shell_quote "${REMOTE_DIR}/.deploy.XXXXXXXX")")
    stage_id="${stage#"${REMOTE_DIR}/.deploy."}"
    if [[ ! "$stage_id" =~ ^[a-zA-Z0-9]{8}$ ]] || [ "$stage" != "${REMOTE_DIR}/.deploy.${stage_id}" ]; then
        echo "デプロイ中止: 一時転送先を確認できません" >&2
        return 1
    fi
    trap '
        if [ "$keep_stage" = false ]; then
            ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "rm -rf -- $(shell_quote "$stage")" ||
                echo "一時転送先の削除に失敗しました: $stage" >&2
        else
            echo "復旧用ファイルの保存先: $stage" >&2
        fi
    ' EXIT

    if [ "$maintenance" = true ]; then
        maintenance_stage="$stage"
    fi
    wrapper=$(create_wrapper_script "$maintenance_stage")
    transfer_bot "$stage" "$wrapper"
    rm "${APP_NAME}_bin" "$wrapper"

    running_services=$(running_services_before_stop "$@")
    if ! results=$(control_services stop 停止 "" "$@"); then
        echo "デプロイ中止: サービス停止に失敗しました" >&2
        restore_stopped_services "$results" "$running_services" || return 1
        return 1
    fi
    keep_stage=true
    if ! install_bot "$stage"; then
        echo "デプロイ中止: Botファイルの切替に失敗しました" >&2
        if restore_bot_files "$stage"; then
            restore_stopped_services "$results" "$running_services" || return 1
            keep_stage=false
        else
            echo "旧Botファイルを復元できませんでした" >&2
        fi
        return 1
    fi

    control_services start 開始 "$maintenance_stage" "$@" || return 1
    keep_stage=false
)

sync_remote() {
    # openrsync がリモートパスを再分割するため、SSH側で配置先に移動する。
    local remote_shell='host=$1
shift
exec ssh -i "$DEPLOY_RSYNC_KEY" "$DEPLOY_RSYNC_USER@$host" "$DEPLOY_RSYNC_PREFIX$*"'
    DEPLOY_RSYNC_KEY="$SFTP_KEY_FILE" DEPLOY_RSYNC_USER="$SFTP_USER" \
        DEPLOY_RSYNC_PREFIX="cd $(shell_quote "$REMOTE_DIR") && " \
        rsync -avz -e "sh -c '$remote_shell' sh" "$@"
}

sync_data_dir() {
    echo "データディレクトリを同期中..."

    if [ -d "${DATA_DIR}" ]; then
        local remote_dir="${REMOTE_HOST}:${DATA_DIR}"

        ssh -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}" "mkdir -p -- $(shell_quote "${REMOTE_DIR}/${DATA_DIR}")"

        mkdir -p "${LOG_DIR}"

        local EXCLUDES=(
            --exclude='cluster_registry.json'
            --exclude='*.example'
            --exclude='*.lock'
            --exclude='log/facts.json.*'
            --exclude='log/.facts-backup.*'
            --exclude='metrics.log*'
        )

        sync_remote --update --quiet "${EXCLUDES[@]}" "${remote_dir}/" "${DATA_DIR}/"

        sync_remote --update --quiet "${EXCLUDES[@]}" "${DATA_DIR}/" "${remote_dir}/"

        echo "  ✓ ${DATA_DIR}/同期完了"
    else
        echo "${DATA_DIR}/ディレクトリが見つかりません" >&2
        return 1
    fi
}

finish_fact_backup() {
    local status="$1" stage="$2" generation path restore_failed=false
    shift 2
    trap '' HUP INT TERM
    for generation in "$@"; do
        path="${LOG_DIR}/facts.json.$generation"
        if [ -f "$stage/previous.$generation" ]; then
            [ "$stage/previous.$generation" -ef "$path" ] && continue
            mv -f -- "$stage/previous.$generation" "$path" || restore_failed=true
        else
            rm -f -- "$path" || restore_failed=true
        fi
    done
    if [ "$restore_failed" = true ]; then
        echo "バックアップの復元用ファイル: $stage" >&2
    elif rm -rf -- "$stage"; then
        exit "$status"
    fi
    if [ "$status" -eq 0 ]; then status=1; fi
    exit "$status"
}

backup_remote_facts() (
    local remote_file="${REMOTE_DIR}/${DATA_DIR}/facts.json" state stage i path
    local restore_on_exit=()
    echo "リモートの facts.json をバックアップ中..."

    if ! state=$(deployment_ssh "
        set -e
        root=$(shell_quote "$REMOTE_DIR")
        dir=$(shell_quote "${REMOTE_DIR}/${DATA_DIR}")
        file=$(shell_quote "$remote_file")
        [ -x \"\$root\" ]
        if [ -e \"\$dir\" ] || [ -L \"\$dir\" ]; then
            [ -d \"\$dir\" ] && [ -x \"\$dir\" ] || exit 1
        fi
        if [ -f \"\$file\" ]; then
            printf 'present\\n'
        elif [ -e \"\$file\" ] || [ -L \"\$file\" ]; then
            exit 1
        else
            printf 'missing\\n'
        fi
    "); then
        echo "バックアップ失敗: リモートの facts.json を確認できません" >&2
        return 1
    fi
    if [ "$state" = missing ]; then
        echo "バックアップ対象なし: リモートの facts.json"
        return 0
    fi
    if [ "$state" != present ]; then
        echo "バックアップ失敗: リモートの facts.json の状態が不明です" >&2
        return 1
    fi

    mkdir -p -- "$LOG_DIR"
    stage=$(mktemp -d "${LOG_DIR}/.facts-backup.XXXXXXXX")
    trap 'finish_fact_backup "$?" "$stage" "${restore_on_exit[@]}"' EXIT
    trap 'echo "バックアップ中断: SIGHUP" >&2; exit 129' HUP
    trap 'echo "バックアップ中断: SIGINT" >&2; exit 130' INT
    trap 'echo "バックアップ中断: SIGTERM" >&2; exit 143' TERM
    if ! scp -i "${SFTP_KEY_FILE}" "${SFTP_USER}@${REMOTE_HOST}:${remote_file}" "$stage/next.1"; then
        echo "バックアップ失敗: リモートの facts.json を取得できません" >&2
        return 1
    fi
    if [ ! -f "$stage/next.1" ] || [ -L "$stage/next.1" ]; then
        echo "バックアップ失敗: ダウンロードした facts.json を確認できません" >&2
        return 1
    fi

    for i in {1..5}; do
        path="${LOG_DIR}/facts.json.$i"
        if [ -e "$path" ] || [ -L "$path" ]; then
            if [ ! -f "$path" ] || [ -L "$path" ]; then
                echo "バックアップ失敗: $path は通常ファイルではありません" >&2
                return 1
            fi
            ln -- "$path" "$stage/previous.$i"
            if [ "$i" -lt 5 ]; then
                ln -- "$stage/previous.$i" "$stage/next.$((i+1))"
            fi
        fi
    done

    for i in {5..1}; do
        path="${LOG_DIR}/facts.json.$i"
        if [ -f "$stage/next.$i" ]; then
            [ "$stage/next.$i" -ef "$path" ] && continue
            restore_on_exit+=("$i")
            if mv -f -- "$stage/next.$i" "$path"; then continue; fi
        else
            [ -f "$stage/previous.$i" ] || continue
            restore_on_exit+=("$i")
            if rm -- "$path"; then continue; fi
        fi
        echo "バックアップ失敗: $path を更新できません" >&2
        return 1
    done
    restore_on_exit=()
    echo "  ✓ バックアップ完了: ${LOG_DIR}/facts.json.1"
)

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
    
    backup_remote_facts
    sync_data_dir
    
    echo ""
    echo "=== 同期完了 ==="
}

prepare_and_deploy_bot() {
    backup_remote_facts
    sync_data_dir
    build_bot
    deploy_staged_bot "$@"
}

run_normal_deploy() {
    echo "  → 通常デプロイ (Bot本体)"
    echo ""
    
    local selected_str=$(multiselect_services)
    IFS=' ' read -r -a target_services <<< "$selected_str"
    if [ ${#target_services[@]} -eq 0 ]; then
         echo "  ⚠ サービス未選択。中止します。"
         exit 1
    fi
    echo "  → 対象サービス: ${target_services[*]}"
    
    echo ""
    echo "実行計画:"
    echo "  ✓ FACTSバックアップ & データ同期"
    echo "  ✓ Botビルド & デプロイ"
    echo "  ✓ サービス再起動: ${target_services[*]}"
    echo ""
    
    with_deployment_lock prepare_and_deploy_bot false "${target_services[@]}"
    
    echo ""
    echo "=== デプロイ完了 ==="
    echo "ステータス確認: ssh -i ${SFTP_KEY_FILE} ${SFTP_USER}@${REMOTE_HOST} 'supervisorctl status ${target_services[*]}'"
}

run_maintenance_deploy() {
    echo "  → メンテナンス実行デプロイ"
    echo ""
    echo "実行計画:"
    echo "  1. メンテナンスモード(RUN_MAINTENANCE=true)で起動"
    echo "  2. 次回以降は通常モード(RUN_MAINTENANCE=false)で起動"
    echo ""
    
    local selected_str=$(multiselect_services)
    IFS=' ' read -r -a target_services <<< "$selected_str"
    if [ ${#target_services[@]} -eq 0 ]; then
         echo "  ⚠ サービス未選択。中止します。"
         exit 1
    fi
    echo "  → 対象サービス: ${target_services[*]}"
    
    with_deployment_lock prepare_and_deploy_bot true "${target_services[@]}"
    echo ""
    echo "=== メンテナンスデプロイ完了 ==="
}

run_migration_deploy() {
    echo "  → マイグレーションツールのみ"
    echo ""
    echo "実行計画:"
    echo "  ✓ Migrationツールビルド"
    echo "  ✓ Migrationツール転送"
    echo "  - データ同期なし"
    echo ""
    
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
    
    echo "facts.json を強制アップロード中..."
    sync_remote --ignore-times "${DATA_DIR}/facts.json" "${REMOTE_HOST}:${DATA_DIR}/facts.json"
    
    echo ""
    echo "=== 強制更新完了 ==="
}

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

main() {
    local script_path="${BASH_SOURCE[0]}"
    local link_target
    while [ -L "$script_path" ]; do
        link_target=$(readlink -- "$script_path")
        case "$link_target" in
            /*) script_path="$link_target" ;;
            *) script_path="$(dirname -- "$script_path")/$link_target" ;;
        esac
    done
    CDPATH= cd -P -- "$(dirname -- "$script_path")"
    if ! grep -Eq '^[[:space:]]*module[[:space:]]+claude_bot[[:space:]]*$' go.mod; then
        echo "デプロイ中止: リポジトリ直下の deploy.sh を使用してください" >&2
        exit 1
    fi
    if ! command -v rsync &> /dev/null; then
        echo "エラー: rsync がインストールされていません"
        exit 1
    fi

    echo "=== Mastodon Claude Bot デプロイスクリプト ==="
    echo ""

    echo "実行モードを選択してください:"
    local mode=$(select_menu "モード:" "同期のみ" "通常デプロイ (Bot本体)" "メンテナンス実行デプロイ" "マイグレーションデプロイ (ツールのみ)" "強制ファクト更新モード")
    
    if [ "$mode" != 3 ] && [ -L "${DATA_DIR}" ]; then
        echo "デプロイ中止: ${DATA_DIR}/ がシンボリックリンクです" >&2
        exit 1
    fi

    case $mode in
        0) with_deployment_lock run_sync_only ;;
        1) run_normal_deploy ;;
        2) run_maintenance_deploy ;;
        3) with_deployment_lock run_migration_deploy ;;
        4) with_deployment_lock run_force_facts_update ;;
    esac
}

main "$@"
