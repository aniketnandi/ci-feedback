package analyze

import (
	"fmt"
	"regexp"
)

// Signature classifies a failure message by a regular expression.
type Signature struct {
	Name string
	Hint string
	re   *regexp.Regexp
}

// NewSignature compiles a signature rule.
func NewSignature(name, pattern, hint string) (Signature, error) {
	if name == "" {
		return Signature{}, fmt.Errorf("signature with pattern %q has no name", pattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Signature{}, fmt.Errorf("signature %q: invalid pattern: %w", name, err)
	}
	return Signature{Name: name, Hint: hint, re: re}, nil
}

// Match reports whether msg matches the signature.
func (s Signature) Match(msg string) bool { return s.re != nil && s.re.MatchString(msg) }

// builtin rules, most specific first: the first matching rule wins.
var builtin = []struct{ name, pattern, hint string }{
	{"data-race", `WARNING: DATA RACE|race detected`,
		"a data race was detected; run locally with -race to reproduce"},
	{"out-of-memory", `(?i)out of memory|OutOfMemoryError|cannot allocate memory|OOMKilled`,
		"the process ran out of memory; check for leaks or raise the runner's memory limit"},
	{"nil-pointer", `(?i)nil pointer dereference|NullPointerException|segmentation (fault|violation)|SIGSEGV|Cannot read propert(y|ies) of (undefined|null)`,
		"nil/null dereference; check error paths that return nil values"},
	{"network", `(?i)connection refused|connection reset|no such host|ECONNREFUSED|ECONNRESET|i/o timeout|broken pipe`,
		"a network dependency was unavailable; is the service container up and healthy?"},
	{"timeout", `(?i)test timed out|deadline exceeded|timed out|TimeoutException|timeout`,
		"something took too long; look for slow dependencies, deadlocks, or tight timeouts"},
	{"port-in-use", `(?i)address already in use|EADDRINUSE`,
		"port collision; use an ephemeral port (:0) instead of a fixed one"},
	{"panic", `panic:|Traceback \(most recent call last\)|Unhandled(Promise)?Rejection`,
		"an unhandled panic/exception escaped the test"},
	{"assertion", `(?i)expected\b.*\b(got|but was|to (be|equal))|assert(ion)?\w*(Error| failed)|\bassert\b.*==|not equal|mismatch`,
		"an assertion failed; this is usually a genuine regression"},
}

// BuiltinSignatures returns the default failure signatures.
func BuiltinSignatures() []Signature {
	out := make([]Signature, len(builtin))
	for i, b := range builtin {
		out[i] = Signature{Name: b.name, Hint: b.hint, re: regexp.MustCompile(b.pattern)}
	}
	return out
}
