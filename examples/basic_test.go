package examples

import (
	"strings"
	"testing"

	"github.com/gfanton/tstar"
)

// TestBasic demonstrates basic usage of testscript with custom commands
func TestBasic(t *testing.T) {
	tstar.Run(t, tstar.Params{
		Dir: "testdata",
		Commands: map[string]func(*tstar.TestScript, bool, []string){
			"echo": cmdEcho,
			"cat":  cmdCat,
		},
	})
}

// cmdEcho implements a simple echo command
func cmdEcho(ts *tstar.TestScript, neg bool, args []string) {
	if len(args) < 2 {
		ts.Fatalf("usage: echo text...")
	}
	// In a real implementation, you would write to stdout
	// This is just a placeholder to demonstrate the API
	ts.Logf("echo: %s", strings.Join(args[1:], " "))
}

// cmdCat implements a simple cat command
func cmdCat(ts *tstar.TestScript, neg bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: cat file")
	}
	// In a real implementation, you would read and output the file
	// This is just a placeholder to demonstrate the API
	ts.Logf("cat: %s", args[1])
}
