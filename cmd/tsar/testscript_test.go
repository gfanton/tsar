package main

import (
	"context"
	"testing"

	"github.com/gfanton/tstar"
)

func TestTsar(t *testing.T) {
	p := tstar.Params{
		Dir: "testdata",
		Setup: func(env *tstar.Env) error {
			return nil
		},
	}

	// Register the tsar command for testscripts
	p.Commands = map[string]func(*tstar.TestScript, bool, []string){}
	p.Commands["tsar"] = func(ts *tstar.TestScript, neg bool, args []string) {
		tsCmd := NewCommand()
		err := tsCmd.ParseAndRun(context.Background(), args[1:])

		commandSucceeded := (err == nil)
		successExpected := !neg

		// Compare the command's success status with the expected outcome.
		if commandSucceeded != successExpected {
			ts.Fatalf("unexpected tsar command outcome (err=%t expected=%t): %s", commandSucceeded, successExpected, err)
		}
	}

	tstar.Run(t, p)
}
