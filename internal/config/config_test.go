package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	f, err := Parse(strings.NewReader(`{
		"slow_threshold": "1.5s",
		"top_slow": 3,
		"fail_on": ["failures", "flaky"],
		"ignore": ["TestLegacy*"],
		"signatures": [{"name": "db", "pattern": "5432", "hint": "check postgres"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(*f.SlowThreshold) != 1500*time.Millisecond || *f.TopSlow != 3 ||
		len(f.FailOn) != 2 || f.Ignore[0] != "TestLegacy*" {
		t.Errorf("unexpected config: %+v", f)
	}
	sigs, err := f.CompileSignatures()
	if err != nil {
		t.Fatal(err)
	}
	if sigs[0].Name != "db" || len(sigs) < 2 {
		t.Errorf("user signature should come first, followed by built-ins: %d sigs", len(sigs))
	}

	f.DisableBuiltinSignatures = true
	if sigs, _ := f.CompileSignatures(); len(sigs) != 1 {
		t.Errorf("built-ins not disabled: %d sigs", len(sigs))
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		`{"slow_treshold": "1s"}`:    "unknown field",
		`{"slow_threshold": 5}`:      "duration must be a string",
		`{"slow_threshold": "fast"}`: "invalid duration",
		`{"slow_threshold": "-1s"}`:  "negative",
		`{"top_slow": -1}`:           "negative",
		`{`:                          "unexpected EOF",
	}
	for in, want := range cases {
		_, err := Parse(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%s) err = %v, want %q", in, err, want)
		}
	}

	f, _ := Parse(strings.NewReader(`{"signatures": [{"name": "x", "pattern": "("}]}`))
	if _, err := f.CompileSignatures(); err == nil || !strings.Contains(err.Error(), `signature "x"`) {
		t.Errorf("CompileSignatures err = %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.json")
	if f, err := Load(missing, false); err != nil || f == nil {
		t.Errorf("optional missing file: %v", err)
	}
	if _, err := Load(missing, true); err == nil {
		t.Error("required missing file should fail")
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"nope": 1}`), 0o644)
	if _, err := Load(bad, false); err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Errorf("error should name the file: %v", err)
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	f, err := Load("../../examples/ci-feedback.json", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.CompileSignatures(); err != nil {
		t.Fatal(err)
	}
}
