// Package report renders an analysis as text, JSON, or GitHub Actions
// workflow annotations.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/analyze"
)

// Summary carries everything a renderer needs.
type Summary struct {
	Report     *analyze.Report
	Files      []string
	Checks     []analyze.Check
	Violations []analyze.Check
}

// Passed reports whether no enabled check was violated.
func (s Summary) Passed() bool { return len(s.Violations) == 0 }

// Format names an output format.
type Format string

const (
	Text   Format = "text"
	JSON   Format = "json"
	GitHub Format = "github"
)

// ParseFormat validates an output format name.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(s)); f {
	case Text, JSON, GitHub:
		return f, nil
	}
	return "", fmt.Errorf("unknown output %q (want text, json or github)", s)
}

// Write renders s in format f.
func Write(w io.Writer, f Format, s Summary) error {
	switch f {
	case JSON:
		return writeJSON(w, s)
	case GitHub:
		if err := writeGitHub(w, s); err != nil {
			return err
		}
		return writeText(w, s)
	default:
		return writeText(w, s)
	}
}

// ---- text ----

func writeText(w io.Writer, s Summary) error {
	r := s.Report
	ew := &errWriter{w: w}
	ew.printf("ci-feedback: %d tests, %d passed, %d failed, %d skipped", r.Tests, r.Passed, r.Failed, r.Skipped)
	if r.Runs != r.Tests {
		ew.printf(" (%d runs)", r.Runs)
	}
	ew.printf(" across %d %s, %s total\n", len(s.Files), plural(len(s.Files), "file", "files"), fmtDur(r.TotalDuration))
	if r.Ignored > 0 {
		ew.printf("ignored %d %s by rule\n", r.Ignored, plural(r.Ignored, "test", "tests"))
	}

	if len(r.Failures) > 0 {
		ew.printf("\nFAILURES (%d)\n", len(r.Failures))
		for _, t := range r.Failures {
			ew.printf("  FAIL  %s", t.FullName())
			if t.Signature != "" {
				ew.printf("  [%s]", t.Signature)
			}
			if t.Flaky() {
				ew.printf("  (also passed %d/%d runs)", t.Passes, t.Runs)
			}
			ew.printf("\n")
			for _, line := range excerpt(t.LastFailure, 3) {
				ew.printf("        %s\n", line)
			}
		}
	}

	if len(r.Flaky) > 0 {
		ew.printf("\nFLAKY (%d)\n", len(r.Flaky))
		for _, t := range r.Flaky {
			ew.printf("  FLAKY %s  failed %d/%d runs (%.0f%%)", t.FullName(), t.Fails, t.Runs, 100*float64(t.Fails)/float64(t.Runs))
			if t.Signature != "" {
				ew.printf("  [%s]", t.Signature)
			}
			ew.printf("\n")
		}
	}

	if len(r.Slow) > 0 {
		ew.printf("\nSLOW (%d >= %s", r.SlowAll, fmtDur(r.SlowThreshold))
		if len(r.Slow) < r.SlowAll {
			ew.printf(", showing top %d", len(r.Slow))
		}
		ew.printf(")\n")
		for _, t := range r.Slow {
			ew.printf("  %8s  %s\n", fmtDur(t.MaxDuration), t.FullName())
		}
	}

	if len(r.Signatures) > 0 {
		ew.printf("\nFAILURE SIGNATURES\n")
		for _, h := range r.Signatures {
			ew.printf("  %-14s %d %s in %d %s", h.Name, h.Count, plural(h.Count, "failure", "failures"), len(h.Tests), plural(len(h.Tests), "test", "tests"))
			if h.Hint != "" {
				ew.printf(" - %s", h.Hint)
			}
			ew.printf("\n")
		}
		if r.Unclassified > 0 {
			ew.printf("  %-14s %d %s\n", "unclassified", r.Unclassified, plural(r.Unclassified, "failure", "failures"))
		}
	}

	ew.printf("\n")
	if s.Passed() {
		ew.printf("RESULT: PASS")
		if len(s.Checks) > 0 {
			ew.printf(" (checked: %s)", joinChecks(s.Checks))
		}
		ew.printf("\n")
	} else {
		ew.printf("RESULT: FAIL (%s)\n", joinChecks(s.Violations))
	}
	return ew.err
}

// ---- json ----

type jsonTest struct {
	Name        string   `json:"name"`
	Suite       string   `json:"suite,omitempty"`
	Test        string   `json:"test"`
	Runs        int      `json:"runs"`
	Passes      int      `json:"passes"`
	Fails       int      `json:"fails"`
	Final       string   `json:"final"`
	MaxSeconds  float64  `json:"max_duration_seconds"`
	Signature   string   `json:"signature,omitempty"`
	LastFailure string   `json:"last_failure,omitempty"`
	Sources     []string `json:"sources,omitempty"`
}

