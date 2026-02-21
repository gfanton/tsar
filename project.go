package tsar

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// ProjectConfig holds convention-based project configuration for a tsar test directory.
type ProjectConfig struct {
	BinDir   string    `toml:"bin"`
	Setup    string    `toml:"setup"`
	Teardown string    `toml:"teardown"`
	Test     TestHooks `toml:"test"`
	dir      string    // resolved absolute base directory
}

// TestHooks holds per-test setup/teardown script paths.
type TestHooks struct {
	Setup    string `toml:"setup"`
	Teardown string `toml:"teardown"`
}

// LoadProjectConfig loads project configuration from a directory.
// It reads tsar.toml if present, then auto-detects conventional files
// (bin/, setup.sh, teardown.sh) for any fields not set by the TOML.
// All paths in the returned config are absolute.
func LoadProjectConfig(dir string) (*ProjectConfig, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir: %w", err)
	}

	cfg := &ProjectConfig{dir: absDir}

	// Track which fields were explicitly set by TOML
	var fromTOML ProjectConfig
	hasTOML := false

	tomlPath := filepath.Join(absDir, "tsar.toml")
	data, err := os.ReadFile(tomlPath)
	if err == nil {
		hasTOML = true
		if err := toml.Unmarshal(data, &fromTOML); err != nil {
			return nil, fmt.Errorf("parse tsar.toml: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read tsar.toml: %w", err)
	}

	// Apply TOML values, then auto-detect missing ones
	cfg.BinDir = resolveField(absDir, fromTOML.BinDir, "bin", isDir)
	cfg.Setup = resolveField(absDir, fromTOML.Setup, "setup.sh", isFile)
	cfg.Teardown = resolveField(absDir, fromTOML.Teardown, "teardown.sh", isFile)
	cfg.Test.Setup = resolveExplicitOnly(absDir, fromTOML.Test.Setup)
	cfg.Test.Teardown = resolveExplicitOnly(absDir, fromTOML.Test.Teardown)

	// Validate that all TOML-specified paths exist
	if hasTOML {
		if err := cfg.validateTOMLPaths(absDir, &fromTOML); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// resolveField applies TOML value if set, otherwise auto-detects the conventional path.
func resolveField(base, tomlVal, convention string, check func(string) bool) string {
	if tomlVal != "" {
		return filepath.Join(base, tomlVal)
	}
	// Auto-detect conventional path
	candidate := filepath.Join(base, convention)
	if check(candidate) {
		return candidate
	}
	return ""
}

// resolveExplicitOnly resolves a path only if explicitly configured (no auto-detection).
func resolveExplicitOnly(base, tomlVal string) string {
	if tomlVal != "" {
		return filepath.Join(base, tomlVal)
	}
	return ""
}

func (cfg *ProjectConfig) validateTOMLPaths(base string, from *ProjectConfig) error {
	checks := []struct {
		val  string
		desc string
	}{
		{from.BinDir, "bin directory"},
		{from.Setup, "setup script"},
		{from.Teardown, "teardown script"},
		{from.Test.Setup, "test setup script"},
		{from.Test.Teardown, "test teardown script"},
	}
	for _, c := range checks {
		if c.val == "" {
			continue
		}
		abs := filepath.Join(base, c.val)
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("tsar.toml: %s %q not found: %w", c.desc, c.val, err)
		}
	}
	return nil
}

// prepareBinDir creates wrapper scripts for .sh files in the project's bin directory
// and returns PATH directory entries to prepend. The first entry is a temp dir with
// wrappers (calling .sh files without extension), the second is the bin dir itself
// (for non-.sh executables). Returns a cleanup function that removes the temp dir.
func (cfg *ProjectConfig) prepareBinDir() (pathDirs []string, cleanup func(), err error) {
	cleanup = func() {} // no-op default

	if cfg.BinDir == "" {
		return nil, cleanup, nil
	}

	entries, err := os.ReadDir(cfg.BinDir)
	if err != nil {
		return nil, cleanup, fmt.Errorf("read bin dir: %w", err)
	}

	wrapperDir, err := os.MkdirTemp("", "tsar-bin-*")
	if err != nil {
		return nil, cleanup, fmt.Errorf("create wrapper dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(wrapperDir) }

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".sh" {
			continue
		}
		// Create a wrapper script that invokes the .sh file
		wrapperName := strings.TrimSuffix(name, ".sh")
		absScript := filepath.Join(cfg.BinDir, name)
		wrapper := fmt.Sprintf("#!/bin/sh\nexec /bin/sh %q \"$@\"\n", absScript)
		wrapperPath := filepath.Join(wrapperDir, wrapperName)
		if err := os.WriteFile(wrapperPath, []byte(wrapper), 0755); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("write wrapper %s: %w", wrapperName, err)
		}
	}

	return []string{wrapperDir, cfg.BinDir}, cleanup, nil
}

// ---- Project-Aware Run Functions

// RunWithProject runs test scripts from p.Dir with project structure support.
// It loads the project config, prepares bin/ wrappers, runs global setup/teardown,
// and wires per-test hooks before delegating to Run.
func RunWithProject(t *testing.T, p Params) {
	cfg, err := LoadProjectConfig(p.Dir)
	if err != nil {
		t.Fatal(err)
	}

	cleanup, err := prepareProject(cfg, &p)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	Run(t, p)
}

// RunStandaloneWithProject is the standalone equivalent of RunWithProject.
// Returns an error if global setup fails or tests fail.
func RunStandaloneWithProject(t TestingT, p Params) error {
	cfg, err := LoadProjectConfig(p.Dir)
	if err != nil {
		return fmt.Errorf("load project config: %w", err)
	}

	cleanup, err := prepareProject(cfg, &p)
	if err != nil {
		return err
	}
	defer cleanup()

	RunStandalone(t, p)
	if t.Failed() {
		return fmt.Errorf("tests failed")
	}
	return nil
}

// RunFilesStandaloneWithProject runs specific files with project structure support.
func RunFilesStandaloneWithProject(t TestingT, p Params, filenames ...string) error {
	cfg, err := LoadProjectConfig(p.Dir)
	if err != nil {
		return fmt.Errorf("load project config: %w", err)
	}

	cleanup, err := prepareProject(cfg, &p)
	if err != nil {
		return err
	}
	defer cleanup()

	RunFilesStandalone(t, p, filenames...)
	if t.Failed() {
		return fmt.Errorf("tests failed")
	}
	return nil
}

// prepareProject sets up the project environment and returns a cleanup function.
// It prepares bin/ wrappers, runs global setup, wires per-test hooks, and
// returns a cleanup that runs global teardown and removes temp dirs.
func prepareProject(cfg *ProjectConfig, p *Params) (cleanup func(), err error) {
	cleanup = func() {} // no-op default

	// Prepare bin/ directory
	binPathDirs, binCleanup, err := cfg.prepareBinDir()
	if err != nil {
		return cleanup, fmt.Errorf("prepare bin dir: %w", err)
	}

	// Wrap the user's Setup to prepend bin PATH dirs to test environment
	origSetup := p.Setup
	p.Setup = func(env *Env) error {
		if origSetup != nil {
			if err := origSetup(env); err != nil {
				return err
			}
		}
		if len(binPathDirs) > 0 {
			currentPATH := env.Getenv("PATH")
			newPATH := strings.Join(binPathDirs, string(os.PathListSeparator))
			if currentPATH != "" {
				newPATH += string(os.PathListSeparator) + currentPATH
			}
			env.Setenv("PATH", newPATH)
		}
		return nil
	}

	// Wire per-test hooks
	if cfg.Test.Setup != "" {
		p.TestSetup = cfg.Test.Setup
	}
	if cfg.Test.Teardown != "" {
		p.TestTeardown = cfg.Test.Teardown
	}

	// Run global setup
	if cfg.Setup != "" {
		if err := runGlobalScript(cfg.dir, cfg.Setup); err != nil {
			binCleanup()
			return func() {}, fmt.Errorf("global setup failed: %w", err)
		}
	}

	// Build cleanup: global teardown (best-effort) + bin cleanup
	projectDir := cfg.dir
	teardownScript := cfg.Teardown
	cleanup = func() {
		if teardownScript != "" {
			if err := runGlobalScript(projectDir, teardownScript); err != nil {
				log.Printf("warning: global teardown failed: %v", err)
			}
		}
		binCleanup()
	}

	return cleanup, nil
}

// runGlobalScript runs a shell script in the project directory.
func runGlobalScript(dir, scriptPath string) error {
	cmd := exec.Command("/bin/sh", scriptPath)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", filepath.Base(scriptPath), err, output)
	}
	return nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
