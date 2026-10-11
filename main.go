// Command ci-feedback summarizes test reports for CI: it highlights failures,
// flaky and slow tests, and groups failures by known signatures, then exits
// non-zero when the configured checks fail.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/analyze"
	"github.com/aniketnandi/ci-feedback/internal/config"
	"github.com/aniketnandi/ci-feedback/internal/parse"
	"github.com/aniketnandi/ci-feedback/internal/report"
	"github.com/aniketnandi/ci-feedback/internal/result"
)

// Exit codes.
const (
	exitOK    = 0 // all enabled checks passed
	exitCheck = 1 // at least one enabled check failed
	exitUsage = 2 // bad flags, unreadable or unparsable input
)

// version can be set at build time with -ldflags "-X main.version=v1.2.3";
// otherwise it comes from the module version stamped by `go install`.
var version string

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

const usage = `Usage: ci-feedback [flags] FILE...

Summarize test reports and fail the CI step on failures, flaky or slow tests.
FILE may be JUnit XML, 'go test -json' output, or a plain-text test log; use
"-" to read stdin. Multiple files (e.g. retries or several CI runs) are
analyzed together, which is how flaky tests are found.

Exit status: 0 checks passed, 1 a check failed, 2 usage or input error.

Flags:
`

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ci-feedback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	var (
		inFormat   = fs.String("format", "auto", "input format: auto, junit, gotest or log")
		outFormat  = fs.String("output", "text", "output format: text, json or github (workflow annotations + text)")
		configPath = fs.String("config", "", "rules file (default: "+config.DefaultFile+" if present)")
		slow       = fs.Duration("slow", 2*time.Second, "flag tests that take at least this long (0 disables)")
		top        = fs.Int("top", 10, "list at most this many slow tests (0 = all)")
		failOn     = fs.String("fail-on", "failures", "comma-separated checks that fail the build: failures, flaky, slow, all, none")
		workers    = fs.Int("j", runtime.NumCPU(), "number of files to parse in parallel")
		showVer    = fs.Bool("version", false, "print version and exit")
		ignore     stringList
	)
	fs.Var(&ignore, "ignore", "glob of tests to ignore, e.g. 'TestKnownFlaky*' (repeatable)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *showVer {
		fmt.Fprintln(stdout, "ci-feedback", versionString())
		return exitOK
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "ci-feedback:", err)
		return exitUsage
	}

	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return exitUsage
	}
	if n := count(paths, "-"); n > 1 {
		return fail(errors.New(`stdin ("-") can only be given once`))
	}

	format, err := parse.ParseFormat(*inFormat)
	if err != nil {
		return fail(err)
	}
	outFmt, err := report.ParseFormat(*outFormat)
	if err != nil {
		return fail(err)
	}
	if *top < 0 {
		return fail(errors.New("-top must not be negative"))
	}
	if *slow < 0 {
		return fail(errors.New("-slow must not be negative"))
	}

	// Settings: built-in defaults < rules file < explicit flags.
	cfgFile := *configPath
	if cfgFile == "" {
		cfgFile = config.DefaultFile
	}
	cfg, err := config.Load(cfgFile, *configPath != "")
	if err != nil {
		return fail(err)
	}
	sigs, err := cfg.CompileSignatures()
	if err != nil {
		return fail(err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	opts := analyze.Options{
		SlowThreshold: *slow,
		TopSlow:       *top,
		Signatures:    sigs,
		Ignore:        append(append([]string{}, cfg.Ignore...), ignore...),
	}
	if cfg.SlowThreshold != nil && !set["slow"] {
		opts.SlowThreshold = time.Duration(*cfg.SlowThreshold)
	}
	if cfg.TopSlow != nil && !set["top"] {
		opts.TopSlow = *cfg.TopSlow
	}
	checkSpec := *failOn
	if cfg.FailOn != nil && !set["fail-on"] {
		checkSpec = strings.Join(cfg.FailOn, ",")
	}
	checks, err := analyze.ParseChecks(checkSpec)
	if err != nil {
		return fail(err)
	}

	inputs, err := parse.ParseFiles(paths, format, *workers, stdin)
	if err != nil {
		return fail(err)
	}
	var all []result.TestResult
	files := make([]string, len(inputs))
	for i, in := range inputs {
		files[i] = in.Path
		all = append(all, in.Results...)
	}

	rep := analyze.Analyze(all, opts)
	sum := report.Summary{Report: rep, Files: files, Checks: checks, Violations: rep.Violations(checks)}
	if err := report.Write(stdout, outFmt, sum); err != nil {
		return fail(err)
	}
	if !sum.Passed() {
		return exitCheck
	}
	return exitOK
}

func count(ss []string, v string) int {
	n := 0
	for _, s := range ss {
		if s == v {
			n++
		}
	}
	return n
}
