// Package testscript provides support for script-driven tests.
//
// This package is heavily inspired by and adapted from the testscript package
// from github.com/rogpeppe/go-internal/testscript, originally developed by
// Roger Peppe and contributors. The original design and implementation patterns
// have been preserved while adapting the functionality to work generically
// with .tsar files instead of .txtar files.
//
// Original testscript package: https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript
// Original author: Roger Peppe <rogpeppe@gmail.com>
//
// The testscript package allows users to define filesystem-based tests by creating
// scripts in .tsar format, enabling complex integration and system testing
// scenarios with custom commands and isolated environments.
//
// Basic usage:
//
//	func TestMyCommand(t *testing.T) {
//	    testscript.Run(t, testscript.Params{
//	        Dir: "testdata",
//	        Commands: map[string]func(*testscript.TestScript, bool, []string){
//	            "mycommand": handleMyCommand,
//	        },
//	    })
//	}
//
// Each script runs in a fresh temporary work directory with a controlled
// environment. The package provides built-in commands for file operations,
// content checking, and process execution, while allowing users to register
// their own custom commands.
package tstar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/txtar"
)

// TestingT is the interface common to *testing.T and *testing.B.
type TestingT interface {
	Skip(args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Log(args ...any)
	Logf(format string, args ...any)
	Failed() bool
	Helper()
}

// Params holds parameters for a call to Run.
type Params struct {
	// Dir is the directory holding the test scripts.
	// All files in the directory with a .tsar extension are considered to be test scripts.
	Dir string

	// Commands holds a map of command names to their implementations.
	// When a command 'foo' is invoked, the function is called with the TestScript
	// context, a boolean indicating whether the command was invoked with '!',
	// and the command line arguments.
	Commands map[string]func(*TestScript, bool, []string)

	// TestWork specifies that working directories should be
	// retained for inspection after the test completes.
	TestWork bool

	// WorkdirRoot specifies the directory within which scripts' work
	// directories will be created. Setting WorkdirRoot implies TestWork=true.
	// If empty, the work directories will be created inside $TMPDIR.
	WorkdirRoot string

	// Setup is called, if non-nil, to complete any setup required for the test.
	// The working directory and environment variables are set up
	// before calling Setup; see the package documentation for details.
	// Setup is responsible for creating any files required by the script.
	Setup func(*Env) error

	// Condition is called, if non-nil, to determine whether a condition
	// listed in a script file should be satisfied. It's called with the condition
	// tag (without the surrounding []). The condition is satisfied if Condition
	// returns true or nil.
	Condition func(cond string) (bool, error)

	// RequireExplicitExec, if true, requires that commands be invoked
	// through the 'exec' builtin, and causes simple command invocation
	// to result in errors.
	RequireExplicitExec bool

	// RequireUniqueNames, if true, requires that all script files
	// have unique base names (excluding extensions).
	RequireUniqueNames bool

	// ContinueOnError causes Run to continue executing tests after an error.
	// If ContinueOnError is false (the default), any error stops execution
	// of later tests.
	ContinueOnError bool
}

// An Env holds the environment variables to use for a test script invocation.
type Env struct {
	WorkDir string
	Values  []string
	ts      *TestScript
}

// Getenv retrieves the value of the environment variable named by the key.
func (e *Env) Getenv(key string) string {
	for _, kv := range e.Values {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// Setenv sets the value of the environment variable named by the key.
func (e *Env) Setenv(key, value string) {
	entry := key + "=" + value
	for i, kv := range e.Values {
		if k, _, ok := strings.Cut(kv, "="); ok && k == key {
			e.Values[i] = entry
			return
		}
	}
	e.Values = append(e.Values, entry)
}

// TestScript holds execution state for a single test script.
type TestScript struct {
	t          TestingT
	testDir    string // directory holding the test script
	workdir    string // temporary work directory ($WORK)
	log        bytes.Buffer
	mark       int    // offset of next log truncation
	cd         string // current directory during test execution; initially $WORK
	name       string // short name of test ("foo")
	file       string // full path to test file
	lineno     int    // line number currently being processed
	line       string // line currently being processed (for error messages)
	env        []string
	envMap     map[string]string // memo of env var key → value mapping
	stdout     string            // standard output from last 'exec' command
	stderr     string            // standard error from last 'exec' command
	stopped    bool              // test wants to stop early
	start      time.Time
	background []backgroundCmd // backgrounded 'exec' commands

	builtin map[string]func(*TestScript, bool, []string)
	user    map[string]func(*TestScript, bool, []string) // external test commands; see Params.Commands
	params  Params                                       // original parameters
}

type backgroundCmd struct {
	name   string
	cmd    *exec.Cmd
	wait   <-chan struct{}
	neg    bool
	stdout strings.Builder
	stderr strings.Builder
}

type actionType int

const (
	actionExec actionType = iota
	actionWait
	actionStop
)

var backgroundSpecifier = regexp.MustCompile(`^&([a-zA-Z_]\w*)?(&)?$`)

// Run runs the test scripts in the given directory as subtests of t.
func Run(t *testing.T, p Params) {
	files := globTestFiles(t, p.Dir)
	runFiles(t, p, files)
}

// RunFiles runs the test scripts with the given file names as subtests of t.
// The files need not be in the same directory.
func RunFiles(t *testing.T, p Params, filenames ...string) {
	runFiles(t, p, filenames)
}

// RunFilesStandalone runs the test scripts without using t.Run for subtest execution.
// This is useful for command-line tools that don't need the full testing framework.
func RunFilesStandalone(t TestingT, p Params, filenames ...string) {
	runFilesStandalone(t, p, filenames)
}

// RunStandalone runs the test scripts in the given directory without using t.Run for subtest execution.
// This is useful for command-line tools that don't need the full testing framework.
func RunStandalone(t TestingT, p Params) {
	files := globTestFiles(t, p.Dir)
	runFilesStandalone(t, p, files)
}

type testCase struct {
	name string
	file string
}

func buildTestCases(t TestingT, p Params, filenames []string) []testCase {
	var tests []testCase
	seen := make(map[string]bool)
	for _, filename := range filenames {
		name := strings.TrimSuffix(filepath.Base(filename), ".tsar")
		if p.RequireUniqueNames {
			if seen[name] {
				t.Fatalf("duplicate test name %q", name)
			}
			seen[name] = true
		}
		tests = append(tests, testCase{name, filename})
	}
	return tests
}

func globTestFiles(t TestingT, dir string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*.tsar"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no test script files found")
	}
	return files
}

func runFiles(t *testing.T, p Params, filenames []string) {
	tests := buildTestCases(t, p, filenames)
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ts := &TestScript{
				t:       t,
				name:    tc.name,
				file:    tc.file,
				testDir: filepath.Dir(tc.file),
				params:  p,
				builtin: builtinCmds,
				user:    p.Commands,
				start:   time.Now(),
			}
			defer ts.finalize()
			ts.run()
		})
	}
}