type jsonSignature struct {
	Name  string   `json:"name"`
	Hint  string   `json:"hint,omitempty"`
	Count int      `json:"count"`
	Tests []string `json:"tests"`
}

type jsonReport struct {
	Passed        bool            `json:"passed"`
	Violations    []string        `json:"violations"`
	Files         []string        `json:"files"`
	Tests         int             `json:"tests"`
	Runs          int             `json:"runs"`
	PassedTests   int             `json:"passed_tests"`
	FailedTests   int             `json:"failed_tests"`
	SkippedTests  int             `json:"skipped_tests"`
	IgnoredTests  int             `json:"ignored_tests"`
	TotalSeconds  float64         `json:"total_duration_seconds"`
	SlowThreshold float64         `json:"slow_threshold_seconds"`
	SlowTotal     int             `json:"slow_total"`
	Failures      []jsonTest      `json:"failures"`
	Flaky         []jsonTest      `json:"flaky"`
	Slow          []jsonTest      `json:"slow"`
	Signatures    []jsonSignature `json:"signatures"`
	Unclassified  int             `json:"unclassified_failures"`
}

func writeJSON(w io.Writer, s Summary) error {
	r := s.Report
	out := jsonReport{
		Passed:        s.Passed(),
		Violations:    checkStrings(s.Violations),
		Files:         nonNil(s.Files),
		Tests:         r.Tests,
		Runs:          r.Runs,
		PassedTests:   r.Passed,
		FailedTests:   r.Failed,
		SkippedTests:  r.Skipped,
		IgnoredTests:  r.Ignored,
		TotalSeconds:  r.TotalDuration.Seconds(),
		SlowThreshold: r.SlowThreshold.Seconds(),
		SlowTotal:     r.SlowAll,
		Failures:      jsonTests(r.Failures),
		Flaky:         jsonTests(r.Flaky),
		Slow:          jsonTests(r.Slow),
		Signatures:    []jsonSignature{},
		Unclassified:  r.Unclassified,
	}
	for _, h := range r.Signatures {
		out.Signatures = append(out.Signatures, jsonSignature{h.Name, h.Hint, h.Count, h.Tests})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func jsonTests(ts []*analyze.Test) []jsonTest {
	out := []jsonTest{}
	for _, t := range ts {
		out = append(out, jsonTest{
			Name: t.FullName(), Suite: t.Suite, Test: t.Name,
			Runs: t.Runs, Passes: t.Passes, Fails: t.Fails, Final: t.Final.String(),
			MaxSeconds: t.MaxDuration.Seconds(), Signature: t.Signature,
			LastFailure: t.LastFailure, Sources: t.Sources,
		})
	}
	return out
}

// ---- GitHub Actions annotations ----

func writeGitHub(w io.Writer, s Summary) error {
	r := s.Report
	ew := &errWriter{w: w}
	for _, t := range r.Failures {
		title := "Test failed: " + t.FullName()
		if t.Signature != "" {
			title += " [" + t.Signature + "]"
		}
		ew.printf("::error title=%s::%s\n", escapeProperty(title), escapeData(strings.Join(excerpt(t.LastFailure, 10), "\n")))
	}
	for _, t := range r.Flaky {
		ew.printf("::warning title=%s::%s\n", escapeProperty("Flaky test: "+t.FullName()),
			escapeData(fmt.Sprintf("failed %d of %d runs", t.Fails, t.Runs)))
	}
	for _, t := range r.Slow {
		ew.printf("::notice title=%s::%s\n", escapeProperty("Slow test: "+t.FullName()),
			escapeData(fmt.Sprintf("took %s (threshold %s)", fmtDur(t.MaxDuration), fmtDur(r.SlowThreshold))))
	}
	return ew.err
}

// See https://github.com/actions/toolkit/blob/main/packages/core/src/command.ts
func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// ---- helpers ----

// excerpt returns up to n non-empty lines of msg, each capped in length.
func excerpt(msg string, n int) []string {
	const maxLen = 200
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > maxLen {
			line = line[:maxLen] + "..."
		}
		if len(out) == n {
			out[n-1] = "..."
			break
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d < time.Millisecond:
		return d.String()
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	default:
		return d.Round(10 * time.Millisecond).String()
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func checkStrings(cs []analyze.Check) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, string(c))
	}
	return out
}

func joinChecks(cs []analyze.Check) string { return strings.Join(checkStrings(cs), ", ") }

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}
