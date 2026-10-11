package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"failures fail by default", []string{"examples/gotest.json"}, exitCheck},
		{"fail-on none", []string{"-fail-on", "none", "examples/gotest.json"}, exitOK},
		{"flaky only", []string{"-fail-on", "flaky", "-ignore", "*TestCacheWarmup", "examples/gotest.json"}, exitOK},
		{"slow check", []string{"-fail-on", "slow", "-slow", "10s", "examples/junit-surefire.xml"}, exitOK},
		{"slow check trips", []string{"-fail-on", "slow", "examples/junit-surefire.xml"}, exitCheck},
		{"missing file", []string{"does-not-exist.xml"}, exitUsage},
		{"no files", []string{}, exitUsage},
		{"bad flag", []string{"-nope", "examples/gotest.json"}, exitUsage},
		{"bad format", []string{"-format", "yaml", "examples/gotest.json"}, exitUsage},
		{"bad output", []string{"-output", "html", "examples/gotest.json"}, exitUsage},
		{"bad check", []string{"-fail-on", "flakey", "examples/gotest.json"}, exitUsage},
		{"stdin twice", []string{"-", "-"}, exitUsage},
		{"wrong forced format", []string{"-format", "junit", "examples/gotest.json"}, exitUsage},
		{"missing config", []string{"-config", "nope.json", "examples/gotest.json"}, exitUsage},
		{"help", []string{"-h"}, exitOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, "", tc.args...)
			if code != tc.want {
				t.Errorf("exit = %d, want %d; stderr: %s", code, tc.want, stderr)
			}
		})
	}
}

func TestMultipleFilesFindFlakyAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	run1 := filepath.Join(dir, "run1.log")
	run2 := filepath.Join(dir, "run2.log")
	os.WriteFile(run1, []byte("PASS: login\nFAIL: search\n"), 0o644)
	os.WriteFile(run2, []byte("PASS: login\nPASS: search\n"), 0o644)

	code, out, stderr := runCLI(t, "", "-fail-on", "failures,flaky", run1, run2)
	if code != exitCheck {
		t.Fatalf("exit = %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "FLAKY search  failed 1/2 runs") || !strings.Contains(out, "RESULT: FAIL (flaky)") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestStdinAndJSONOutput(t *testing.T) {
	code, out, stderr := runCLI(t, "=== RUN   TestX\n    x_test.go:9: expected 1, got 2\n--- FAIL: TestX (0.50s)\n", "-output", "json", "-")
	if code != exitCheck {
		t.Fatalf("exit = %d; stderr: %s", code, stderr)
	}
	var rep struct {
		Passed   bool     `json:"passed"`
		Files    []string `json:"files"`
		Failures []struct {
			Name, Signature string
		} `json:"failures"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if rep.Passed || rep.Files[0] != "<stdin>" || rep.Failures[0].Name != "TestX" || rep.Failures[0].Signature != "assertion" {
		t.Errorf("unexpected report: %+v", rep)
	}
}

func TestConfigFileAndFlagPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "rules.json")
	os.WriteFile(cfg, []byte(`{"fail_on": ["flaky"], "slow_threshold": "100s", "ignore": ["*Inventory*", "*negative"]}`), 0o644)

	// Config: only flaky fails the build; TestCacheWarmup is flaky.
	code, out, _ := runCLI(t, "", "-config", cfg, "examples/gotest.json")
	if code != exitCheck || !strings.Contains(out, "RESULT: FAIL (flaky)") || strings.Contains(out, "SLOW") {
		t.Errorf("config not applied (exit %d):\n%s", code, out)
	}
	// The flag overrides the config's fail_on.
	if code, out, _ := runCLI(t, "", "-config", cfg, "-fail-on", "failures", "examples/gotest.json"); code != exitOK {
		t.Errorf("flag should override config (exit %d):\n%s", code, out)
	}
}

func TestErrorMessagesNameTheFile(t *testing.T) {
	_, _, stderr := runCLI(t, "", "-format", "junit", "examples/gotest.json")
	if !strings.Contains(stderr, "examples/gotest.json (junit): no test results found") {
		t.Errorf("stderr = %q", stderr)
	}
}