func runFilesStandalone(t TestingT, p Params, filenames []string) {
	tests := buildTestCases(t, p, filenames)
	for _, tc := range tests {
		func() {
			t.Logf("=== RUN   %s", tc.name)
			ts := &TestScript{
				t:       t,
				name:    tc.name,
				file:    tc.file,
				testDir: filepath.Dir(tc.file),
				params:  p,
				builtin: builtinCmds,
				user:    p.Commands,
				start:   time.Now(),
			}
			defer ts.finalize()
			ts.run()

			if t.Failed() {
				t.Logf("--- FAIL: %s", tc.name)
			} else {
				t.Logf("--- PASS: %s", tc.name)
			}
		}()
		if t.Failed() && !p.ContinueOnError {
			return
		}
	}
}

// setup sets up the test execution temporary directory and environment.
func (ts *TestScript) setup() {
	startTime := time.Now()
	ts.log.Reset()
	ts.mark = 0
	ts.cd = ""
	ts.stdout = ""
	ts.stderr = ""
	ts.stopped = false
	ts.start = startTime
	ts.background = nil

	root := os.TempDir()
	if ts.params.WorkdirRoot != "" {
		root = ts.params.WorkdirRoot
		ts.params.TestWork = true
		if err := os.MkdirAll(root, 0755); err != nil {
			ts.t.Fatal(err)
		}
	}
	var err error
	ts.workdir, err = os.MkdirTemp(root, "tsar-*")
	if err != nil {
		ts.t.Fatal(err)
	}
	ts.cd = ts.workdir

	// Set up environment.
	ts.env = []string{
		"WORK=" + ts.workdir,
		"PATH=" + os.Getenv("PATH"),
		homeEnvName() + "=/no-home",
		tempEnvName() + "=" + filepath.Join(ts.workdir, "tmp"),
	}
	if runtime.GOOS == "windows" {
		ts.env = append(ts.env, "exe=.exe")
	} else {
		ts.env = append(ts.env, "exe=")
	}
	ts.envMap = make(map[string]string)
	for _, kv := range ts.env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			ts.envMap[k] = v
		}
	}

	if err := os.MkdirAll(filepath.Join(ts.workdir, "tmp"), 0755); err != nil {
		ts.t.Fatal(err)
	}
}

