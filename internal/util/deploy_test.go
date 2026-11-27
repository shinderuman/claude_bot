//go:build darwin || linux

package util

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDeployChecksTransferAndSupervisorResults(t *testing.T) {
	script, err := os.ReadFile("../../deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		mode         string
		fail         string
		wantCode     int
		wantLog      string
		wantRecovery bool
		wantClaude   bool
	}{
		{name: "normal", mode: "normal", wantLog: "=== デプロイ完了 ==="},
		{name: "retry after SSH lost on lock acquisition", fail: "lock-acquire-disconnect", wantCode: 1, wantLog: "配置先の排他ロックを取得できません"},
		{name: "late lock acquisition after SSH loss", fail: "lock-acquire-late", wantCode: 1, wantLog: "配置先の排他ロックを取得できません"},
		{name: "retry after SSH lost on lock release", fail: "lock-release-disconnect", wantLog: "=== デプロイ完了 ==="},
		{name: "retry after remote shell killed during lock release", fail: "lock-release-killed", wantLog: "=== デプロイ完了 ==="},
		{name: "partial backup download preserves every generation", mode: "sync-only", fail: "backup-download", wantCode: 1, wantLog: "リモートの facts.json を取得できません"},
		{name: "backup rotation failure restores every generation", mode: "sync-only", fail: "backup-rotation", wantCode: 1, wantLog: "facts.json.3 を更新できません"},
		{name: "backup probe failure preserves every generation", mode: "sync-only", fail: "backup-probe", wantCode: 1, wantLog: "リモートの facts.json を確認できません"},
		{name: "backup missing file preserves every generation", mode: "sync-only", fail: "backup-missing", wantLog: "バックアップ対象なし"},
		{name: "successful backup rotates generations", mode: "sync-only", fail: "backup-success", wantLog: "=== 同期完了 ==="},
		{name: "backup rotates with missing newest generation", mode: "sync-only", fail: "backup-gap-success", wantLog: "=== 同期完了 ==="},
		{name: "backup failure after rotating a gap restores generations", mode: "sync-only", fail: "backup-gap-rotation", wantCode: 1, wantLog: "facts.json.1 を更新できません"},
		{name: "SIGTERM after replacing oldest backup", mode: "sync-only", fail: "backup-term", wantCode: 143, wantLog: "バックアップ中断"},
		{name: "SIGTERM after replacing newest backup", mode: "sync-only", fail: "backup-term-final", wantCode: 143, wantLog: "バックアップ中断"},
		{name: "failed restoration after SIGTERM preserves recovery files", mode: "sync-only", fail: "backup-term-restore", wantCode: 143, wantLog: "バックアップの復元用ファイル"},
		{name: "start failure", mode: "normal", fail: "start", wantCode: 1, wantLog: "claude_bot_claude) の開始失敗 (終了コード: 1)"},
		{name: "stop failure", mode: "normal", fail: "stop", wantCode: 1, wantLog: "claude_bot_claude) の停止失敗 (終了コード: 1)", wantRecovery: true},
		{name: "maintenance stop failure", mode: "maintenance", fail: "stop", wantCode: 1, wantLog: "claude_bot_claude) の停止失敗 (終了コード: 1)", wantRecovery: true},
		{name: "SSH lost after stop", fail: "stop-disconnect", wantCode: 1, wantLog: "claude_bot_claude) の停止結果が未確認", wantRecovery: true, wantClaude: true},
		{name: "SSH lost while stopping in maintenance", mode: "maintenance", fail: "stop-in-flight", wantCode: 1, wantLog: "claude_bot_claude) の停止結果が未確認", wantRecovery: true, wantClaude: true},
		{name: "SSH still unavailable", fail: "stop-unreachable", wantCode: 1, wantLog: "claude_bot_claude) の停止状態を確認できません", wantRecovery: true},
		{name: "SSH lost for already stopped service", fail: "stop-disconnect-inactive", wantCode: 1, wantLog: "claude_bot_claude) の停止結果が未確認", wantRecovery: true},
		{name: "already stopped", mode: "normal", fail: "stop-already-stopped", wantCode: 1, wantLog: "claude_bot_gemini: ERROR (not running)"},
		{name: "recovery failure", mode: "normal", fail: "stop-recovery", wantCode: 1, wantLog: "claude_bot_gemini) の復旧失敗 (終了コード: 7)"},
		{name: "state check failure", fail: "preflight", wantCode: 1, wantLog: "claude_bot_gemini) の停止前の状態を確認できません"},
		{name: "binary upload failure", fail: "upload-binary", wantCode: 9, wantLog: "アップロード失敗のモック"},
		{name: "wrapper upload failure", mode: "maintenance", fail: "upload-wrapper", wantCode: 9, wantLog: "アップロード失敗のモック"},
		{name: "install failure after binary replacement", mode: "maintenance", fail: "install", wantCode: 1, wantLog: "Botファイルの切替に失敗", wantRecovery: true, wantClaude: true},
		{name: "connection lost after replacement", fail: "install-disconnect", wantCode: 1, wantLog: "Botファイルの切替に失敗", wantRecovery: true, wantClaude: true},
		{name: "restore files failure", fail: "install-restore", wantCode: 1, wantLog: "旧Botファイルを復元できませんでした"},
		{name: "copy fails restoring old binary", fail: "install-restore-copy", wantCode: 1, wantLog: "旧Botファイルを復元できませんでした"},
		{name: "sync failure", mode: "normal", fail: "sync", wantCode: 23, wantLog: "同期失敗のモック"},
		{name: "maintenance", mode: "maintenance", wantLog: "=== メンテナンスデプロイ完了 ==="},
		{name: "second checkout during file switch", mode: "maintenance", fail: "overlap-install", wantLog: "=== メンテナンスデプロイ完了 ==="},
		{name: "restart before maintenance stage cleanup", mode: "maintenance", fail: "immediate-restart", wantLog: "started: false -env .env.claude"},
		{name: "maintenance start failure", mode: "maintenance", fail: "start", wantCode: 1, wantLog: "claude_bot_claude) の開始失敗 (終了コード: 1)"},
		{name: "force update with newer destination", mode: "force-newer", wantLog: "=== 強制更新完了 ==="},
		{name: "force update with equal timestamps and sizes", mode: "force-equal", wantLog: "=== 強制更新完了 ==="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, "checkout")
			remote := filepath.Join(root, "bot's remote dir")
			tools := filepath.Join(root, "tools")
			for _, dir := range []string{filepath.Join(checkout, "data"), tools} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"go.mod":           "module claude_bot\n",
				"data/.env.claude": "",
				"data/.env.gemini": "",
				"deploy.sh": strings.Replace(string(script), `REMOTE_DIR="/home/ubuntu/claude_bot"`,
					`REMOTE_DIR="`+remote+`"`, 1),
			}
			checkouts := []string{checkout}
			var overlapCheckout string
			if tc.fail == "overlap-install" {
				overlapCheckout = filepath.Join(root, "second checkout")
				if err := os.MkdirAll(filepath.Join(overlapCheckout, "data"), 0700); err != nil {
					t.Fatal(err)
				}
				checkouts = append(checkouts, overlapCheckout)
			}
			for _, dir := range checkouts {
				for name, content := range files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			checkBackups := strings.HasPrefix(tc.fail, "backup-")
			backupGap := strings.HasPrefix(tc.fail, "backup-gap-")
			if checkBackups {
				if err := os.MkdirAll(filepath.Join(checkout, "data", "log"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(remote, "data"), 0700); err != nil {
					t.Fatal(err)
				}
				if tc.fail != "backup-missing" {
					if err := os.WriteFile(filepath.Join(remote, "data", "facts.json"), []byte("[0]\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				for i := 1; i <= 5; i++ {
					if backupGap && i == 1 {
						continue
					}
					archive := filepath.Join(checkout, "data", "log", fmt.Sprintf("facts.json.%d", i))
					if err := os.WriteFile(archive, []byte(fmt.Sprintf("[%d]\n", i)), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var realRsync string
			force := strings.HasPrefix(tc.mode, "force-")
			if force {
				realRsync, err = exec.LookPath("rsync")
				if err != nil {
					t.Skip("rsync is required for the force-update transfer test")
				}
				if err := os.MkdirAll(filepath.Join(remote, "data"), 0700); err != nil {
					t.Fatal(err)
				}
				now := time.Now().Truncate(time.Second)
				oldTime := now
				if tc.mode == "force-newer" {
					oldTime = now.Add(-time.Hour)
				}
				for _, file := range []struct {
					path, content string
					timestamp     time.Time
				}{
					{filepath.Join(checkout, "data", "facts.json"), "[1]\n", oldTime},
					{filepath.Join(remote, "data", "facts.json"), "[2]\n", now},
				} {
					if err := os.WriteFile(file.path, []byte(file.content), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(file.path, file.timestamp, file.timestamp); err != nil {
						t.Fatal(err)
					}
				}
			}
			needsOldFiles := strings.HasPrefix(tc.fail, "stop") || strings.HasPrefix(tc.fail, "install")
			beforeStop := tc.fail == "preflight" || strings.HasPrefix(tc.fail, "upload")
			startFailed := tc.fail == "start"
			oldFiles := map[string]string{
				"claude_bot":     "#!/bin/sh\nexec ../claude_bot_bin \"$@\"\n",
				"claude_bot_bin": "#!/bin/sh\nprintf 'old started: %s %s\\n' \"$1\" \"$2\"\n",
			}
			if needsOldFiles || beforeStop || startFailed {
				if err := os.Mkdir(remote, 0700); err != nil {
					t.Fatal(err)
				}
				for name, content := range oldFiles {
					if err := os.WriteFile(filepath.Join(remote, name), []byte(content), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			stub := filepath.Join(tools, "stub")
			if err := os.WriteFile(stub, []byte(deployCommandStub), 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"go", "ssh", "scp", "rsync", "sudo", "stty", "tput", "sleep", "mv", "cp", "rm", "flock"} {
				if err := os.Symlink(stub, filepath.Join(tools, name)); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "deploy.sh")
			cmd.Dir = checkout
			testExecutable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = []string{"PATH=" + tools + ":/usr/bin:/bin", "TEST_REMOTE=" + remote, "TEST_FAIL=" + tc.fail, "TEST_RSYNC=" + realRsync, "TEST_OVERLAP_CHECKOUT=" + overlapCheckout, "TEST_EXECUTABLE=" + testExecutable}
			keys := "\x1b[B\n\n"
			if tc.mode == "sync-only" {
				keys = "\n"
			}
			if tc.mode == "maintenance" {
				keys = "\x1b[B\x1b[B\n\n"
			}
			if force {
				keys = strings.Repeat("\x1b[B", 4) + "\n"
			}
			cmd.Stdin = strings.NewReader(keys)
			rawOutput, err := cmd.CombinedOutput()
			output := string(rawOutput)
			if cmd.ProcessState == nil {
				t.Fatalf("could not run deploy.sh: %v\n%s", err, output)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.wantCode {
				t.Fatalf("deploy exit = %d, want %d\n%s", code, tc.wantCode, output)
			}
			if !strings.Contains(output, tc.wantLog) {
				t.Fatalf("deploy output does not match outcome\n%s", output)
			}
			if checkBackups {
				if strings.HasPrefix(tc.fail, "backup-term") {
					wantReplacement := "[4]\n"
					if tc.fail != "backup-term" {
						wantReplacement = "[0]\n"
					}
					replacement, err := os.ReadFile(filepath.Join(remote, "backup-interrupted"))
					if err != nil || string(replacement) != wantReplacement {
						t.Fatalf("SIGTERM was not sent after the replacement: %q (%v)", replacement, err)
					}
				}
				for i := 1; i <= 5; i++ {
					want := i
					if strings.HasSuffix(tc.fail, "-success") {
						want--
					}
					if tc.fail == "backup-term-restore" && i == 5 {
						want = 4
					}
					data, err := os.ReadFile(filepath.Join(checkout, "data", "log", fmt.Sprintf("facts.json.%d", i)))
					if backupGap && want == 1 {
						if !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("generation %d = %q (%v), want missing\n%s", i, data, err, output)
						}
						continue
					}
					if err != nil || string(data) != fmt.Sprintf("[%d]\n", want) {
						t.Fatalf("generation %d = %q (%v), want [%d]\n%s", i, data, err, want, output)
					}
				}
				staged, err := filepath.Glob(filepath.Join(checkout, "data", "log", ".facts-backup.*"))
				if err != nil {
					t.Fatal(err)
				}
				if tc.fail == "backup-term-restore" {
					if len(staged) != 1 || !strings.Contains(output, "バックアップの復元用ファイル: data/log/"+filepath.Base(staged[0])) {
						t.Fatalf("recovery directory = %v\n%s", staged, output)
					}
					previous, err := os.ReadFile(filepath.Join(staged[0], "previous.5"))
					if err != nil || string(previous) != "[5]\n" {
						t.Fatalf("oldest recovery file = %q (%v)", previous, err)
					}
				} else if len(staged) != 0 {
					t.Fatalf("backup left temporary files: %v", staged)
				}
				if tc.wantCode != 0 && strings.Contains(output, "データディレクトリを同期中") {
					t.Fatalf("deployment synced data after backup failure\n%s", output)
				}
				if _, err := os.Lstat(filepath.Join(remote, ".deploy.lock")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deployment left a lock after backup: %v", err)
				}
				return
			}
			if strings.HasPrefix(tc.fail, "lock-") {
				if _, err := os.Stat(filepath.Join(remote, "cut")); err != nil {
					t.Fatalf("SSH was not interrupted: %v\n%s", err, output)
				}
				if tc.fail == "lock-acquire-late" {
					late, err := os.ReadFile(filepath.Join(remote, "late-acquire"))
					if err != nil {
						t.Fatal(err)
					}
					delayed := exec.CommandContext(ctx, "/bin/sh", "-ec", string(late))
					delayed.Env = cmd.Environ()
					if diagnostic, err := delayed.CombinedOutput(); err == nil || !strings.Contains(string(diagnostic), "/owner") {
						t.Fatalf("delayed command did not fail on withdrawn ownership: %v\n%s", err, diagnostic)
					}
				}
				if _, err := os.Lstat(filepath.Join(remote, ".deploy.lock")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("interrupted deployment left a lock: %v", err)
				}
				if guards, err := filepath.Glob(filepath.Join(remote, ".deploy-owner.*")); err != nil || len(guards) != 0 {
					t.Fatalf("interrupted deployment left ownership files: %v (%v)", guards, err)
				}
				retry := exec.CommandContext(ctx, "bash", "deploy.sh")
				retry.Dir = checkout
				retry.Env = append(cmd.Environ(), "TEST_FAIL=")
				retry.Stdin = strings.NewReader(keys)
				if output, err := retry.CombinedOutput(); err != nil || !strings.Contains(string(output), "=== デプロイ完了 ===") {
					t.Fatalf("deployment retry failed: %v\n%s", err, output)
				}
				return
			}
			if force {
				data, err := os.ReadFile(filepath.Join(remote, "data", "facts.json"))
				if err != nil || string(data) != "[1]\n" {
					t.Fatalf("forced update left destination at %q (%v)\n%s", data, err, output)
				}
				return
			}
			if tc.wantCode != 0 && strings.Contains(output, "デプロイ完了") {
				t.Fatalf("deploy reported success after a failure\n%s", output)
			}
			if overlapCheckout != "" {
				log, err := os.ReadFile(filepath.Join(remote, "overlap.log"))
				if err != nil || !strings.Contains(string(log), "配置先の排他ロックを取得できません") {
					t.Fatalf("overlapping deployment did not report a busy destination: %s (%v)", log, err)
				}
				if strings.Contains(string(log), "リモートの facts.json をバックアップ中") ||
					strings.Contains(string(log), "を停止中") {
					t.Fatalf("overlapping deployment mutated a busy destination: %s", log)
				}
			}
			staged, err := filepath.Glob(filepath.Join(remote, ".deploy.*"))
			if err != nil {
				t.Fatal(err)
			}
			restoreFailed := strings.HasPrefix(tc.fail, "install-restore")
			if restoreFailed || startFailed {
				if len(staged) != 1 || !strings.Contains(output, "復旧用ファイルの保存先: "+staged[0]) {
					t.Fatalf("deployment failure did not preserve the backup directory\n%s", output)
				}
				for name, want := range map[string]string{
					"bin.previous":     oldFiles["claude_bot_bin"],
					"wrapper.previous": oldFiles["claude_bot"],
				} {
					backup, err := os.ReadFile(filepath.Join(staged[0], name))
					if err != nil || string(backup) != want {
						t.Fatalf("%s backup = %s (%v)", name, backup, err)
					}
				}
			} else if len(staged) != 0 {
				t.Fatalf("deploy did not clean up staged files: %v\n%s", staged, output)
			}
			if restoreFailed {
				if strings.Contains(output, "を復旧中") {
					t.Fatalf("deploy restarted services before file restoration succeeded\n%s", output)
				}
				return
			}
			if needsOldFiles || beforeStop {
				for name, want := range oldFiles {
					got, err := os.ReadFile(filepath.Join(remote, name))
					if err != nil || string(got) != want {
						t.Fatalf("deploy changed %s after stop failed: %s (%v)", name, got, err)
					}
				}
				if beforeStop && (strings.Contains(output, "を停止中") || strings.Contains(output, "old started:")) {
					t.Fatalf("deploy changed service state before staging and checks succeeded\n%s", output)
				}
				if restarted := strings.Contains(output, "old started: -env .env.claude"); restarted != tc.wantClaude {
					t.Fatalf("old Claude restarted = %v, want %v\n%s", restarted, tc.wantClaude, output)
				}
				if restarted := strings.Contains(output, "old started: -env .env.gemini"); restarted != tc.wantRecovery {
					t.Fatalf("old Gemini restarted = %v, want %v\n%s", restarted, tc.wantRecovery, output)
				}
				if !tc.wantRecovery && tc.fail == "stop-already-stopped" && strings.Contains(output, "Supervisor (claude_bot_gemini) を復旧中") {
					t.Fatalf("deploy attempted to restart an already stopped service\n%s", output)
				}
				return
			}
			if tc.fail == "sync" {
				if _, err := os.Stat(filepath.Join(remote, "claude_bot")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deploy installed the wrapper after %s failed: %v", tc.fail, err)
				}
				return
			}

			wantMaintenance := "false"
			if tc.mode == "maintenance" {
				wantMaintenance = "true"
			}
			if !strings.Contains(output, "started: "+wantMaintenance+" -env .env.gemini") {
				t.Fatalf("the other service did not start with its configuration\n%s", output)
			}
			if tc.wantCode == 0 && !strings.Contains(output, "started: "+wantMaintenance+" -env .env.claude") {
				t.Fatalf("the Claude service did not start with its configuration\n%s", output)
			}
			wrapper := exec.CommandContext(ctx, filepath.Join(remote, "claude_bot"), "-env", ".env.claude")
			wrapper.Dir = filepath.Join(remote, "data")
			wrapper.Env = append(os.Environ(), "SUPERVISOR_PROCESS_NAME=claude_bot_claude")
			if out, err := wrapper.CombinedOutput(); err != nil || string(out) != "started: false -env .env.claude\n" {
				t.Fatalf("installed wrapper = %s (%v), want normal mode", out, err)
			}
		})
	}
}

func TestDeployFlockHelper(t *testing.T) {
	if os.Getenv("TEST_DEPLOY_FLOCK") != "1" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Flock(9, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			os.Exit(0)
		}
		if err != syscall.EWOULDBLOCK || time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const deployCommandStub = `#!/bin/sh
set -eu
case "${0##*/}" in
    stty|tput|sleep) ;;
    flock)
        test "$*" = "-x -w 5 9"
        exec env TEST_DEPLOY_FLOCK=1 GORACE=atexit_sleep_ms=0 "$TEST_EXECUTABLE" -test.run '^TestDeployFlockHelper$'
        ;;
    ssh)
        for command do :; done
        case "$TEST_FAIL:$command" in
            lock-acquire-disconnect:*'ln -- '*'/owner'*|lock-release-disconnect:*'rm -f -- "$lock"'*)
                if [ ! -e "$TEST_REMOTE/cut" ]; then
                    /bin/sh -ec "$command"
                    touch "$TEST_REMOTE/cut"
                    exit 255
                fi
                ;;
            lock-acquire-late:*'ln -- '*'/owner'*)
                printf '%s\n' "$command" > "$TEST_REMOTE/late-acquire"
                touch "$TEST_REMOTE/cut"
                exit 255
                ;;
            backup-probe:*'present'*) exit 255 ;;
        esac
        /bin/sh -ec "$command"
        ;;
    sudo)
        state="$TEST_REMOTE/$3.state"
        case "$2:$3:$TEST_FAIL" in
            status:claude_bot_gemini:preflight) exit 255 ;;
            status:claude_bot_claude:stop-unreachable)
                [ ! -f "$state" ] || exit 255
                printf '%s RUNNING\n' "$3"
                ;;
            status:claude_bot_claude:stop-disconnect-inactive|status:claude_bot_gemini:stop-already-stopped)
                printf '%s STOPPED\n' "$3"
                exit 3
                ;;
            status:*)
                if [ -f "$state" ]; then
                    read -r status < "$state"
                    printf '%s %s\n' "$3" "$status"
                    if [ "$status" = STOPPING ]; then
                        printf 'STOPPED\n' > "$state"
                    else
                        exit 3
                    fi
                else
                    printf '%s RUNNING\n' "$3"
                fi
                ;;
            stop:claude_bot_claude:stop-in-flight)
                printf 'STOPPING\n' > "$state"
                exit 255
                ;;
            stop:claude_bot_claude:stop-disconnect*|stop:claude_bot_claude:stop-unreachable)
                printf 'STOPPED\n' > "$state"
                exit 255
                ;;
            stop:claude_bot_claude:stop*) exit 1 ;;
            stop:claude_bot_gemini:stop-already-stopped) printf '%s: ERROR (not running)\n' "$3" ;;
            start:claude_bot_gemini:stop-recovery) exit 7 ;;
            stop:*) printf '%s: stopped\n' "$3" ;;
            start:*)
                cd "$TEST_REMOTE/data"
                export SUPERVISOR_PROCESS_NAME="$3"
                "$TEST_REMOTE/claude_bot" -env ".env.${3#claude_bot_}"
                if [ "$TEST_FAIL" = immediate-restart ]; then
                    "$TEST_REMOTE/claude_bot" -env ".env.${3#claude_bot_}"
                fi
                ;;
        esac
        ;;
    scp)
        case "$TEST_FAIL:$3" in
            backup-download:*facts.json)
                printf 'partial\n' > "$4"
                exit 9
                ;;
            upload-binary:claude_bot_bin|upload-wrapper:wrapper.sh)
                echo "アップロード失敗のモック" >&2
                exit 9
                ;;
        esac
        if [ "${3#*:}" = "$3" ]; then
            cp "$3" "${4#*:}"
        else
            cp "${3#*:}" "$4"
        fi
        ;;
    mv)
        case "$TEST_FAIL:$3" in
            backup-rotation:*/next.3|backup-gap-rotation:*/next.1) exit 8 ;;
            backup-term:*/next.5|backup-term-final:*/next.1|backup-term-restore:*/next.1)
                /bin/mv "$@"
                cat "$4" > "$TEST_REMOTE/backup-interrupted"
                kill -TERM "$PPID"
                exit 0
                ;;
            backup-term-restore:*/previous.5) exit 8 ;;
            overlap-install:*/bin)
                /bin/mv "$@"
                (
                    cd "$TEST_OVERLAP_CHECKOUT"
                    if printf '\033[B\n\n' | TEST_FAIL=overlap bash deploy.sh > "$TEST_REMOTE/overlap.log" 2>&1; then
                        exit 8
                    fi
                )
                exit
                ;;
            install:*/wrapper|install-restore*:*/wrapper|install-restore:*/bin.previous.restore)
                echo "切替失敗のモック" >&2
                exit 8
                ;;
            install-disconnect:*/wrapper)
                /bin/mv "$@"
                if [ ! -f "$TEST_REMOTE/disconnected" ]; then
                    touch "$TEST_REMOTE/disconnected"
                    exit 255
                fi
                exit 0
                ;;
        esac
        exec /bin/mv "$@"
        ;;
    rm)
        case "$TEST_FAIL:${3:-}" in
            lock-release-killed:*/.deploy.lock)
                if [ ! -e "$TEST_REMOTE/cut" ]; then
                    test -f "$3"
                    touch "$TEST_REMOTE/cut"
                    kill -KILL "$PPID"
                    exit 255
                fi
                ;;
        esac
        exec /bin/rm "$@"
        ;;
    cp)
        case "$TEST_FAIL:$*" in
            install-restore-copy:*bin.previous.restore) exit 8 ;;
        esac
        exec /bin/cp "$@"
        ;;
    rsync)
        if [ -n "$TEST_RSYNC" ]; then
            exec "$TEST_RSYNC" "$@"
        fi
        if [ "$TEST_FAIL" = sync ]; then
            echo "同期失敗のモック" >&2
            exit 23
        fi
        for arg do
            previous="${last:-}"
            last="$arg"
            if [ "$previous" = -e ]; then
                remote_shell="$arg"
            fi
        done
        /bin/sh -ec "$remote_shell example.test test -d data"
        if [ "$previous" = data/ ]; then
            cp data/.env.* "$TEST_REMOTE/data/"
        fi
        ;;
    go)
        cat > claude_bot_bin <<'BOT'
#!/bin/sh
set -eu
test "$1" = -env
test -f "$2"
if [ "${TEST_FAIL:-}" = start ] && [ "$2" = .env.claude ]; then exit 1; fi
printf 'started: %s %s %s\n' "$RUN_MAINTENANCE" "$1" "$2"
BOT
        ;;
esac
`
