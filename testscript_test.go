package tstar

import (
	"testing"
)

func TestTsarBasic(t *testing.T) {
	Run(t, Params{
		Dir: "examples/testdata",
	})
}

func TestTsarWithCommands(t *testing.T) {
	Run(t, Params{
		Dir: "examples/testdata",
		Commands: map[string]func(*TestScript, bool, []string){
			"custom": func(ts *TestScript, neg bool, args []string) {
				ts.Logf("Custom command executed with args: %v", args[1:])
			},
		},
	})
}