// run executes the test script.
func (ts *TestScript) run() {
	ts.setup()

	// Read and parse the test script.
	filename := ts.file
	data, err := os.ReadFile(filename)
	if err != nil {
		ts.t.Fatal(err)
	}

	// Check if this is a txtar archive.
	var ar *txtar.Archive
	if bytes.Contains(data, []byte("-- ")) {
		ar = txtar.Parse(data)
		data = ar.Comment
	}

	if ts.params.Setup != nil {
		env := &Env{
			WorkDir: ts.workdir,
			Values:  append([]string{}, ts.env...),
			ts:      ts,
		}
		if err := ts.params.Setup(env); err != nil {
			ts.t.Fatalf("setup failed: %v", err)
		}
		ts.env = env.Values
		ts.refreshEnvMap()
	}

	// Extract archive files if present.
	if ar != nil {
		for _, f := range ar.Files {
			name := f.Name
			dir := filepath.Dir(ts.mkabs(name))
			if err := os.MkdirAll(dir, 0777); err != nil {
				ts.t.Fatal(err)
			}
			if err := os.WriteFile(ts.mkabs(name), f.Data, 0666); err != nil {
				ts.t.Fatal(err)
			}
		}
	}

	script := string(data)
	// Execute script line by line.
	for script != "" {
		line, rest := getLine(script)
		script = rest
		ts.parseLine(line)
		if ts.t.Failed() || ts.stopped {
			break
		}
	}
}

// parseLine parses and executes a single script line.
func (ts *TestScript) parseLine(line string) {
	ts.lineno++
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return
	}

	// Handle conditions like [short] or [!windows]
	var cond string
	if line[0] == '[' {
		i := strings.Index(line, "]")
		if i < 0 {
			ts.t.Fatalf("script:%d: unterminated condition", ts.lineno)
		}
		cond = line[1:i]
		line = strings.TrimSpace(line[i+1:])
		if line == "" {
			return
		}
	}

	if cond != "" {
		ok, err := ts.condition(cond)
		if err != nil {
			ts.t.Fatalf("script:%d: %v", ts.lineno, err)
		}
		if !ok {
			return
		}
	}

	// Parse command line.
	args := ts.parse(line)
	if len(args) == 0 {
		return
	}

	// Check for negation prefix.
	neg := false
	if args[0] == "!" {
		neg = true
		args = args[1:]
		if len(args) == 0 {
			ts.t.Fatalf("script:%d: ! on line by itself", ts.lineno)
		}
	}

	// Execute the command.
	ts.line = line
	ts.cmdExec(neg, args)
}

// cmdExec executes a command with the given arguments.
func (ts *TestScript) cmdExec(neg bool, args []string) {
	cmd := args[0]
	if ts.builtin[cmd] != nil {
		ts.builtin[cmd](ts, neg, args)
		return
	}
	if ts.user != nil && ts.user[cmd] != nil {
		ts.user[cmd](ts, neg, args)
		return
	}

	if !ts.params.RequireExplicitExec {
		ts.cmdExecBuiltin(neg, append([]string{"exec"}, args...))
		return
	}

	ts.t.Fatalf("script:%d: unknown command %q", ts.lineno, cmd)
}

