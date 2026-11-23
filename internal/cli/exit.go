package cli

import (
	"errors"
	"flag"
	"log"
)

type argumentError struct{ error }

func Parse(flags *flag.FlagSet, args []string) error {
	err := flags.Parse(args)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return err
	}
	return argumentError{err}
}

func ExitCode(err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var invalid argumentError
	if errors.As(err, &invalid) {
		return 2
	}
	log.Print(err)
	return 1
}
