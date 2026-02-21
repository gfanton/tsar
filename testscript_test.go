package tstar

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

func TestTsarBasic(t *testing.T) {
	Run(t, Params{
		Dir: "examples/testdata",
	})
}

func TestLookPathUsesTestEnvPATH(t *testing.T) {
	// Create a temp directory with a custom binary
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "myhelper")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hello-from-myhelper\n"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Run a .tsar test that sets PATH to include our binDir,
	// then execs "myhelper" which should be found via test env PATH
	testDir := t.TempDir()
	tsarContent := "env PATH=" + binDir + "\nexec myhelper\nstdout hello-from-myhelper\n"
	err = os.WriteFile(filepath.Join(testDir, "test_path.tsar"), []byte(tsarContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	Run(t, Params{Dir: testDir})
}

func TestPerTestSetupTeardown(t *testing.T) {
	dir := t.TempDir()

	// Create a per-test setup script that writes a marker file
	setupScript := filepath.Join(dir, "per_test_setup.sh")
	writeFile(t, setupScript, []byte("#!/bin/sh\necho setup-ran > \"$WORK/setup-marker\"\n"), 0755)

	// Create a per-test teardown script that writes a marker file
	teardownScript := filepath.Join(dir, "per_test_teardown.sh")
	writeFile(t, teardownScript, []byte("#!/bin/sh\necho teardown-ran > \"$WORK/teardown-marker\"\n"), 0755)

	// Create a .tsar test that checks setup ran and creates a file for teardown check
	tsarContent := "exists setup-marker\ngrep setup-ran setup-marker\n"
	writeFile(t, filepath.Join(dir, "test_hooks.tsar"), []byte(tsarContent), 0644)

	Run(t, Params{
		Dir:          dir,
		TestSetup:    setupScript,
		TestTeardown: teardownScript,
	})

	// Note: We can't easily check teardown ran from inside the test because
	// the work dir is cleaned up. We verify it by ensuring no error occurred.
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    []string
		wantErr bool
	}{
		{
			name: "no quotes",
			line: "hello world",
			want: []string{"hello", "world"},
		},
		{
			name: "single quoted arg without spaces",
			line: `"hello"`,
			want: []string{"hello"},
		},
		{
			name: "quoted arg with spaces",
			line: `"hello world"`,
			want: []string{"hello world"},
		},
		{
			name: "mixed quoted and unquoted",
			line: `foo "hello world" bar`,
			want: []string{"foo", "hello world", "bar"},
		},
		{
			name:    "unclosed quote",
			line:    `"hello world`,
			wantErr: true,
		},
		{
			name: "escaped quote inside",
			line: `"hello \"world\""`,
			want: []string{`hello "world"`},
		},
		{
			name: "empty input",
			line: "",
			want: nil,
		},
		{
			name: "multiple quoted segments",
			line: `"foo bar" "baz qux"`,
			want: []string{"foo bar", "baz qux"},
		},
		{
			name: "preserves multiple spaces inside quotes",
			line: `"hello  world"`,
			want: []string{"hello  world"},
		},
		{
			name: "tabs as separators",
			line: "foo\tbar",
			want: []string{"foo", "bar"},
		},
		{
			name: "quoted with tabs inside",
			line: "\"foo\tbar\"",
			want: []string{"foo\tbar"},
		},
		{
			name: "escaped backslash",
			line: `"hello\\world"`,
			want: []string{`hello\world`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitArgs(tt.line)
			if (err != nil) != tt.wantErr {
				t.Fatalf("splitArgs(%q) error = %v, wantErr %v", tt.line, err, tt.wantErr)
			}
			if !tt.wantErr && !slices.Equal(got, tt.want) {
				t.Errorf("splitArgs(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseWithQuotes(t *testing.T) {
	dir := t.TempDir()

	// Test that parse() handles quoted arguments in a .tsar script.
	// Use a custom command to capture the args it receives.
	var capturedArgs []string
	tsarContent := `capture -header "Content-Type: application/json" -body "hello world"` + "\n"
	writeFile(t, filepath.Join(dir, "test_quotes.tsar"), []byte(tsarContent), 0644)

	Run(t, Params{
		Dir: dir,
		Commands: map[string]func(*TestScript, bool, []string){
			"capture": func(ts *TestScript, neg bool, args []string) {
				capturedArgs = args[1:] // skip command name
			},
		},
	})

	want := []string{"-header", "Content-Type: application/json", "-body", "hello world"}
	if !slices.Equal(capturedArgs, want) {
		t.Errorf("captured args = %v, want %v", capturedArgs, want)
	}
}

// ---- Script-driven tests

func TestExec(t *testing.T) {
	Run(t, Params{Dir: "testdata/exec"})
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(testHTTPHandler))
	defer srv.Close()

	Run(t, Params{
		Dir: "testdata/http",
		Setup: func(env *Env) error {
			env.Setenv("SERVER", srv.URL)
			return nil
		},
	})
}

func TestHTTPRepeat(t *testing.T) {
	var flakyCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flaky":
			n := flakyCount.Add(1)
			if n%3 == 0 {
				w.WriteHeader(500)
				fmt.Fprint(w, "server error")
				return
			}
			fmt.Fprint(w, "ok")
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	defer srv.Close()

	Run(t, Params{
		Dir: "testdata/http_repeat",
		Setup: func(env *Env) error {
			env.Setenv("SERVER", srv.URL)
			return nil
		},
	})
}

// ---- Error meta-tests (assert the framework itself fails correctly)

func TestHTTPStatusWithoutPriorHTTP(t *testing.T) {
	dir := t.TempDir()
	tsarContent := "httpstatus 200\n"
	writeFile(t, filepath.Join(dir, "test_no_http.tsar"), []byte(tsarContent), 0644)

	runner := &testResultCapture{}
	RunFilesStandalone(runner, Params{Dir: dir}, filepath.Join(dir, "test_no_http.tsar"))
	if !runner.Failed() {
		t.Fatal("expected failure when httpstatus called without prior http")
	}
}

func TestRepeatUnsupportedCommand(t *testing.T) {
	dir := t.TempDir()
	tsarContent := "repeat 5 exists foo\n"
	writeFile(t, filepath.Join(dir, "test_repeat_bad.tsar"), []byte(tsarContent), 0644)

	runner := &testResultCapture{}
	RunFilesStandalone(runner, Params{Dir: dir}, filepath.Join(dir, "test_repeat_bad.tsar"))
	if !runner.Failed() {
		t.Fatal("expected failure for unsupported repeat command")
	}
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

// ---- Test HTTP handler

func testHTTPHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/health":
		fmt.Fprint(w, "ok")
	case r.Method == "GET" && r.URL.Path == "/api/info":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	case r.Method == "POST" && r.URL.Path == "/api/echo":
		if ct := r.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	case r.URL.Path == "/created":
		w.WriteHeader(201)
		fmt.Fprint(w, "body")
	case r.URL.Path == "/echo/headers":
		w.Header().Set("Content-Type", "text/plain")
		for _, name := range []string{"Authorization", "Accept"} {
			if v := r.Header.Get(name); v != "" {
				fmt.Fprintf(w, "%s: %s\n", name, v)
			}
		}
	case r.URL.Path == "/with-headers":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Request-Id", "abc-123")
		fmt.Fprint(w, "ok")
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, "not found")
	}
}