// finalize cleans up after script execution.
func (ts *TestScript) finalize() {
	if !ts.params.TestWork {
		removeAll(ts.workdir)
	} else {
		ts.t.Logf("work directory: %s", ts.workdir)
	}
}

// Built-in commands
var builtinCmds = map[string]func(*TestScript, bool, []string){
	"cd":     (*TestScript).cmdCD,
	"cp":     (*TestScript).cmdCp,
	"env":    (*TestScript).cmdEnv,
	"exec":   (*TestScript).cmdExecBuiltin,
	"exists": (*TestScript).cmdExists,
	"grep":   (*TestScript).cmdGrep,
	"mkdir":  (*TestScript).cmdMkdir,
	"rm":     (*TestScript).cmdRm,
	"skip":   (*TestScript).cmdSkip,
	"stderr": (*TestScript).cmdStderr,
	"stdout": (*TestScript).cmdStdout,
	"stop":   (*TestScript).cmdStop,
	"wait":   (*TestScript).cmdWait,
}

// Helper functions and remaining method implementations...

// getLine returns the first line and the remainder of the input.
func getLine(s string) (line, rest string) {
	i := strings.Index(s, "\n")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

// parse parses a command line into words, handling quotes and environment variables.
func (ts *TestScript) parse(line string) []string {
	// Expand environment variables
	expandedLine := ts.expandEnvVars(line)
	return strings.Fields(expandedLine)
}

// expandEnvVars expands environment variables in the form $VAR or ${VAR}
func (ts *TestScript) expandEnvVars(s string) string {
	return os.Expand(s, func(key string) string {
		if value, ok := ts.envMap[key]; ok {
			return value
		}
		return os.Getenv(key)
	})
}

// condition evaluates whether a condition should be satisfied.
func (ts *TestScript) condition(cond string) (bool, error) {
	if ts.params.Condition != nil {
		return ts.params.Condition(cond)
	}

	// Built-in conditions
	switch cond {
	case "short":
		return testing.Short(), nil
	case "windows":
		return runtime.GOOS == "windows", nil
	case "darwin":
		return runtime.GOOS == "darwin", nil
	case "linux":
		return runtime.GOOS == "linux", nil
	default:
		if strings.HasPrefix(cond, "!") {
			ok, err := ts.condition(cond[1:])
			return !ok, err
		}
		return false, fmt.Errorf("unknown condition %q", cond)
	}
}

// mkabs returns an absolute path for the given file within the test's work directory.
func (ts *TestScript) mkabs(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(ts.workdir, file)
}

// refreshEnvMap updates the environment variable map.
func (ts *TestScript) refreshEnvMap() {
	ts.envMap = make(map[string]string)
	for _, kv := range ts.env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			ts.envMap[k] = v
		}
	}
}

// Logf formats and logs a message.
func (ts *TestScript) Logf(format string, args ...any) {
	ts.t.Logf(format, args...)
}

// Log logs a message.
func (ts *TestScript) Log(args ...any) {
	ts.t.Log(args...)
}

// Fatalf formats and reports a fatal error.
func (ts *TestScript) Fatalf(format string, args ...any) {
	ts.t.Fatalf("script:%d: "+format, append([]any{ts.lineno}, args...)...)
}

// Fatal reports a fatal error.
func (ts *TestScript) Fatal(args ...any) {
	ts.t.Fatal(append([]any{fmt.Sprintf("script:%d:", ts.lineno)}, args...)...)
}

// ReadFile reads the named file and returns its contents.
func (ts *TestScript) ReadFile(filename string) string {
	filename = ts.mkabs(filename)
	data, err := os.ReadFile(filename)
	if err != nil {
		ts.t.Fatal(err)
	}
	return string(data)
}

// Chdir changes the current directory.
func (ts *TestScript) Chdir(dir string) {
	ts.cmdCD(false, []string{"cd", dir})
}

// Getenv retrieves the value of the environment variable named by the key.
func (ts *TestScript) Getenv(key string) string {
	return ts.envMap[key]
}

// Setenv sets the value of the environment variable named by the key.
func (ts *TestScript) Setenv(key, value string) {
	ts.cmdEnv(false, []string{"env", key + "=" + value})
}

