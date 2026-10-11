// Package result defines the format-independent representation of a single
// test execution that every parser produces and the analyzer consumes.
package result

import "time"

// Status is the outcome of one test execution.
type Status int

const (
	Pass Status = iota
	Fail
	Skip
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	case Skip:
		return "skip"
	default:
		return "unknown"
	}
}

// TestResult is one execution (attempt) of a test. A test that was retried
// or run with -count=N yields several TestResults with the same FullName.
type TestResult struct {
	Suite    string // package, classname or file; may be empty
	Name     string
	Status   Status
	Duration time.Duration
	Message  string // failure output, empty for passing tests
	Source   string // input file the result came from
}

// FullName identifies a test across runs and input files.
func (r TestResult) FullName() string {
	if r.Suite == "" {
		return r.Name
	}
	return r.Suite + "." + r.Name
}
