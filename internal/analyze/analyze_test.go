package analyze

import (
	"reflect"
	"testing"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

func r(name string, st result.Status, d time.Duration, msg string) result.TestResult {
	return result.TestResult{Suite: "pkg", Name: name, Status: st, Duration: d, Message: msg}
}

func names(ts []*Test) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

func TestAnalyzeAcrossRuns(t *testing.T) {
	// Two CI runs of the same suite concatenated in chronological order.
	results := []result.TestResult{
		r("TestStable", result.Pass, 10*time.Millisecond, ""),
		r("TestFlakyA", result.Fail, 0, "context deadline exceeded"),
		r("TestFlakyB", result.Pass, 0, ""),
		r("TestBroken", result.Fail, 0, "expected 2, got 3"),
		r("TestSkip", result.Skip, 0, ""),

		r("TestStable", result.Pass, 3*time.Second, ""),
		r("TestFlakyA", result.Pass, 0, ""),
		r("TestFlakyB", result.Fail, 0, "dial tcp: connection refused"),
		r("TestBroken", result.Fail, 0, "expected 2, got 3"),
		r("TestSkip", result.Skip, 0, ""),
	}
	rep := Analyze(results, Options{SlowThreshold: time.Second, Signatures: BuiltinSignatures()})

	if rep.Runs != 10 || rep.Tests != 5 {
		t.Errorf("runs/tests = %d/%d, want 10/5", rep.Runs, rep.Tests)
	}
	// Final status decides pass/fail: FlakyB failed last, FlakyA recovered.
	if rep.Passed != 2 || rep.Failed != 2 || rep.Skipped != 1 {
		t.Errorf("passed/failed/skipped = %d/%d/%d, want 2/2/1", rep.Passed, rep.Failed, rep.Skipped)
	}
	if got := names(rep.Failures); !reflect.DeepEqual(got, []string{"TestFlakyB", "TestBroken"}) {
		t.Errorf("failures = %v", got)
	}
	if got := names(rep.Flaky); !reflect.DeepEqual(got, []string{"TestFlakyA", "TestFlakyB"}) {
		t.Errorf("flaky = %v", got)
	}
	if got := names(rep.Slow); !reflect.DeepEqual(got, []string{"TestStable"}) || rep.Slow[0].MaxDuration != 3*time.Second {
		t.Errorf("slow = %v", got)
	}
	if rep.TotalDuration != 3010*time.Millisecond {
		t.Errorf("total = %v", rep.TotalDuration)
	}

	sigs := map[string]int{}
	for _, h := range rep.Signatures {
		sigs[h.Name] = h.Count
	}
	if !reflect.DeepEqual(sigs, map[string]int{"assertion": 2, "timeout": 1, "network": 1}) {
		t.Errorf("signatures = %v", sigs)
	}
	if rep.Signatures[0].Name != "assertion" || len(rep.Signatures[0].Tests) != 1 {
		t.Errorf("most frequent signature should be assertion with 1 test: %+v", rep.Signatures[0])
	}
	if rep.Failures[0].Signature != "network" {
		t.Errorf("TestFlakyB signature = %q", rep.Failures[0].Signature)
	}
}

func TestFlakyOrderedByFailureRate(t *testing.T) {
	var results []result.TestResult
	add := func(name string, fails, passes int) {
		for i := 0; i < fails; i++ {
			results = append(results, r(name, result.Fail, 0, ""))
		}
		for i := 0; i < passes; i++ {
			results = append(results, r(name, result.Pass, 0, ""))
		}
	}
	add("Rare", 1, 9)
	add("Often", 3, 1)
	add("Half", 2, 2)
	rep := Analyze(results, Options{})
	if got := names(rep.Flaky); !reflect.DeepEqual(got, []string{"Often", "Half", "Rare"}) {
		t.Errorf("flaky order = %v", got)
	}
	if rep.Unclassified != 6 {
		t.Errorf("unclassified = %d, want 6", rep.Unclassified)
	}
}

func TestSlowTopN(t *testing.T) {
	var results []result.TestResult
	for i := 1; i <= 5; i++ {
		results = append(results, r(string(rune('A'+i-1)), result.Pass, time.Duration(i)*time.Second, ""))
	}
	rep := Analyze(results, Options{SlowThreshold: 2 * time.Second, TopSlow: 2})
	if got := names(rep.Slow); !reflect.DeepEqual(got, []string{"E", "D"}) || rep.SlowAll != 4 {
		t.Errorf("slow = %v (all %d)", got, rep.SlowAll)
	}
	if rep := Analyze(results, Options{}); len(rep.Slow) != 0 {
		t.Errorf("zero threshold should disable slow detection")
	}
}

func TestIgnore(t *testing.T) {
	results := []result.TestResult{
		r("TestKnownFlaky", result.Fail, 0, ""),
		r("TestKnownFlaky", result.Pass, 0, ""),
		{Suite: "github.com/acme/api/v2", Name: "TestQuarantined", Status: result.Fail},
		r("TestReal", result.Fail, 0, ""),
	}
	rep := Analyze(results, Options{Ignore: []string{"TestKnown*", "github.com/acme/*.TestQuarantined"}})
	if rep.Ignored != 2 || rep.Tests != 1 || names(rep.Failures)[0] != "TestReal" || len(rep.Flaky) != 0 {
		t.Errorf("ignored=%d tests=%d failures=%v flaky=%v", rep.Ignored, rep.Tests, names(rep.Failures), names(rep.Flaky))
	}
}

func TestSignaturesFirstMatchWins(t *testing.T) {
	custom, err := NewSignature("db", `5432`, "check postgres")
	if err != nil {
		t.Fatal(err)
	}
	sigs := append([]Signature{custom}, BuiltinSignatures()...)
	rep := Analyze([]result.TestResult{r("T", result.Fail, 0, "dial tcp 127.0.0.1:5432: connection refused")}, Options{Signatures: sigs})
	if rep.Failures[0].Signature != "db" || rep.Signatures[0].Hint != "check postgres" {
		t.Errorf("got %+v", rep.Signatures)
	}
}

func TestBuiltinSignatures(t *testing.T) {
	cases := map[string]string{
		"WARNING: DATA RACE\nRead at 0x00c":                                       "data-race",
		"java.lang.OutOfMemoryError: Java heap space":                             "out-of-memory",
		"panic: runtime error: invalid memory address or nil pointer dereference": "nil-pointer",
		"panic: test timed out after 10m0s":                                       "timeout",
		"dial tcp: lookup db: no such host":                                       "network",
		"read tcp 10.0.0.1:443: i/o timeout":                                      "network",
		"listen tcp :8080: bind: address already in use":                          "port-in-use",
		"panic: something bad":                                                    "panic",
		"Traceback (most recent call last):\n  File x":                            "panic",
		"expected: <90.00> but was: <100.00>":                                     "assertion",
		"E       assert [] == ['color']":                                          "assertion",
	}
	sigs := BuiltinSignatures()
	for msg, want := range cases {
		got, ok := classify(sigs, msg)
		if !ok || got.Name != want {
			t.Errorf("classify(%q) = %q, want %q", msg, got.Name, want)
		}
	}
	if _, ok := classify(sigs, "the widget is blue"); ok {
		t.Error("unrelated message classified")
	}
}

func TestNewSignatureErrors(t *testing.T) {
	if _, err := NewSignature("", "x", ""); err == nil {
		t.Error("want error for missing name")
	}
	if _, err := NewSignature("bad", "(", ""); err == nil {
		t.Error("want error for invalid regexp")
	}
}

func TestChecks(t *testing.T) {
	checks, err := ParseChecks("failures, FLAKY")
	if err != nil || !reflect.DeepEqual(checks, []Check{CheckFailures, CheckFlaky}) {
		t.Fatalf("ParseChecks = %v, %v", checks, err)
	}
	if c, _ := ParseChecks("all"); len(c) != 3 {
		t.Errorf("all = %v", c)
	}
	if c, _ := ParseChecks("none"); len(c) != 0 {
		t.Errorf("none = %v", c)
	}
	if _, err := ParseChecks("failures,typo"); err == nil {
		t.Error("want error for unknown check")
	}

	rep := &Report{Flaky: []*Test{{Name: "x"}}, SlowAll: 1}
	if got := rep.Violations([]Check{CheckFailures, CheckFlaky, CheckFlaky, CheckSlow}); !reflect.DeepEqual(got, []Check{CheckFlaky, CheckSlow}) {
		t.Errorf("violations = %v", got)
	}
}
