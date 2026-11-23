package cli

import (
	"errors"
	"flag"
	"io"
	"log"
)

type argumentError struct{ error }

func Parse(flags *flag.FlagSet, args []string) error {
	output := flags.Output()
	flags.SetOutput(io.Discard)
	err := flags.Parse(args)
	flags.SetOutput(output)
	if errors.Is(err, flag.ErrHelp) {
		flags.Usage()
	}
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return err
	}
	return argumentError{err}
}

func ExitCode(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	log.Printf("コマンド実行失敗: %v", err)
	var invalid argumentError
	if errors.As(err, &invalid) {
		return 2
	}
	return 1
}
