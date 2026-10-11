// Package analyze aggregates test results across runs and input files and
// detects failures, flaky tests, slow tests, and recurring failure
// signatures.
package analyze

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

// Options controls the analysis.
type Options struct {
	// SlowThreshold marks tests whose longest run took at least this long.
	// Zero disables slow-test detection.
	SlowThreshold time.Duration
	// TopSlow limits how many slow tests are listed (0 = all).
	TopSlow int
	// Signatures are tried in order; the first match classifies a failure.
	Signatures []Signature
	// Ignore holds glob patterns matched against a test's full name and its
	// bare name; '*' matches any run of characters (including '/' and '.')
	// and '?' any single character. Matching tests are excluded entirely.
	Ignore []string
}

// Test is the aggregate of every run of one test.
type Test struct {
	Suite, Name string
	Runs        int
	Passes      int
	Fails       int
	Skips       int
	Final       result.Status // status of the last run
	MaxDuration time.Duration
	LastFailure string // message of the most recent failed run
	Signature   string // signature of LastFailure, if any
	Sources     []string
}

// FullName identifies the test.
func (t *Test) FullName() string {
	return result.TestResult{Suite: t.Suite, Name: t.Name}.FullName()
}

// Flaky reports whether the test both passed and failed.
func (t *Test) Flaky() bool { return t.Passes > 0 && t.Fails > 0 }

// SignatureHit counts failed runs that matched one signature.
type SignatureHit struct {
	Name  string
	Hint  string
	Count int      // failed runs matching
	Tests []string // distinct tests, in first-seen order
}

// Report is the outcome of an analysis.
type Report struct {
	Runs                    int // total executions, including retries
	Tests                   int // distinct tests
	Passed, Failed, Skipped int // by final status
	Ignored                 int // distinct tests excluded by Options.Ignore
	TotalDuration           time.Duration

	Failures []*Test // final status fail, in first-seen order
	Flaky    []*Test // passed and failed; highest failure rate first
	Slow     []*Test // longest first, truncated to TopSlow
	SlowAll  int     // number of slow tests before truncation

	SlowThreshold time.Duration
	Signatures    []SignatureHit // most frequent first
	Unclassified  int            // failed runs matching no signature
}

