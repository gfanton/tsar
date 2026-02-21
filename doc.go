// Copyright 2024 The testscript Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package tsar provides support for defining filesystem-based tests by
creating scripts in a directory using .tsar files (testscript archive format).

This package is heavily inspired by and adapted from the testscript package
originally developed by Roger Peppe:
https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript

To invoke the tests, call [Run]:

	func TestFoo(t *testing.T) {
		tsar.Run(t, tsar.Params{
			Dir: "testdata",
		})
	}

The package scans the directory for files with .tsar suffix and runs each
one as a separate subtest.

A script is a text file executed line-by-line. It can contain commands,
comments (lines starting with #), conditional execution, and embedded
files using the txtar format.

# Negation

Any command can be prefixed with ! to expect failure:

	! exec false
	! http GET $SERVER/missing

# Commands

The following built-in commands are available:

	cd <dir>                                Change directory
	cp <src> <dst>                          Copy file
	env [key=value]                         Set/print environment variables
	exec <cmd> [args...]                    Execute external command
	exists <file>                           Check that file exists
	grep <pattern> <file>                   Check that file contains pattern
	mkdir <dir>...                          Create directories
	rm <file>...                            Remove files/directories
	skip [message]                          Skip the test
	stop                                    Stop test execution
	wait [name...]                          Wait for background commands
	stdout <pattern>                        Assert last command stdout contains pattern
	stderr <pattern>                        Assert last command stderr contains pattern

# HTTP Commands

	http METHOD URL [-body FILE] [-header "Key: Value"]...

Performs an HTTP request. The response body is captured in stdout for
assertion with the stdout command. Non-2xx status codes are treated as
failure (use ! prefix to expect non-success).

	httpstatus CODE                         Assert last HTTP response status code
	httpheader NAME VALUE                   Assert last HTTP response header contains value

Example:

	http GET $SERVER/api/info
	stdout healthy
	httpstatus 200
	httpheader Content-Type application/json

	http POST $SERVER/api/echo -body request.json -header "Content-Type: application/json"
	stdout result

	! http GET $SERVER/missing
	httpstatus 404

# Repeat Command

	repeat [-all] COUNT exec <cmd> [args...]
	repeat [-all] COUNT http METHOD URL [flags...]

Runs a command COUNT times. Without -all, stops at first failure.
With -all, runs all iterations and reports stats. The summary is
written to stderr for assertion:

	repeat 10 exec echo hello
	stderr "10/10 passed"

	! repeat -all 9 http GET $SERVER/flaky
	stderr "6/9 passed"
	stderr "3/9 failed"

# Background Execution

Commands can be run in the background by appending &name:

	exec long-running-server &srv
	exec curl http://localhost:8080
	wait srv

# Conditional Execution

Lines can be prefixed with conditions in square brackets:

	[!windows] mkdir unix-only-dir
	[short] skip "skipping in short mode"

Built-in conditions: short, windows, darwin, linux.
Prefix with ! to negate: [!short].

# Embedded Files

Scripts can contain embedded files using txtar format:

	exec cat input.txt
	stdout "hello"

	-- input.txt --
	hello world

# Custom Commands

Register custom commands via [Params].Commands:

	tsar.Run(t, tsar.Params{
		Dir: "testdata",
		Commands: map[string]func(*tsar.TestScript, bool, []string){
			"mycommand": handleMyCommand,
		},
	})

# Setup

Use [Params].Setup to inject environment variables (e.g., server URLs):

	srv := httptest.NewServer(handler)
	tsar.Run(t, tsar.Params{
		Dir: "testdata/http",
		Setup: func(env *tsar.Env) error {
			env.Setenv("SERVER", srv.URL)
			return nil
		},
	})

# Command-line Tool

The tsar command provides a standalone way to run test scripts:

	tsar testdata/              # Run all .tsar files in directory
	tsar testdata/example.tsar  # Run specific file
	tsar --verbose testdata/    # Verbose output

Flags: -v/--verbose, -s/--short, --test-work, -w/--workdir-root,
-c/--continue-on-error, -e/--require-explicit-exec, -u/--require-unique-names.

Environment variables with TSAR_ prefix are also supported.

# Attribution

Inspired by and adapted from the testscript package by Roger Peppe:
https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript
*/
package tsar
