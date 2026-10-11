# ci-feedback

[![ci](https://github.com/aniketnandi/ci-feedback/actions/workflows/ci.yml/badge.svg)](https://github.com/aniketnandi/ci-feedback/actions/workflows/ci.yml)

A small Go CLI that reads test reports and tells you, in a few lines, what
actually needs your attention: **what failed and why, which tests are flaky,
and which are slow**. It exits non-zero based on rules you choose, so it can
be the step that passes or fails a CI job.

```
$ ci-feedback examples/gotest.json
ci-feedback: 7 tests, 4 passed, 2 failed, 1 skipped (21 runs) across 1 file, 6.9s total

FAILURES (2)
  FAIL  example.com/shop/cart.TestParsePrice/negative  [assertion]
        cart_test.go:25: ParsePrice("-1") = 1, expected error but got nil
  FAIL  example.com/shop/cart.TestInventoryClient  [network]
        cart_test.go:30: dial tcp 127.0.0.1:5432: connect: connection refused

FLAKY (1)
  FLAKY example.com/shop/cart.TestCacheWarmup  failed 1/3 runs (33%)  [timeout]

SLOW (1 >= 2s)
      2.3s  example.com/shop/cart.TestCheckoutSlow

FAILURE SIGNATURES
  assertion      3 failures in 1 test - an assertion failed; this is usually a genuine regression
  network        3 failures in 1 test - a network dependency was unavailable; is the service container up and healthy?
  timeout        1 failure in 1 test - something took too long; look for slow dependencies, deadlocks, or tight timeouts

RESULT: FAIL (failures)
$ echo $?
1
```

## Features

- **Three input formats**, detected automatically:
  - **JUnit XML**: nested `<testsuites>`, plus the Maven Surefire / Gradle
    retry elements (`flakyFailure`, `rerunFailure`, `rerunError`, ...).
  - **`go test -json`** (test2json): subtests, `-count=N`, build failures,
    and tests killed by `-timeout` or a panic.
  - **Plain-text logs** (best effort): `go test -v`, `pytest -v` (including
    `RERUN` from pytest-rerunfailures and the tracebacks in the FAILURES
    section), and generic `PASS: name (1.2s)` / `[FAIL] name` lines.
- **Flaky detection**: a test that both passed and failed is flaky. That
  covers retries inside one report (Surefire reruns, pytest reruns,
  `go test -count=N`) and the same suite across several files, e.g.
  `ci-feedback run-1.xml run-2.xml run-3.xml`. Flaky tests are ranked by
  failure rate.
- **Slow tests**: tests whose longest run hit a threshold, longest first.
- **Failure signatures**: failures are grouped by regex rules
  (`timeout`, `network`, `nil-pointer`, `data-race`, `out-of-memory`,
  `port-in-use`, `panic`, `assertion`), each with a hint. You can add your
  own rules in a config file.
- **Rules that decide the exit code**: `-fail-on failures,flaky,slow`.
- **Ignore / quarantine** known-bad tests with globs.
- **Output formats**: `text`, `json` (machine-readable), or `github`
  (GitHub Actions `::error` / `::warning` / `::notice` annotations, followed by the text summary).
- **Concurrent parsing**: files are parsed in parallel (`-j`) and the results
  stay in the order you passed the files.
- Uses only the Go standard library.

## Install

```sh
go install github.com/aniketnandi/ci-feedback@latest
```

Or build from source:

```sh
git clone https://github.com/aniketnandi/ci-feedback
cd ci-feedback
go build -o ci-feedback .
```

Requires Go 1.22+.

## Usage

```
ci-feedback [flags] FILE...
```

`FILE` can be any supported format; use `-` for stdin.

| Flag | Default | Description |
|---|---|---|
| `-format` | `auto` | Input format: `auto`, `junit`, `gotest`, `log` |
| `-output` | `text` | `text`, `json`, or `github` |
| `-fail-on` | `failures` | Comma-separated checks that fail the build: `failures`, `flaky`, `slow`, `all`, `none` |
| `-slow` | `2s` | Slow-test threshold (`0` disables it) |
| `-top` | `10` | Show at most N slow tests (`0` = all) |
| `-ignore` | | Glob of tests to skip, matched against the full name (`pkg.TestName`) and the bare name. `*` matches anything. Repeatable |
| `-config` | `.ci-feedback.json` if present | Rules file (see below) |
| `-j` | number of CPUs | Number of files to parse in parallel |
| `-version` | | Print the version (the release tag when installed with `go install`) |

### Exit codes

| Code | Meaning |
|---|---|
| `0` | All enabled checks passed |
| `1` | At least one check in `-fail-on` failed |
| `2` | Usage error, or an input that can't be read or parsed (the message names the file) |

Exit code 2 is kept separate from 1 so CI can tell "tests are broken" apart
from "the report is missing or malformed". An input with **no tests in it**
also counts as an error, because that usually means the wrong path was given.

### Examples

```sh
# Go: pipe straight in
go test -json ./... | ci-feedback -

# Detect flakes by running the suite several times
go test -json -count=5 ./... > report.json
ci-feedback -fail-on failures,flaky report.json

# JUnit from any toolchain, several CI runs at once
ci-feedback -slow 1s build/test-results/test/*.xml

# pytest
pytest -v | tee pytest.log; ci-feedback pytest.log

# Report on everything but never fail the step
ci-feedback -fail-on none -output json report.xml > summary.json
```

Try it on the bundled examples:

```sh
go run . examples/gotest.json
go run . examples/junit-surefire.xml
go run . examples/pytest.log
go run . examples/gotest-verbose.log
go run . -config examples/ci-feedback.json examples/gotest.json
```

## Configuration

Settings come from three places, and later ones win:
built-in defaults, then the rules file, then flags you pass explicitly.
Unknown keys are rejected, so a typo doesn't get silently ignored.

```json
{
  "slow_threshold": "1.5s",
  "top_slow": 5,
  "fail_on": ["failures", "flaky"],
  "ignore": ["*LegacyDiscount", "com.acme.quarantine.*"],
  "signatures": [
    {
      "name": "db-unavailable",
      "pattern": "127\\.0\\.0\\.1:5432|could not connect to server",
      "hint": "Postgres service container is not ready; add a health check to the job"
    }
  ],
  "disable_builtin_signatures": false
}
```

Signatures are tried in order, your rules first and then the built-in ones.
The first match classifies a failure. Patterns use
[Go regexp syntax](https://pkg.go.dev/regexp/syntax), so use `(?i)` to match
case-insensitively.

## GitHub Actions

```yaml
- name: test
  run: go test -json ./... > test-report.json || true   # let ci-feedback decide

- name: ci-feedback
  run: go run github.com/aniketnandi/ci-feedback@latest -output github -fail-on failures,flaky test-report.json
```

With `-output github`, failures show up as error annotations, flaky tests as
warnings, and slow tests as notices on the workflow run. This repository's
own workflow ([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs
ci-feedback on its own test output.

## How it works

```
files ──► parse (concurrent, auto-detect) ──► []TestResult ──► analyze ──► report ──► exit code
```

- `internal/parse` turns each format into a flat list of `TestResult`s, one
  per attempt. When a parent test failed only because one of its subtests
  failed, the parent's failure is dropped, so one broken subtest isn't
  counted twice.
- `internal/analyze` groups results by full test name, in input order. A
  test's **final** status (its last run) decides whether it counts as passed
  or failed. A test with both passes and failures is flaky. The slowest run
  is what gets compared to the slow threshold.
- `internal/report` renders the text, JSON, and GitHub output.
- `internal/config` loads and validates the rules file.

## Development

```sh
go test -race ./...
go vet ./...
```

`examples/gotest.json` and `examples/gotest-verbose.log` are real output
from the deliberately broken package in `examples/shop`. It is a separate
module, so it doesn't run as part of `go test ./...`. To regenerate:

```sh
cd examples/shop
go test -json -count=3 ./... > ../gotest.json
go test -v ./... > ../gotest-verbose.log 2>&1
```

## License

MIT
