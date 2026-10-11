package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

// goEvent is one line of `go test -json` (test2json) output.
type goEvent struct {
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	ImportPath  string // build-output events (Go 1.24+)
	FailedBuild string // package fail caused by a build failure (Go 1.24+)
}

// packageName is used for failures that belong to a package rather than to a
// single test, e.g. build errors or a panic in TestMain.
const packageName = "[package]"

func parseGoTest(r io.Reader) ([]result.TestResult, error) {
	type key struct{ pkg, test string }
	var (
		br        = bufio.NewReader(r)
		output    = map[key]*strings.Builder{}
		buildOut  = map[string]*strings.Builder{}
		running   = map[string]map[string]bool{}
		testFails = map[string]bool{}
		out       []result.TestResult
		events    int
	)
	buf := func(m map[key]*strings.Builder, k key) *strings.Builder {
		if m[k] == nil {
			m[k] = &strings.Builder{}
		}
		return m[k]
	}

	for lineNo := 1; ; lineNo++ {
		line, err := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '{' {
			var ev goEvent
			if jerr := json.Unmarshal(trimmed, &ev); jerr != nil {
				return nil, fmt.Errorf("line %d: invalid test2json event: %v", lineNo, jerr)
			}
			events++
			k := key{ev.Package, ev.Test}

			switch ev.Action {
			case "build-output":
				if buildOut[ev.ImportPath] == nil {
					buildOut[ev.ImportPath] = &strings.Builder{}
				}
				buildOut[ev.ImportPath].WriteString(ev.Output)
			case "run":
				if ev.Test != "" {
					output[k] = &strings.Builder{}
					if running[ev.Package] == nil {
						running[ev.Package] = map[string]bool{}
					}
					running[ev.Package][ev.Test] = true
				}
			case "output":
				buf(output, k).WriteString(ev.Output)
			case "pass", "fail", "skip":
				if ev.Test != "" {
					r := result.TestResult{
						Suite:    ev.Package,
						Name:     ev.Test,
						Status:   goStatus(ev.Action),
						Duration: time.Duration(ev.Elapsed * float64(time.Second)),
					}
					if r.Status == result.Fail {
						r.Message = cleanGoOutput(buf(output, k).String())
						testFails[ev.Package] = true
					}
					delete(running[ev.Package], ev.Test)
					out = append(out, r)
					continue
				}

				// Package finished: tests still running were killed, e.g. by
				// -timeout or a panic in another goroutine.
				pkgOut := cleanGoOutput(buf(output, k).String())
				for _, name := range sortedKeys(running[ev.Package]) {
					msg := cleanGoOutput(buf(output, key{ev.Package, name}).String())
					out = append(out, result.TestResult{
						Suite:   ev.Package,
						Name:    name,
						Status:  result.Fail,
						Message: strings.TrimSpace(msg + "\n" + pkgOut + "\n(test did not complete before the package exited)"),
					})
					testFails[ev.Package] = true
				}
				delete(running, ev.Package)

				if ev.Action == "fail" && !testFails[ev.Package] {
					msg := pkgOut
					if b := buildOut[ev.FailedBuild]; ev.FailedBuild != "" && b != nil {
						msg = strings.TrimSpace(b.String())
					}
					out = append(out, result.TestResult{
						Suite:    ev.Package,
						Name:     packageName,
						Status:   result.Fail,
						Duration: time.Duration(ev.Elapsed * float64(time.Second)),
						Message:  msg,
					})
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if events == 0 {
		return nil, fmt.Errorf("no test2json events found")
	}
	return out, nil
}

func goStatus(action string) result.Status {
	switch action {
	case "fail":
		return result.Fail
	case "skip":
		return result.Skip
	}
	return result.Pass
}

// cleanGoOutput strips the framing lines (=== RUN, --- FAIL, ...) that go
// test interleaves with a test's own output.
func cleanGoOutput(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "=== ") || strings.HasPrefix(t, "--- ") ||
			t == "FAIL" || t == "PASS" || strings.HasPrefix(t, "FAIL\t") || strings.HasPrefix(t, "ok  \t") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