// Exec runs the named program with the given arguments.
func (ts *TestScript) Exec(name string, args ...string) error {
	cmdArgs := append([]string{"exec", name}, args...)
	ts.cmdExecBuiltin(false, cmdArgs)
	if ts.t.Failed() {
		return errors.New("exec failed")
	}
	return nil
}

// Built-in command implementations

func (ts *TestScript) cmdCD(neg bool, args []string) {
	if len(args) != 2 {
		ts.t.Fatalf("script:%d: usage: cd dir", ts.lineno)
	}
	dir := args[1]
	if !filepath.IsAbs(dir) {
		if ts.cd == "" {
			if ts.workdir == "" {
				ts.t.Fatalf("script:%d: workdir not initialized", ts.lineno)
			}
			ts.cd = ts.workdir
		}
		dir = filepath.Join(ts.cd, dir)
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		ts.t.Fatalf("script:%d: directory %s does not exist", ts.lineno, dir)
	}
	if err != nil {
		ts.t.Fatalf("script:%d: %v", ts.lineno, err)
	}
	if !info.IsDir() {
		ts.t.Fatalf("script:%d: %s is not a directory", ts.lineno, dir)
	}
	ts.cd = dir
}

func (ts *TestScript) cmdCp(neg bool, args []string) {
	if len(args) < 3 {
		ts.t.Fatalf("script:%d: usage: cp src... dst", ts.lineno)
	}
	// Implementation would copy files
	ts.t.Fatalf("script:%d: cp command not fully implemented", ts.lineno)
}

func (ts *TestScript) cmdEnv(neg bool, args []string) {
	if len(args) == 1 {
		// Print all environment variables
		for _, env := range ts.env {
			ts.t.Log(env)
		}
		return
	}
	if len(args) != 2 {
		ts.t.Fatalf("script:%d: usage: env [key=value]", ts.lineno)
	}
	kv := args[1]
	if k, v, ok := strings.Cut(kv, "="); ok {
		entry := k + "=" + v
		replaced := false
		for i, existing := range ts.env {
			if ek, _, eok := strings.Cut(existing, "="); eok && ek == k {
				ts.env[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			ts.env = append(ts.env, entry)
		}
		ts.envMap[k] = v
	} else {
		ts.t.Fatalf("script:%d: env: no '=' in argument", ts.lineno)
	}
}

func (ts *TestScript) cmdExecBuiltin(neg bool, args []string) {
	if len(args) < 2 {
		ts.t.Fatalf("script:%d: usage: exec program [args...]", ts.lineno)
	}

	var err error
	if len(args) > 2 && backgroundSpecifier.MatchString(args[len(args)-1]) {
		// Background execution
		bgName := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-1], "&"), "&")
		if bgName == "" {
			bgName = fmt.Sprintf("bg%d", len(ts.background))
		}
		if ts.findBackground(bgName) != nil {
			ts.t.Fatalf("script:%d: duplicate background process name %q", ts.lineno, bgName)
		}

		cmd, execErr := ts.buildExecCmd(args[1], args[2:len(args)-1])
		if execErr != nil {
			err = execErr
		} else {
			bg := backgroundCmd{
				name: bgName,
				cmd:  cmd,
				neg:  neg,
			}
			cmd.Stdout = &bg.stdout
			cmd.Stderr = &bg.stderr
			wait := make(chan struct{})
			go func() {
				ts.waitOrStop(context.Background(), cmd, -1)
				close(wait)
			}()
			bg.wait = wait
			ts.background = append(ts.background, bg)
		}
		ts.stdout, ts.stderr = "", ""
	} else {
		// Foreground execution
		ts.stdout, ts.stderr, err = ts.exec(args[1], args[2:]...)
		if ts.stdout != "" {
			ts.t.Logf("[stdout]\n%s", ts.stdout)
		}
		if ts.stderr != "" {
			ts.t.Logf("[stderr]\n%s", ts.stderr)
		}
	}

	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			ts.t.Fatalf("script:%d: exec %s: %v", ts.lineno, args[1], err)
			return
		}
		// Command exited non-zero
		if !neg {
			ts.t.Fatalf("script:%d: %s failed: %v\n%s", ts.lineno, args[1], err, ts.stderr)
			return
		}
	} else {
		// Command succeeded
		if neg {
			ts.t.Fatalf("script:%d: unexpected command success", ts.lineno)
			return
		}
	}
}

