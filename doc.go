// Copyright 2024 The testscript Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package testscript provides support for defining filesystem-based tests by
creating scripts in a directory using .tsar files (testscript archive format).

This package is heavily inspired by and adapted from the testscript package
originally developed by Roger Peppe:
https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript

The core design patterns, API structure, and implementation approach have been
preserved while adapting the functionality to work with .tsar files and custom
command registration.

To invoke the tests, call testscript.Run. For example:

	func TestFoo(t *testing.T) {
		testscript.Run(t, testscript.Params{
			Dir: "testdata",
		})
	}

The testscript package will scan the directory for files with .tsar suffix
and run each one as a separate test.

A script is a text file that is executed line-by-line. The script can contain
commands, comments (starting with #), conditional execution, and embedded
files using the txtar format.

The command-line tool tsar provides a standalone way to run test scripts:

	tsar testdata/              # Run all .tsar files in directory
	tsar testdata/example.tsar  # Run specific file
	tsar --verbose testdata/    # Verbose output

Available flags:
- -v, --verbose: Enable verbose output
- -s, --short: Run tests in short mode
- --test-work: Preserve work directories after tests
- -w, --workdir-root: Root directory for work directories
- -c, --continue-on-error: Continue executing tests after an error
- -e, --require-explicit-exec: Require explicit 'exec' for command execution
- -u, --require-unique-names: Require unique test names

Environment variables with TSAR_ prefix are supported:
TSAR_VERBOSE=true, TSAR_TEST_WORK=true, etc.

# Script Format

Each script file is a text file containing a sequence of commands to run,
comments, conditions, and optionally embedded files.

# Commands

The following commands are available:

- cd <dir>: Change to the given directory
- chmod <perm> <file>...: Change file permissions
- cmp <file1> <file2>: Check that two files are equal
- cmpenv <file1> <file2>: Like cmp but expand environment variables
- cp <src> <dst>: Copy file or directory
- env <key>=<value>: Set environment variable
- exec <command> <args>...: Execute external command
- exists <file>: Check that file exists
- grep <pattern> <file>: Check that file contains pattern
- mkdir <dir>...: Create directories
- rm <file>...: Remove files/directories
- skip [message]: Skip the test
- stop: Stop test execution
- wait: Wait for background commands

# Conditional Execution

Lines can be prefixed with conditions in square brackets:

	[!windows] mkdir unix-only-dir
	[short] skip "skipping in short mode"
	[!short] exec long-running-command

Built-in conditions:
- short: Running with go test -short or tsar --short
- windows, darwin, linux: Operating system
- !condition: Negation of any condition

# Embedded Files

Scripts can contain embedded files using txtar format:

	# Commands here
	mkdir testdata

	-- testdata/input.txt --
	This content will be written to testdata/input.txt
	when the test runs.

	-- testdata/config.json --
	{
		"key": "value"
	}

# Custom Commands

Register custom commands by providing a Commands map:

	testscript.Run(t, testscript.Params{
		Dir: "testdata",
		Commands: map[string]func(*testscript.TestScript, bool, []string){
			"mycommand": handleMyCommand,
		},
	})

# TestScript API

Within custom commands, you have access to the TestScript context:

	func myCommand(ts *testscript.TestScript, neg bool, args []string) {
		// File operations
		content := ts.ReadFile("somefile.txt")

		// Environment
		workDir := ts.Getenv("WORK")
		ts.Setenv("MYVAR", "value")

		// Directory operations
		ts.Chdir("subdir")

		// Logging and errors
		ts.Logf("Processing %d items", len(args))
		if someError {
			ts.Fatalf("command failed: %v", err)
		}
	}

# Error Reporting

When a command fails, the script execution stops and the test is marked as failed.
The output includes the command that failed and the line number in the script.

# Debugging

Use the --test-work flag to preserve work directories for inspection:

	tsar --test-work testdata/

Or set the environment variable:

	TSAR_TEST_WORK=true tsar testdata/

# Attribution

This library is heavily inspired by and adapted from the testscript package:
- Original Author: Roger Peppe <rogpeppe@gmail.com>
- Original Package: https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript
- License: Original testscript package license applies

The core design patterns, API structure, and implementation approach have been
preserved while adapting the functionality to work with .tsar files and custom
command registration.
*/
package tstar
