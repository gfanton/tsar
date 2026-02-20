package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gfanton/tstar"
	"github.com/peterbourgon/ff/v4"
)

type config struct {
	verbose             bool
	short               bool
	testWork            bool
	workdirRoot         string
	continueOnError     bool
	requireExplicitExec bool
	requireUniqueNames  bool
}

func (cfg *config) registerFlags(fs *ff.FlagSet) {
	fs.BoolVar(&cfg.verbose, 'v', "verbose", "enable verbose output")
	fs.BoolVar(&cfg.short, 's', "short", "run tests in short mode")
	fs.BoolVar(&cfg.testWork, 0, "test-work", "preserve work directories after tests")
	fs.StringVar(&cfg.workdirRoot, 'w', "workdir-root", "", "root directory for work directories")
	fs.BoolVar(&cfg.continueOnError, 'c', "continue-on-error", "continue executing tests after an error")
	fs.BoolVar(&cfg.requireExplicitExec, 'e', "require-explicit-exec", "require explicit 'exec' for command execution")
	fs.BoolVar(&cfg.requireUniqueNames, 'u', "require-unique-names", "require unique test names")
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tsCmd := NewCommand()

	// Parse flags with ff for environment variable support
	if err := tsCmd.ParseAndRun(ctx, os.Args[1:], ff.WithEnvVarPrefix("TSAR")); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// NewCommand creates the root ff.Command for the tsar CLI.
func NewCommand() *ff.Command {
	var cfg config

	fs := ff.NewFlagSet("tsar")
	cfg.registerFlags(fs)

	return &ff.Command{
		Name:  "tsar",
		Usage: "tsar [FLAGS] SUBCOMMAND ...",
		Flags: fs,
		Exec: func(ctx context.Context, args []string) error {
			return execTestRunner(ctx, &cfg, args)
		},
	}
}

func execTestRunner(ctx context.Context, cfg *config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("at least one argument required")
	}

	target := args[0]

	// Determine if target is a file or directory
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", target, err)
	}

	// Initialize testing framework with minimal os.Args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"tsar"}
	flag.Parse()
	testing.Init()

	// Set up test flags after testing.Init()
	if cfg.short {
		flag.Set("test.short", "true")
	}

	if cfg.verbose {
		flag.Set("test.v", "true")
	}

	// Create parameters for testscript
	params := tstar.Params{
		TestWork:            cfg.testWork,
		WorkdirRoot:         cfg.workdirRoot,
		ContinueOnError:     cfg.continueOnError,
		RequireExplicitExec: cfg.requireExplicitExec,
		RequireUniqueNames:  cfg.requireUniqueNames,
	}

	// Create a testResultCapture to capture test results
	runner := &testResultCapture{
		verbose: cfg.verbose,
	}

	if !info.IsDir() {
		// Single file execution
		if !strings.HasSuffix(target, ".tsar") {
			return fmt.Errorf("file must have .tsar extension: %s", target)
		}

		absPath, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("cannot get absolute path for %s: %v", target, err)
		}

		params.Dir = filepath.Dir(absPath)
		tstar.RunFilesStandalone(runner, params, absPath)
	} else {
		// Directory execution
		absPath, err := filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("cannot get absolute path for %s: %v", target, err)
		}

		params.Dir = absPath
		tstar.RunStandalone(runner, params)
	}

	if runner.failed {
		return fmt.Errorf("tests failed")
	}

	return nil
}

// testResultCapture implements TestingT to capture test results
type testResultCapture struct {
	failed  bool
	verbose bool
}

func (t *testResultCapture) Skip(args ...any) {
	if t.verbose {
		fmt.Print("SKIP: ")
		fmt.Println(args...)
	}
}

func (t *testResultCapture) Fatal(args ...any) {
	t.failed = true
	fmt.Print("FAIL: ")
	fmt.Println(args...)
	// Don't exit here like testing.T does, just mark as failed
}

func (t *testResultCapture) Fatalf(format string, args ...any) {
	t.failed = true
	fmt.Print("FAIL: ")
	fmt.Printf(format, args...)
	fmt.Println()
	// Don't exit here like testing.T does, just mark as failed
}

func (t *testResultCapture) Log(args ...any) {
	if t.verbose {
		fmt.Println(args...)
	}
}

func (t *testResultCapture) Logf(format string, args ...any) {
	if t.verbose {
		fmt.Printf(format, args...)
		fmt.Print("\n")
	}
}

func (t *testResultCapture) Failed() bool {
	return t.failed
}

func (t *testResultCapture) Helper() {
	// No-op for our purposes
}
