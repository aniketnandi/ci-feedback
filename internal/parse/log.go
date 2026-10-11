package parse

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

var (
	// go test -v:  "--- FAIL: TestFoo/bar (0.01s)"
	reGoVerbose = regexp.MustCompile(`^\s*--- (PASS|FAIL|SKIP): (\S+) \((\d+(?:\.\d+)?)s\)`)
	// pytest -v:   "tests/test_api.py::test_login PASSED    [ 50%]" (RERUN from pytest-rerunfailures)
	rePytest = regexp.MustCompile(`^(\S+?)::(\S+)\s+(PASSED|FAILED|SKIPPED|ERROR|RERUN)\b`)
	// generic:     "FAIL: checkout_flow (2.5s)", "[PASS] login 120ms", "ok widget"
	reGeneric = regexp.MustCompile(`^\[?(PASS|PASSED|OK|FAIL|FAILED|ERROR|SKIP|SKIPPED)\]?:? +(\S+)(?: +\(?(\d+(?:\.\d+)?(?:ms|s|m))\)?)?\s*$`)
	// pytest failure details, printed after all results:
	//   "_____ TestCart.test_total _____" section headers and
	//   "FAILED tests/a.py::test_b - AssertionError: ..." summary lines
	rePytestSection = regexp.MustCompile(`^_{3,} (\S+(?: \S+)*?) _{3,}$`)
	rePytestSummary = regexp.MustCompile(`^(?:FAILED|ERROR) (\S+::\S+) - (.+)$`)
	rePytestBanner  = regexp.MustCompile(`^={3,}`)
	// lines that are framing, not test output
	reNoise = regexp.MustCompile(`^\s*=== (RUN|PAUSE|CONT|NAME)\b`)
)

// maxContext bounds how many preceding log lines are kept as a failure's
// message.
const maxContext = 40

// parseLog is a best-effort line-oriented parser. Lines that are not result
// lines are buffered and attached to the next failing result as context.
func parseLog(r io.Reader) ([]result.TestResult, error) {
	var (
		sc      = bufio.NewScanner(r)
		out     []result.TestResult
		context []string
		pytest  bool
		// pytest failure details keyed by test id or bare test name
		details = map[string]*strings.Builder{}
		section *strings.Builder
	)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	emit := func(suite, name string, st result.Status, d time.Duration) {
		res := result.TestResult{Suite: suite, Name: name, Status: st, Duration: d}
		if st == result.Fail {
			res.Message = strings.TrimSpace(strings.Join(context, "\n"))
		}
		out = append(out, res)
		context = context[:0]
	}

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m := reGoVerbose.FindStringSubmatch(line); m != nil {
			secs, _ := strconv.ParseFloat(m[3], 64)
			emit("", m[2], logStatus(m[1]), time.Duration(secs*float64(time.Second)))
			continue
		}
		if m := rePytest.FindStringSubmatch(line); m != nil {
			pytest = true
			emit("", m[1]+"::"+m[2], logStatus(m[3]), 0)
			continue
		}
		if pytest {
			if m := rePytestSection.FindStringSubmatch(line); m != nil {
				section = &strings.Builder{}
				details[m[1]] = section
				continue
			}
			if m := rePytestSummary.FindStringSubmatch(line); m != nil {
				section = nil
				if details[m[1]] == nil {
					details[m[1]] = &strings.Builder{}
				}
				details[m[1]].WriteString(m[2])
				continue
			}
			if rePytestBanner.MatchString(line) {
				section = nil
				continue
			}
			if section != nil {
				section.WriteString(line + "\n")
				continue
			}
		}
		// pytest's "short test summary" repeats results as "FAILED a.py::b";
		// skip those so they are not counted twice.
		if m := reGeneric.FindStringSubmatch(line); m != nil && !(pytest && strings.Contains(m[2], "::")) {
			emit("", m[2], logStatus(m[1]), parseLogDuration(m[3]))
			continue
		}
		if strings.TrimSpace(line) == "" || reNoise.MatchString(line) {
			continue
		}
		context = append(context, line)
		if len(context) > maxContext {
			context = context[len(context)-maxContext:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if pytest {
		attachPytestDetails(out, details)
	}
	return out, nil
}

// attachPytestDetails gives each failed pytest result the traceback from its
// FAILURES section (keyed by "test_name" or "TestClass.test_name") and, if
// that is missing, its one-line short-summary reason (keyed by test id).
func attachPytestDetails(out []result.TestResult, details map[string]*strings.Builder) {
	for i := range out {
		r := &out[i]
		if r.Status != result.Fail || r.Message != "" {
			continue
		}
		id := r.Name
		_, rest, _ := strings.Cut(id, "::")
		var msg string
		if b := details[strings.ReplaceAll(rest, "::", ".")]; b != nil {
			msg = b.String()
		}
		if b := details[id]; b != nil && !strings.Contains(msg, b.String()) {
			msg += "\n" + b.String()
		}
		r.Message = strings.TrimSpace(msg)
	}
}

func logStatus(s string) result.Status {
	switch s {
	case "FAIL", "FAILED", "ERROR", "RERUN":
		return result.Fail
	case "SKIP", "SKIPPED":
		return result.Skip
	}
	return result.Pass
}

func parseLogDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
