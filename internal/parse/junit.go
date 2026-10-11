package parse

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

type junitProblem struct {
	Message    string `xml:"message,attr"`
	Type       string `xml:"type,attr"`
	Body       string `xml:",chardata"`
	StackTrace string `xml:"stackTrace"`
}

func (p junitProblem) text() string {
	msg := strings.TrimSpace(p.Message)
	body := strings.TrimSpace(p.Body)
	if st := strings.TrimSpace(p.StackTrace); st != "" {
		body = strings.TrimSpace(body + "\n" + st)
	}
	switch {
	case body == "":
		msg = strings.TrimSpace(p.Type + " " + msg)
		return msg
	case msg == "" || strings.Contains(body, msg):
		return body
	default:
		return msg + "\n" + body
	}
}

type junitCase struct {
	Name      string `xml:"name,attr"`
	Classname string `xml:"classname,attr"`
	Time      string `xml:"time,attr"`

	Failures []junitProblem `xml:"failure"`
	Errors   []junitProblem `xml:"error"`
	Skipped  *junitProblem  `xml:"skipped"`

	// Maven Surefire / Gradle retry extensions.
	FlakyFailures []junitProblem `xml:"flakyFailure"`
	FlakyErrors   []junitProblem `xml:"flakyError"`
	RerunFailures []junitProblem `xml:"rerunFailure"`
	RerunErrors   []junitProblem `xml:"rerunError"`
}

// results expands one <testcase> into one TestResult per attempt.
func (c junitCase) results(suite string) []result.TestResult {
	if c.Classname != "" {
		suite = c.Classname
	}
	base := result.TestResult{Suite: suite, Name: c.Name}
	final := base
	final.Duration = parseSeconds(c.Time)

	attempt := func(p junitProblem) result.TestResult {
		r := base
		r.Status = result.Fail
		r.Message = p.text()
		return r
	}

	var out []result.TestResult
	switch {
	case len(c.Failures)+len(c.Errors) > 0:
		problems := append(append([]junitProblem{}, c.Failures...), c.Errors...)
		final.Status = result.Fail
		final.Message = problems[0].text()
		for _, p := range append(c.RerunFailures, c.RerunErrors...) {
			out = append(out, attempt(p))
		}
	case c.Skipped != nil:
		final.Status = result.Skip
	default:
		// Passed, possibly only after failed attempts that were retried.
		final.Status = result.Pass
		for _, p := range append(c.FlakyFailures, c.FlakyErrors...) {
			out = append(out, attempt(p))
		}
	}
	return append(out, final)
}

func parseJUnit(r io.Reader) ([]result.TestResult, error) {
	dec := xml.NewDecoder(r)
	// Reports frequently declare ISO-8859-1 or similar; parse best-effort
	// rather than refusing the whole file.
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }

	var (
		suites []string
		out    []result.TestResult
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid JUnit XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "testsuite":
				suites = append(suites, attr(t, "name"))
			case "testcase":
				var c junitCase
				if err := dec.DecodeElement(&c, &t); err != nil {
					return nil, fmt.Errorf("invalid JUnit XML: %w", err)
				}
				suite := ""
				if len(suites) > 0 {
					suite = suites[len(suites)-1]
				}
				out = append(out, c.results(suite)...)
			}
		case xml.EndElement:
			if t.Name.Local == "testsuite" && len(suites) > 0 {
				suites = suites[:len(suites)-1]
			}
		}
	}
	return out, nil
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// parseSeconds parses JUnit's time attribute ("1.234", sometimes "1,234.5").
func parseSeconds(s string) time.Duration {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
	if err != nil || f < 0 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}
