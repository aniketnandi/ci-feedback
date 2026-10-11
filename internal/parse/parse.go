// Package parse turns test reports (JUnit XML, `go test -json` output, or
// plain-text logs) into a flat list of result.TestResult values.
package parse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

// Format names an input format.
type Format string

const (
	Auto   Format = "auto"
	JUnit  Format = "junit"
	GoTest Format = "gotest"
	Log    Format = "log"
)

// ErrNoResults is returned when an input parses cleanly but contains no tests,
// which in CI almost always means the wrong file was passed.
var ErrNoResults = errors.New("no test results found")

// ParseFormat validates a user-supplied format name.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(strings.TrimSpace(s))); f {
	case Auto, JUnit, GoTest, Log:
		return f, nil
	case "go", "json":
		return GoTest, nil
	case "xml":
		return JUnit, nil
	}
	return "", fmt.Errorf("unknown format %q (want auto, junit, gotest or log)", s)
}

// Detect guesses the format from the first bytes of an input.
func Detect(peek []byte) Format {
	b := bytes.TrimPrefix(peek, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	b = bytes.TrimLeft(b, " \t\r\n")
	switch {
	case len(b) == 0:
		return Log
	case b[0] == '<':
		return JUnit
	case b[0] == '{':
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line = b[:i]
		}
		if bytes.Contains(line, []byte(`"Action"`)) {
			return GoTest
		}
	}
	return Log
}

// Parse reads a single report. With Format Auto the format is detected from
// the content; the format actually used is returned.
func Parse(r io.Reader, f Format) ([]result.TestResult, Format, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	if f == Auto {
		peek, err := br.Peek(4096)
		if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
			return nil, f, err
		}
		f = Detect(peek)
	}

	var (
		res []result.TestResult
		err error
	)
	switch f {
	case JUnit:
		res, err = parseJUnit(br)
	case GoTest:
		res, err = parseGoTest(br)
	case Log:
		res, err = parseLog(br)
	default:
		return nil, f, fmt.Errorf("unsupported format %q", f)
	}
	if err != nil {
		return nil, f, err
	}
	if len(res) == 0 {
		return nil, f, ErrNoResults
	}
	return dropRedundantParents(res), f, nil
}

// Input is the parsed content of one file.
type Input struct {
	Path    string
	Format  Format
	Results []result.TestResult
}

// ParseFiles parses paths concurrently using up to workers goroutines. The
// path "-" reads from stdin. Results are returned in the order of paths; if
// any file fails, the joined error names every failing file.
func ParseFiles(paths []string, f Format, workers int, stdin io.Reader) ([]Input, error) {
	if workers < 1 {
		workers = 1
	}
	if workers > len(paths) {
		workers = len(paths)
	}
	out := make([]Input, len(paths))
	errs := make([]error, len(paths))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i], errs[i] = parseOne(paths[i], f, stdin)
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return out, errors.Join(errs...)
}

func parseOne(path string, f Format, stdin io.Reader) (Input, error) {
	in := Input{Path: path}
	var r io.Reader
	if path == "-" {
		in.Path = "<stdin>"
		r = stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return in, err // *PathError already names the file
		}
		defer file.Close()
		r = file
	}

	res, used, err := Parse(r, f)
	in.Format = used
	if err != nil {
		return in, fmt.Errorf("%s (%s): %w", in.Path, used, err)
	}
	for i := range res {
		res[i].Source = in.Path
	}
	in.Results = res
	return in, nil
}

// dropRedundantParents removes a parent test's failure when it failed only
// because one of its subtests ("Parent/child") failed, so a single broken
// subtest is not reported twice. Reporters disagree on whether the parent is
// written before (go test -v) or after (test2json) its children, so the
// nearest result for the parent in either direction is considered. A child
// with no output of its own inherits the parent's message.
func dropRedundantParents(res []result.TestResult) []result.TestResult {
	drop := make([]bool, len(res))
	for i, r := range res {
		if r.Status != result.Fail || !strings.Contains(r.Name, "/") {
			continue
		}
		for parent := r.Name; ; {
			j := strings.LastIndexByte(parent, '/')
			if j < 0 {
				break
			}
			parent = parent[:j]
			if k := nearest(res, i, r.Suite, parent); k >= 0 && res[k].Status == result.Fail {
				drop[k] = true
				if res[i].Message == "" {
					res[i].Message = res[k].Message
				}
			}
		}
	}
	kept := res[:0]
	for i, r := range res {
		if !drop[i] {
			kept = append(kept, r)
		}
	}
	return kept
}

func nearest(res []result.TestResult, from int, suite, name string) int {
	for d := 1; from-d >= 0 || from+d < len(res); d++ {
		if k := from - d; k >= 0 && res[k].Suite == suite && res[k].Name == name {
			return k
		}
		if k := from + d; k < len(res) && res[k].Suite == suite && res[k].Name == name {
			return k
		}
	}
	return -1
}
