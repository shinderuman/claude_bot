package cli

import (
	"bytes"
	"errors"
	"flag"
	"log"
	"strings"
	"testing"
)

func TestFlagParsingExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
		text string
		log  string
	}{
		{name: "valid flag", args: []string{"-env", "instance.env"}},
		{name: "help", args: []string{"-h"}, text: "Usage of bot:"},
		{name: "invalid flag", args: []string{"-unknown"}, code: 2, log: "コマンド実行失敗: flag provided but not defined: -unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			var logged bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logged)
			t.Cleanup(func() { log.SetOutput(previous) })
			flags := flag.NewFlagSet("bot", flag.ContinueOnError)
			flags.SetOutput(&output)
			flags.String("env", "", "environment file")
			if got := ExitCode(Parse(flags, tt.args)); got != tt.code {
				t.Fatalf("exit code = %d, want %d", got, tt.code)
			}
			if !strings.Contains(output.String(), tt.text) {
				t.Fatalf("flag output = %q, want %q", output.String(), tt.text)
			}
			if tt.text == "" && output.Len() != 0 {
				t.Fatalf("unexpected flag output = %q", output.String())
			}
			if tt.log == "" {
				if logged.Len() != 0 {
					t.Fatalf("unexpected log: %q", logged.String())
				}
			} else if !strings.HasSuffix(logged.String(), tt.log+"\n") || strings.Count(logged.String(), "\n") != 1 {
				t.Fatalf("log output = %q, want %q", logged.String(), tt.log)
			}
		})
	}
	if got := ExitCode(errors.New("initialization failed")); got != 1 {
		t.Fatalf("initialization failure exit code = %d, want 1", got)
	}
}
