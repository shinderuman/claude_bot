package cli

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestFlagParsingExitCodes(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
		text string
	}{
		{name: "valid flag", args: []string{"-env", "instance.env"}},
		{name: "help", args: []string{"-h"}, text: "Usage of bot:"},
		{name: "invalid flag", args: []string{"-unknown"}, code: 2, text: "flag provided but not defined"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			flags := flag.NewFlagSet("bot", flag.ContinueOnError)
			flags.SetOutput(&output)
			flags.String("env", "", "environment file")
			if got := ExitCode(Parse(flags, tt.args)); got != tt.code {
				t.Fatalf("exit code = %d, want %d", got, tt.code)
			}
			if !strings.Contains(output.String(), tt.text) {
				t.Fatalf("flag output = %q, want %q", output.String(), tt.text)
			}
		})
	}
	if got := ExitCode(errors.New("initialization failed")); got != 1 {
		t.Fatalf("initialization failure exit code = %d, want 1", got)
	}
}