func (ts *TestScript) cmdExists(neg bool, args []string) {
	if len(args) != 2 {
		ts.t.Fatalf("script:%d: usage: exists file", ts.lineno)
	}
	file := ts.mkabs(args[1])
	_, err := os.Stat(file)
	exists := err == nil
	if neg {
		exists = !exists
	}
	if !exists {
		if neg {
			ts.t.Fatalf("script:%d: file %s exists unexpectedly", ts.lineno, file)
		} else {
			ts.t.Fatalf("script:%d: file %s does not exist", ts.lineno, file)
		}
	}
}

func (ts *TestScript) cmdGrep(neg bool, args []string) {
	if len(args) != 3 {
		ts.t.Fatalf("script:%d: usage: grep pattern file", ts.lineno)
	}
	pattern := args[1]
	filename := ts.mkabs(args[2])

	data, err := os.ReadFile(filename)
	if err != nil {
		ts.t.Fatalf("script:%d: grep %s: %v", ts.lineno, filename, err)
	}

	content := string(data)
	match := strings.Contains(content, pattern)
	if match == neg {
		if neg {
			ts.t.Fatalf("script:%d: file %s unexpectedly contains %q", ts.lineno, filename, pattern)
		} else {
			ts.t.Fatalf("script:%d: file %s does not contain %q", ts.lineno, filename, pattern)
		}
	}
}

func (ts *TestScript) cmdMkdir(neg bool, args []string) {
	if len(args) < 2 {
		ts.t.Fatalf("script:%d: usage: mkdir dir...", ts.lineno)
	}
	for _, arg := range args[1:] {
		dir := ts.mkabs(arg)
		if err := os.MkdirAll(dir, 0777); err != nil {
			ts.t.Fatalf("script:%d: mkdir %s: %v", ts.lineno, dir, err)
		}
	}
}

func (ts *TestScript) cmdRm(neg bool, args []string) {
	if len(args) < 2 {
		ts.t.Fatalf("script:%d: usage: rm file...", ts.lineno)
	}
	for _, arg := range args[1:] {
		file := ts.mkabs(arg)
		if err := removeAll(file); err != nil {
			ts.t.Fatalf("script:%d: rm %s: %v", ts.lineno, file, err)
		}
	}
}

func (ts *TestScript) cmdSkip(neg bool, args []string) {
	if len(args) > 1 {
		ts.t.Skip(args[1])
	} else {
		ts.t.Skip()
	}
}

func (ts *TestScript) cmdStderr(neg bool, args []string) {
	if len(args) != 2 {
		ts.t.Fatalf("script:%d: usage: stderr text", ts.lineno)
	}
	pattern := args[1]
	match := strings.Contains(ts.stderr, pattern)
	if match == neg {
		if neg {
			ts.t.Fatalf("script:%d: stderr unexpectedly contains %q", ts.lineno, pattern)
		} else {
			ts.t.Fatalf("script:%d: stderr does not contain %q", ts.lineno, pattern)
		}
	}
}

func (ts *TestScript) cmdStdout(neg bool, args []string) {
	if len(args) != 2 {
		ts.t.Fatalf("script:%d: usage: stdout text", ts.lineno)
	}
	pattern := args[1]
	match := strings.Contains(ts.stdout, pattern)
	if match == neg {
		if neg {
			ts.t.Fatalf("script:%d: stdout unexpectedly contains %q", ts.lineno, pattern)
		} else {
			ts.t.Fatalf("script:%d: stdout does not contain %q", ts.lineno, pattern)
		}
	}
}

func (ts *TestScript) cmdStop(neg bool, args []string) {
	ts.stopped = true
}

