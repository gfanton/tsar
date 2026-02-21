# tsar - Script-driven Testing Library

[![Go Reference](https://pkg.go.dev/badge/github.com/gfanton/tsar.svg)](https://pkg.go.dev/github.com/gfanton/tsar)

`tsar` is a Go testing library for script-driven tests, inspired by the [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) package by [Roger Peppe](https://github.com/rogpeppe). Define tests as `.tsar` scripts with built-in support for HTTP testing, repeat/stress testing, and custom commands.

## Installation

```bash
# Library
go get github.com/gfanton/tsar

# Command-line tool
go install github.com/gfanton/tsar/cmd/tsar@latest
```

## Quick Start

Create `testdata/hello.tsar`:

```bash
exec echo "Hello, World!"
stdout "Hello, World!"
```

Write a Go test:

```go
func TestHello(t *testing.T) {
    tsar.Run(t, tsar.Params{Dir: "testdata"})
}
```

Each `.tsar` file in the directory becomes a subtest.

## Built-in Commands

### General

| Command | Description |
|---------|-------------|
| `cd <dir>` | Change directory |
| `env [key=value]` | Set or print environment variables |
| `exec <cmd> [args...]` | Execute external command |
| `exists <file>` | Assert file exists |
| `grep <pattern> <file>` | Assert file contains pattern |
| `mkdir <dir>...` | Create directories |
| `cp <src> <dst>` | Copy file |
| `rm <file>...` | Remove files/directories |
| `skip [message]` | Skip the test |
| `stop` | Stop test execution |
| `wait [name...]` | Wait for background commands |

### Output Assertions

| Command | Description |
|---------|-------------|
| `stdout <pattern>` | Assert last command's stdout contains pattern |
| `stderr <pattern>` | Assert last command's stderr contains pattern |

### HTTP

| Command | Description |
|---------|-------------|
| `http METHOD URL [-body FILE] [-header "K: V"]...` | Perform HTTP request |
| `httpstatus CODE` | Assert last HTTP response status code |
| `httpheader NAME VALUE` | Assert last HTTP response header contains value |

The `http` command captures the response body in stdout, so you can chain `stdout` assertions:

```bash
http GET $SERVER/api/info
stdout healthy
httpstatus 200
httpheader Content-Type application/json

http POST $SERVER/api/echo -body payload.json -header "Content-Type: application/json"
stdout result

! http GET $SERVER/missing
httpstatus 404
stdout "not found"
```

### Repeat / Stress Testing

```bash
repeat [-all] COUNT exec <cmd> [args...]
repeat [-all] COUNT http METHOD URL [flags...]
```

Runs a command COUNT times. Without `-all`, stops at first failure. With `-all`, runs all iterations and reports stats to stderr:

```bash
repeat 100 http GET $SERVER/health
stderr "100/100 passed"

! repeat -all 20 http GET $SERVER/flaky
stderr "16/20 passed"
stderr "4/20 failed"
stderr "first at iteration 5"
```

## Negation

Prefix any command with `!` to expect failure:

```bash
! exec false
! http GET $SERVER/missing
! httpstatus 200
! httpheader X-Missing value
```

## Conditional Execution

```bash
[!windows] mkdir unix-only-dir
[short] skip "skipping in short mode"
[!short] exec long-running-command
```

Built-in conditions: `short`, `windows`, `darwin`, `linux`. Negate with `!`.

## Embedded Files

Scripts can embed files using [txtar](https://pkg.go.dev/golang.org/x/tools/txtar) format:

```bash
exec cat config.json
stdout "hello"

-- config.json --
{"message":"hello"}
```

## Background Execution

```bash
exec slow-server &srv
exec curl http://localhost:8080
wait srv
```

## HTTP Testing with Servers

Use `Params.Setup` to inject a test server URL:

```go
func TestAPI(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(myHandler))
    defer srv.Close()

    tsar.Run(t, tsar.Params{
        Dir: "testdata/api",
        Setup: func(env *tsar.Env) error {
            env.Setenv("SERVER", srv.URL)
            return nil
        },
    })
}
```

Then in `testdata/api/health.tsar`:

```bash
http GET $SERVER/health
stdout ok
httpstatus 200
```

## Custom Commands

```go
tsar.Run(t, tsar.Params{
    Dir: "testdata",
    Commands: map[string]func(*tsar.TestScript, bool, []string){
        "mycommand": func(ts *tsar.TestScript, neg bool, args []string) {
            ts.Logf("args: %v", args[1:])
        },
    },
})
```

## Command-line Tool

```bash
tsar testdata/              # Run all .tsar files in directory
tsar testdata/example.tsar  # Run specific file
tsar -v testdata/           # Verbose output
tsar --test-work testdata/  # Preserve work directories
```

| Flag | Description |
|------|-------------|
| `-v, --verbose` | Verbose output |
| `-s, --short` | Short mode |
| `--test-work` | Preserve work directories |
| `-w, --workdir-root` | Custom work directory root |
| `-c, --continue-on-error` | Continue after errors |
| `-e, --require-explicit-exec` | Require explicit `exec` |
| `-u, --require-unique-names` | Require unique test names |

Environment variables with `TSAR_` prefix are also supported (e.g., `TSAR_VERBOSE=true`).

## Attribution

Inspired by and adapted from the [testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript) package by Roger Peppe.

## License

See the original testscript package for license details.
