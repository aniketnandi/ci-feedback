// Package config loads the optional JSON rules file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/analyze"
)

// DefaultFile is loaded from the working directory when no -config flag is
// given and the file exists.
const DefaultFile = ".ci-feedback.json"

// Duration accepts Go duration strings ("1.5s", "300ms") in JSON.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"2s\", got %s", b)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if v < 0 {
		return fmt.Errorf("duration %q must not be negative", s)
	}
	*d = Duration(v)
	return nil
}

// SignatureRule is a user-defined failure signature.
type SignatureRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Hint    string `json:"hint"`
}

// File is the schema of the rules file. Pointer fields distinguish "unset"
// from an explicit zero value.
type File struct {
	SlowThreshold            *Duration       `json:"slow_threshold"`
	TopSlow                  *int            `json:"top_slow"`
	FailOn                   []string        `json:"fail_on"`
	Ignore                   []string        `json:"ignore"`
	Signatures               []SignatureRule `json:"signatures"`
	DisableBuiltinSignatures bool            `json:"disable_builtin_signatures"`
}

// Load reads a rules file. If required is false and the file does not exist,
// an empty File is returned.
func Load(path string, required bool) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	f, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return f, nil
}

// Parse decodes a rules file, rejecting unknown keys so typos surface.
func Parse(r io.Reader) (*File, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	if f.TopSlow != nil && *f.TopSlow < 0 {
		return nil, fmt.Errorf("top_slow must not be negative")
	}
	return &f, nil
}

// CompileSignatures compiles the user rules (tried first) followed by the built-in
// rules unless they are disabled.
func (f *File) CompileSignatures() ([]analyze.Signature, error) {
	var out []analyze.Signature
	for _, r := range f.Signatures {
		s, err := analyze.NewSignature(r.Name, r.Pattern, r.Hint)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if !f.DisableBuiltinSignatures {
		out = append(out, analyze.BuiltinSignatures()...)
	}
	return out, nil
}
