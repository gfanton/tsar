package tsar

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testResultCapture implements TestingT for standalone test execution in tests.
type testResultCapture struct {
	failed bool
}

func (t *testResultCapture) Skip(args ...any)  {}
func (t *testResultCapture) Fatal(args ...any) { t.failed = true }
func (t *testResultCapture) Fatalf(format string, args ...any) {
	t.failed = true
	fmt.Printf("CAPTURED FAIL: "+format+"\n", args...)
}
func (t *testResultCapture) Log(args ...any)                 {}
func (t *testResultCapture) Logf(format string, args ...any) {}
func (t *testResultCapture) Failed() bool                    { return t.failed }
func (t *testResultCapture) Helper()                         {}

func writeFile(t *testing.T, path string, content []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatal(err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectConfig_WithTOML(t *testing.T) {
	dir := t.TempDir()

	// Create a tsar.toml with all fields
	toml := `bin = "mybin"
setup = "my_setup.sh"
teardown = "my_teardown.sh"

[test]
setup = "scripts/before.sh"
teardown = "scripts/after.sh"
`
	writeFile(t, filepath.Join(dir, "tsar.toml"), []byte(toml), 0644)

	// Create referenced paths
	mkdirAll(t, filepath.Join(dir, "mybin"))
	writeFile(t, filepath.Join(dir, "my_setup.sh"), []byte("#!/bin/sh\n"), 0755)
	writeFile(t, filepath.Join(dir, "my_teardown.sh"), []byte("#!/bin/sh\n"), 0755)
	mkdirAll(t, filepath.Join(dir, "scripts"))
	writeFile(t, filepath.Join(dir, "scripts", "before.sh"), []byte("#!/bin/sh\n"), 0755)
	writeFile(t, filepath.Join(dir, "scripts", "after.sh"), []byte("#!/bin/sh\n"), 0755)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if filepath.Base(cfg.BinDir) != "mybin" {
		t.Errorf("BinDir = %q, want suffix mybin", cfg.BinDir)
	}
	if filepath.Base(cfg.Setup) != "my_setup.sh" {
		t.Errorf("Setup = %q, want suffix my_setup.sh", cfg.Setup)
	}
	if filepath.Base(cfg.Teardown) != "my_teardown.sh" {
		t.Errorf("Teardown = %q, want suffix my_teardown.sh", cfg.Teardown)
	}
	if filepath.Base(cfg.Test.Setup) != "before.sh" {
		t.Errorf("Test.Setup = %q, want suffix before.sh", cfg.Test.Setup)
	}
	if filepath.Base(cfg.Test.Teardown) != "after.sh" {
		t.Errorf("Test.Teardown = %q, want suffix after.sh", cfg.Test.Teardown)
	}
}

func TestLoadProjectConfig_AutoDetect(t *testing.T) {
	dir := t.TempDir()

	// Create conventional files (no tsar.toml)
	mkdirAll(t, filepath.Join(dir, "bin"))
	writeFile(t, filepath.Join(dir, "setup.sh"), []byte("#!/bin/sh\n"), 0755)
	writeFile(t, filepath.Join(dir, "teardown.sh"), []byte("#!/bin/sh\n"), 0755)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if filepath.Base(cfg.BinDir) != "bin" {
		t.Errorf("BinDir = %q, want suffix bin", cfg.BinDir)
	}
	if filepath.Base(cfg.Setup) != "setup.sh" {
		t.Errorf("Setup = %q, want suffix setup.sh", cfg.Setup)
	}
	if filepath.Base(cfg.Teardown) != "teardown.sh" {
		t.Errorf("Teardown = %q, want suffix teardown.sh", cfg.Teardown)
	}
	// Test hooks should remain empty (no auto-detection for those)
	if cfg.Test.Setup != "" {
		t.Errorf("Test.Setup = %q, want empty (no auto-detection)", cfg.Test.Setup)
	}
	if cfg.Test.Teardown != "" {
		t.Errorf("Test.Teardown = %q, want empty (no auto-detection)", cfg.Test.Teardown)
	}
}

func TestLoadProjectConfig_TOMLOverridesAutoDetect(t *testing.T) {
	dir := t.TempDir()

	// Create both conventional and overridden paths
	mkdirAll(t, filepath.Join(dir, "bin"))
	mkdirAll(t, filepath.Join(dir, "custom-bin"))
	writeFile(t, filepath.Join(dir, "setup.sh"), []byte("#!/bin/sh\n"), 0755)

	toml := `bin = "custom-bin"
`
	writeFile(t, filepath.Join(dir, "tsar.toml"), []byte(toml), 0644)

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// TOML should override auto-detected bin
	if filepath.Base(cfg.BinDir) != "custom-bin" {
		t.Errorf("BinDir = %q, want suffix custom-bin", cfg.BinDir)
	}
	// setup.sh should still be auto-detected (not in TOML)
	if filepath.Base(cfg.Setup) != "setup.sh" {
		t.Errorf("Setup = %q, want suffix setup.sh", cfg.Setup)
	}
}

func TestLoadProjectConfig_TOMLMissingPath(t *testing.T) {
	dir := t.TempDir()

	toml := `bin = "nonexistent-dir"
`
	writeFile(t, filepath.Join(dir, "tsar.toml"), []byte(toml), 0644)

	_, err := LoadProjectConfig(dir)
	if err == nil {
		t.Fatal("expected error for nonexistent TOML path, got nil")
	}
}

func TestLoadProjectConfig_InvalidTOML(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "tsar.toml"), []byte("invalid [[[toml"), 0644)

	_, err := LoadProjectConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

func TestPrepareBinDir_ShellWrappers(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	mkdirAll(t, binDir)

	// Create a .sh file in bin
	writeFile(t, filepath.Join(binDir, "greet.sh"), []byte("#!/bin/sh\necho \"hello $1\"\n"), 0755)
	// Create a non-.sh executable
	writeFile(t, filepath.Join(binDir, "helper"), []byte("#!/bin/sh\necho helper-output\n"), 0755)

	cfg := &ProjectConfig{BinDir: binDir, dir: dir}
	pathDirs, cleanup, err := cfg.prepareBinDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	if len(pathDirs) != 2 {
		t.Fatalf("pathDirs len = %d, want 2 (wrapper dir + bin dir)", len(pathDirs))
	}

	// The wrapper dir should contain a "greet" script (no .sh extension)
	wrapperDir := pathDirs[0]
	entries, err := os.ReadDir(wrapperDir)
	if err != nil {
		t.Fatalf("read wrapper dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "greet" {
		t.Errorf("wrapper dir entries = %v, want [greet]", names)
	}

	// The wrapper should invoke the original .sh script
	newPATH := strings.Join(pathDirs, string(os.PathListSeparator))
	cmd := exec.Command(filepath.Join(wrapperDir, "greet"), "world")
	cmd.Env = []string{"PATH=" + newPATH}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("greet wrapper failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello world" {
		t.Errorf("greet output = %q, want %q", got, "hello world")
	}

	// The non-.sh file "helper" should be directly accessible via the bin dir
	cmd2 := exec.Command(filepath.Join(binDir, "helper"))
	out2, err := cmd2.Output()
	if err != nil {
		t.Fatalf("helper exec failed: %v", err)
	}
	if got := strings.TrimSpace(string(out2)); got != "helper-output" {
		t.Errorf("helper output = %q, want %q", got, "helper-output")
	}
}

func TestPrepareBinDir_EmptyBin(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	mkdirAll(t, binDir)

	cfg := &ProjectConfig{BinDir: binDir, dir: dir}
	pathDirs, cleanup, err := cfg.prepareBinDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Should still return the bin dir in path
	if len(pathDirs) != 2 {
		t.Fatalf("pathDirs len = %d, want 2", len(pathDirs))
	}
}

func TestPrepareBinDir_NoBinDir(t *testing.T) {
	cfg := &ProjectConfig{dir: t.TempDir()}
	pathDirs, cleanup, err := cfg.prepareBinDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	if len(pathDirs) != 0 {
		t.Errorf("pathDirs len = %d, want 0 for no bin dir", len(pathDirs))
	}
}

// ---- RunWithProject Integration Tests

func TestRunWithProject_GlobalSetupAndBin(t *testing.T) {
	dir := t.TempDir()

	// Create bin/greet.sh
	mkdirAll(t, filepath.Join(dir, "bin"))
	writeFile(t, filepath.Join(dir, "bin", "greet.sh"),
		[]byte("#!/bin/sh\necho \"hello $1\"\n"), 0755)

	// Create global setup.sh that writes a marker
	writeFile(t, filepath.Join(dir, "setup.sh"),
		[]byte("#!/bin/sh\necho global-setup-ran > \"$PWD/setup-ran.marker\"\n"), 0755)

	// Create global teardown.sh
	writeFile(t, filepath.Join(dir, "teardown.sh"),
		[]byte("#!/bin/sh\necho global-teardown-ran\n"), 0755)

	// Create test that uses the greet command from bin/
	writeFile(t, filepath.Join(dir, "test_project.tsar"),
		[]byte("exec greet world\nstdout hello\n"), 0644)

	RunWithProject(t, Params{Dir: dir})
}

func TestRunWithProject_SetupFailurePreventsTests(t *testing.T) {
	dir := t.TempDir()

	// Create a setup.sh that fails
	writeFile(t, filepath.Join(dir, "setup.sh"),
		[]byte("#!/bin/sh\nexit 1\n"), 0755)

	writeFile(t, filepath.Join(dir, "test_should_not_run.tsar"),
		[]byte("exec echo should-not-reach-here\n"), 0644)

	// Use a capture to check that tests don't run (setup fails fatally)
	runner := &testResultCapture{}
	err := RunStandaloneWithProject(runner, Params{Dir: dir})
	if err == nil {
		t.Fatal("expected error from failed setup.sh, got nil")
	}
}

func TestRunWithProject_EmptyProject(t *testing.T) {
	// A directory with just .tsar files (no bin, no setup, no teardown)
	// should work identically to Run()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "test_simple.tsar"),
		[]byte("exec echo works\nstdout works\n"), 0644)

	RunWithProject(t, Params{Dir: dir})
}

func TestRunWithProject_WithTOML(t *testing.T) {
	dir := t.TempDir()

	mkdirAll(t, filepath.Join(dir, "scripts"))
	writeFile(t, filepath.Join(dir, "scripts", "before.sh"),
		[]byte("#!/bin/sh\necho before-each > \"$WORK/before-marker\"\n"), 0755)

	tomlContent := `[test]
setup = "scripts/before.sh"
`
	writeFile(t, filepath.Join(dir, "tsar.toml"), []byte(tomlContent), 0644)

	writeFile(t, filepath.Join(dir, "test_toml.tsar"),
		[]byte("exists before-marker\ngrep before-each before-marker\n"), 0644)

	RunWithProject(t, Params{Dir: dir})
}

func TestLoadProjectConfig_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error for empty dir: %v", err)
	}

	// Everything should be empty
	if cfg.BinDir != "" {
		t.Errorf("BinDir = %q, want empty", cfg.BinDir)
	}
	if cfg.Setup != "" {
		t.Errorf("Setup = %q, want empty", cfg.Setup)
	}
	if cfg.Teardown != "" {
		t.Errorf("Teardown = %q, want empty", cfg.Teardown)
	}
}