func (ts *TestScript) cmdWait(neg bool, args []string) {
	var bgcmds []*backgroundCmd
	if len(args) == 1 {
		// Wait for all background commands
		bgcmds = make([]*backgroundCmd, len(ts.background))
		for i := range ts.background {
			bgcmds[i] = &ts.background[i]
		}
	} else {
		// Wait for specific background commands
		for _, name := range args[1:] {
			bg := ts.findBackground(name)
			if bg == nil {
				ts.t.Fatalf("script:%d: unknown background process %q", ts.lineno, name)
			}
			bgcmds = append(bgcmds, bg)
		}
	}

	var stdouts, stderrs []string
	for _, bg := range bgcmds {
		<-bg.wait

		// Collect output
		if bg.stdout.Len() > 0 {
			stdouts = append(stdouts, bg.stdout.String())
		}
		if bg.stderr.Len() > 0 {
			stderrs = append(stderrs, bg.stderr.String())
		}

		// Check exit status
		var err error
		if bg.cmd.ProcessState != nil && !bg.cmd.ProcessState.Success() {
			err = &exec.ExitError{ProcessState: bg.cmd.ProcessState}
		}

		success := err == nil
		if success != !bg.neg {
			if bg.neg {
				ts.t.Fatalf("script:%d: unexpected command success", ts.lineno)
			} else {
				ts.t.Fatalf("script:%d: unexpected command failure", ts.lineno)
			}
		}
	}

	// Update stdout/stderr with combined output
	ts.stdout = strings.Join(stdouts, "")
	ts.stderr = strings.Join(stderrs, "")

	// Remove completed background commands
	if len(args) == 1 {
		ts.background = nil
	} else {
		// Remove specific commands
		for _, name := range args[1:] {
			ts.removeBackground(name)
		}
	}
}

// Utility functions

func removeAll(path string) error {
	return os.RemoveAll(path)
}

func homeEnvName() string {
	switch runtime.GOOS {
	case "windows":
		return "USERPROFILE"
	case "plan9":
		return "home"
	default:
		return "HOME"
	}
}

func tempEnvName() string {
	switch runtime.GOOS {
	case "windows":
		return "TMP"
	case "plan9":
		return "TMPDIR" // actually plan 9 doesn't have one at all but this is fine
	default:
		return "TMPDIR"
	}
}

// exec executes a command and returns stdout, stderr, and any error
func (ts *TestScript) exec(name string, args ...string) (stdout, stderr string, err error) {
	cmd, err := ts.buildExecCmd(name, args)
	if err != nil {
		return "", "", err
	}

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// buildExecCmd creates an exec.Cmd for the given command and arguments
func (ts *TestScript) buildExecCmd(name string, args []string) (*exec.Cmd, error) {
	var cmd *exec.Cmd

	// If name contains path separators, use it as is
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		cmd = exec.Command(name, args...)
	} else {
		// Look for the command in PATH
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("command %q not found: %v", name, err)
		}
		cmd = exec.Command(path, args...)
	}

	cmd.Dir = ts.cd
	cmd.Env = append(ts.env, "PWD="+ts.cd)

	return cmd, nil
}

// waitOrStop waits for a command to complete or stops it after timeout
func (ts *TestScript) waitOrStop(ctx context.Context, cmd *exec.Cmd, interrupt time.Duration) error {
	if cmd.Process == nil {
		if err := cmd.Start(); err != nil {
			return err
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	if interrupt < 0 {
		// No timeout, just wait
		return <-done
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Context cancelled, try to stop the process
		if cmd.Process != nil {
			if runtime.GOOS == "windows" {
				cmd.Process.Kill()
			} else {
				cmd.Process.Signal(os.Interrupt)
				// Give it time to stop gracefully
				select {
				case <-done:
					return nil
				case <-time.After(interrupt):
					cmd.Process.Kill()
				}
			}
		}
		return ctx.Err()
	}
}

// findBackground finds a background command by name
func (ts *TestScript) findBackground(name string) *backgroundCmd {
	for i := range ts.background {
		if ts.background[i].name == name {
			return &ts.background[i]
		}
	}
	return nil
}

// removeBackground removes a background command by name
func (ts *TestScript) removeBackground(name string) {
	for i := range ts.background {
		if ts.background[i].name == name {
			ts.background = slices.Delete(ts.background, i, i+1)
			return
		}
	}
}