// Analyze aggregates results. Results are expected in chronological order
// (input files in the order given, runs within a file in report order).
func Analyze(results []result.TestResult, opts Options) *Report {
	ignore := make([]*regexp.Regexp, len(opts.Ignore))
	for i, p := range opts.Ignore {
		ignore[i] = globRegexp(p)
	}

	rep := &Report{SlowThreshold: opts.SlowThreshold}
	byName := map[string]*Test{}
	var order []*Test
	ignored := map[string]bool{}
	hits := map[string]*SignatureHit{}
	hitTests := map[string]map[string]bool{}
	var hitOrder []string

	for _, r := range results {
		name := r.FullName()
		if matchAny(ignore, name, r.Name) {
			ignored[name] = true
			continue
		}
		t := byName[name]
		if t == nil {
			t = &Test{Suite: r.Suite, Name: r.Name}
			byName[name] = t
			order = append(order, t)
		}
		t.Runs++
		t.Final = r.Status
		if r.Duration > t.MaxDuration {
			t.MaxDuration = r.Duration
		}
		if r.Source != "" && (len(t.Sources) == 0 || t.Sources[len(t.Sources)-1] != r.Source) {
			t.Sources = append(t.Sources, r.Source)
		}
		rep.Runs++
		rep.TotalDuration += r.Duration

		switch r.Status {
		case result.Pass:
			t.Passes++
		case result.Skip:
			t.Skips++
		case result.Fail:
			t.Fails++
			t.LastFailure = r.Message
			t.Signature = ""
			sig, ok := classify(opts.Signatures, r.Message)
			if !ok {
				rep.Unclassified++
				break
			}
			t.Signature = sig.Name
			h := hits[sig.Name]
			if h == nil {
				h = &SignatureHit{Name: sig.Name, Hint: sig.Hint}
				hits[sig.Name] = h
				hitTests[sig.Name] = map[string]bool{}
				hitOrder = append(hitOrder, sig.Name)
			}
			h.Count++
			if !hitTests[sig.Name][name] {
				hitTests[sig.Name][name] = true
				h.Tests = append(h.Tests, name)
			}
		}
	}

	rep.Tests = len(order)
	rep.Ignored = len(ignored)
	var slow []*Test
	for _, t := range order {
		switch t.Final {
		case result.Pass:
			rep.Passed++
		case result.Fail:
			rep.Failed++
			rep.Failures = append(rep.Failures, t)
		case result.Skip:
			rep.Skipped++
		}
		if t.Flaky() {
			rep.Flaky = append(rep.Flaky, t)
		}
		if opts.SlowThreshold > 0 && t.MaxDuration >= opts.SlowThreshold {
			slow = append(slow, t)
		}
	}

	sort.SliceStable(rep.Flaky, func(i, j int) bool {
		a, b := rep.Flaky[i], rep.Flaky[j]
		// compare failure rates a.Fails/a.Runs > b.Fails/b.Runs without floats
		return a.Fails*b.Runs > b.Fails*a.Runs
	})
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].MaxDuration > slow[j].MaxDuration })
	rep.SlowAll = len(slow)
	if opts.TopSlow > 0 && len(slow) > opts.TopSlow {
		slow = slow[:opts.TopSlow]
	}
	rep.Slow = slow

	for _, n := range hitOrder {
		rep.Signatures = append(rep.Signatures, *hits[n])
	}
	sort.SliceStable(rep.Signatures, func(i, j int) bool { return rep.Signatures[i].Count > rep.Signatures[j].Count })
	return rep
}

func classify(sigs []Signature, msg string) (Signature, bool) {
	if msg == "" {
		return Signature{}, false
	}
	for _, s := range sigs {
		if s.Match(msg) {
			return s, true
		}
	}
	return Signature{}, false
}

func globRegexp(glob string) *regexp.Regexp {
	q := regexp.QuoteMeta(glob)
	q = strings.ReplaceAll(q, `\*`, ".*")
	q = strings.ReplaceAll(q, `\?`, ".")
	return regexp.MustCompile("^" + q + "$")
}

func matchAny(patterns []*regexp.Regexp, names ...string) bool {
	for _, p := range patterns {
		for _, n := range names {
			if p.MatchString(n) {
				return true
			}
		}
	}
	return false
}

// Check names a condition that can fail the build.
type Check string

const (
	CheckFailures Check = "failures"
	CheckFlaky    Check = "flaky"
	CheckSlow     Check = "slow"
)

// ParseChecks parses a comma-separated list such as "failures,flaky".
// "none" (or an empty string) disables all checks.
func ParseChecks(s string) ([]Check, error) {
	var out []Check
	for _, part := range strings.Split(s, ",") {
		switch c := Check(strings.ToLower(strings.TrimSpace(part))); c {
		case "", "none":
		case CheckFailures, CheckFlaky, CheckSlow:
			out = append(out, c)
		case "all":
			out = append(out, CheckFailures, CheckFlaky, CheckSlow)
		default:
			return nil, fmt.Errorf("unknown check %q (want failures, flaky, slow, all or none)", part)
		}
	}
	return out, nil
}

// Violations returns the checks that this report fails.
func (r *Report) Violations(checks []Check) []Check {
	var out []Check
	seen := map[Check]bool{}
	for _, c := range checks {
		if seen[c] {
			continue
		}
		seen[c] = true
		if (c == CheckFailures && len(r.Failures) > 0) ||
			(c == CheckFlaky && len(r.Flaky) > 0) ||
			(c == CheckSlow && r.SlowAll > 0) {
			out = append(out, c)
		}
	}
	return out
}
