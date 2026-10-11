package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/analyze"
	"github.com/aniketnandi/ci-feedback/internal/result"
)

func sample() Summary {
	results := []result.TestResult{
		{Suite: "pkg", Name: "TestOK", Status: result.Pass, Duration: 3 * time.Second},
		{Suite: "pkg", Name: "TestBad", Status: result.Fail, Message: "dial tcp: connection refused\nline2\nline3\nline4"},
		{Suite: "pkg", Name: "TestFlaky", Status: result.Fail, Message: "boom"},
		{Suite: "pkg", Name: "TestFlaky", Status: result.Pass},
	}
	rep := analyze.Analyze(results, analyze.Options{SlowThreshold: time.Second, Signatures: analyze.BuiltinSignatures()})
	checks := []analyze.Check{analyze.CheckFailures, analyze.CheckFlaky}
	return Summary{Report: rep, Files: []string{"a.xml"}, Checks: checks, Violations: rep.Violations(checks)}
}

func TestText(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, Text, sample()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"ci-feedback: 3 tests, 2 passed, 1 failed, 0 skipped (4 runs) across 1 file",
		"FAIL  pkg.TestBad  [network]",
		"dial tcp: connection refused",
		"FLAKY pkg.TestFlaky  failed 1/2 runs (50%)",
		"SLOW (1 >= 1s)",
		"3s  pkg.TestOK",
		"network        1 failure in 1 test",
		"unclassified   1 failure",
		"RESULT: FAIL (failures, flaky)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line4") {
		t.Errorf("failure excerpt not truncated:\n%s", out)
	}
}

func TestTextPass(t *testing.T) {
	rep := analyze.Analyze([]result.TestResult{{Name: "T", Status: result.Pass}}, analyze.Options{})
	var buf bytes.Buffer
	Write(&buf, Text, Summary{Report: rep, Files: []string{"x"}, Checks: []analyze.Check{analyze.CheckFailures}})
	if !strings.Contains(buf.String(), "RESULT: PASS (checked: failures)") {
		t.Errorf("got:\n%s", buf.String())
	}
}

func TestJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, JSON, sample()); err != nil {
		t.Fatal(err)
	}
	var got jsonReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if got.Passed || len(got.Violations) != 2 || got.Tests != 3 || got.Runs != 4 ||
		len(got.Failures) != 1 || got.Failures[0].Signature != "network" ||
		len(got.Flaky) != 1 || got.Flaky[0].Fails != 1 || len(got.Slow) != 1 || got.Slow[0].MaxSeconds != 3 {
		t.Errorf("unexpected JSON report: %+v", got)
	}
	// Empty lists must encode as [] so consumers can index without null checks.
	var empty bytes.Buffer
	rep := analyze.Analyze([]result.TestResult{{Name: "T", Status: result.Pass}}, analyze.Options{})
	Write(&empty, JSON, Summary{Report: rep})
	if !strings.Contains(empty.String(), `"failures": []`) || strings.Contains(empty.String(), "null") {
		t.Errorf("empty lists should be []:\n%s", empty.String())
	}
}

func TestGitHubAnnotations(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, GitHub, sample()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"::error title=Test failed%3A pkg.TestBad [network]::dial tcp: connection refused%0Aline2%0Aline3%0Aline4\n",
		"::warning title=Flaky test%3A pkg.TestFlaky::failed 1 of 2 runs\n",
		"::notice title=Slow test%3A pkg.TestOK::took 3s (threshold 1s)\n",
		"RESULT: FAIL", // text summary follows the annotations
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if got := escapeData("100% done\r\n"); got != "100%25 done%0D%0A" {
		t.Errorf("escapeData = %q", got)
	}
}

func TestParseFormat(t *testing.T) {
	if f, err := ParseFormat("JSON"); err != nil || f != JSON {
		t.Errorf("ParseFormat(JSON) = %v, %v", f, err)
	}
	if _, err := ParseFormat("html"); err == nil {
		t.Error("want error")
	}
}

func TestExcerpt(t *testing.T) {
	got := excerpt("\n  a\n\n  b  \nc\nd\n", 3)
	if strings.Join(got, "|") != "a|b|..." {
		t.Errorf("excerpt = %q", got)
	}
	if got := excerpt(strings.Repeat("x", 500), 1); len(got[0]) != 203 {
		t.Errorf("long line not capped: %d", len(got[0]))
	}
}
