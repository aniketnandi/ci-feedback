package parse

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aniketnandi/ci-feedback/internal/result"
)

const examples = "../../examples"

func TestDetect(t *testing.T) {
	cases := map[string]Format{
		"\xef\xbb\xbf  <?xml version=\"1.0\"?><testsuites/>": JUnit,
		"\n<testsuite>": JUnit,
		`{"Time":"x","Action":"start","Package":"p"}` + "\n": GoTest,
		`{"some":"other json"}`:                              Log,
		"--- PASS: TestX (0.00s)":                            Log,
		"":                                                   Log,
	}
	for in, want := range cases {
		if got := Detect([]byte(in)); got != want {
			t.Errorf("Detect(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"auto": Auto, "JUnit": JUnit, "xml": JUnit, "go": GoTest, " log ": Log} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(yaml) succeeded, want error")
	}
}

func parseExample(t *testing.T, name string) ([]result.TestResult, Format) {
	t.Helper()
	f, err := os.Open(filepath.Join(examples, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, format, err := Parse(f, Auto)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return res, format
}

// tally maps FullName to its sequence of statuses, e.g. "fail,pass".
func tally(res []result.TestResult) map[string]string {
	m := map[string]string{}
	for _, r := range res {
		k := r.FullName()
		if m[k] != "" {
			m[k] += ","
		}
		m[k] += r.Status.String()
	}
	return m
}

func TestJUnitSurefire(t *testing.T) {
	res, format := parseExample(t, "junit-surefire.xml")
	if format != JUnit {
		t.Fatalf("format = %s, want junit", format)
	}
	got := tally(res)
	want := map[string]string{
		"com.acme.orders.OrderServiceTest.createsOrder":   "pass",
		"com.acme.orders.OrderServiceTest.appliesCoupon":  "fail",
		"com.acme.orders.OrderServiceTest.publishesEvent": "fail,pass", // flakyFailure, then passed
		"com.acme.orders.OrderServiceTest.legacyExport":   "skip",
		"com.acme.orders.PaymentGatewayIT.chargesCard":    "pass",
		"com.acme.orders.PaymentGatewayIT.refundsCard":    "fail,fail,fail", // two reruns
		"com.acme.orders.PaymentGatewayIT.handlesDecline": "fail",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d tests, want %d: %v", len(got), len(want), got)
	}
	for _, r := range res {
		if r.Name == "publishesEvent" && r.Status == result.Pass && r.Duration != 5904*time.Millisecond {
			t.Errorf("publishesEvent duration = %v", r.Duration)
		}
		if r.Name == "appliesCoupon" && !strings.Contains(r.Message, "but was: <100.00>") {
			t.Errorf("appliesCoupon message = %q", r.Message)
		}
	}
}

func TestJUnitSuiteNameFallbackAndNesting(t *testing.T) {
	in := `<testsuites>
	  <testsuite name="outer">
	    <testsuite name="inner"><testcase name="a" time="1,234.5"/></testsuite>
	    <testcase name="b"><failure>boom</failure></testcase>
	  </testsuite>
	</testsuites>`
	res, _, err := Parse(strings.NewReader(in), JUnit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].FullName() != "inner.a" || res[1].FullName() != "outer.b" {
		t.Fatalf("unexpected results: %+v", res)
	}
	if res[0].Duration != 1234500*time.Millisecond {
		t.Errorf("duration = %v, want 1234.5s", res[0].Duration)
	}
	if res[1].Message != "boom" {
		t.Errorf("message = %q", res[1].Message)
	}
}

func TestJUnitInvalid(t *testing.T) {
	_, _, err := Parse(strings.NewReader("<testsuite><testcase name='x'>"), JUnit)
	if err == nil || !strings.Contains(err.Error(), "invalid JUnit XML") {
		t.Fatalf("err = %v, want invalid JUnit XML", err)
	}
	_, _, err = Parse(strings.NewReader("<testsuites/>"), JUnit)
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults", err)
	}
}

func TestGoTestJSONExample(t *testing.T) {
	res, format := parseExample(t, "gotest.json")
	if format != GoTest {
		t.Fatalf("format = %s, want gotest", format)
	}
	got := tally(res)
	const pkg = "example.com/shop/cart."
	want := map[string]string{
		pkg + "TestAddItem":             "pass,pass,pass",
		pkg + "TestCheckoutSlow":        "pass,pass,pass",
		pkg + "TestCacheWarmup":         "fail,pass,pass",
		pkg + "TestParsePrice/valid":    "pass,pass,pass",
		pkg + "TestParsePrice/negative": "fail,fail,fail",
		pkg + "TestInventoryClient":     "fail,fail,fail",
		pkg + "TestLegacyDiscount":      "skip,skip,skip",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	// The parent TestParsePrice failed only because of its subtest.
	if _, ok := got[pkg+"TestParsePrice"]; ok {
		t.Errorf("redundant parent failure reported: %v", got[pkg+"TestParsePrice"])
	}
	for _, r := range res {
		if r.Name == "TestInventoryClient" {
			if !strings.Contains(r.Message, "connection refused") || strings.Contains(r.Message, "--- FAIL") {
				t.Errorf("message not cleaned: %q", r.Message)
			}
		}
	}
}

func TestGoTestBuildFailureAndTimeout(t *testing.T) {
	in := strings.Join([]string{
		`{"ImportPath":"ex/broken [ex/broken.test]","Action":"build-output","Output":"broken/x_test.go:3:1: syntax error\n"}`,
		`{"ImportPath":"ex/broken [ex/broken.test]","Action":"build-fail"}`,
		`{"Action":"start","Package":"ex/broken"}`,
		`{"Action":"fail","Package":"ex/broken","Elapsed":0,"FailedBuild":"ex/broken [ex/broken.test]"}`,
		`{"Action":"run","Package":"ex/hang","Test":"TestHang"}`,
		`{"Action":"output","Package":"ex/hang","Test":"TestHang","Output":"waiting...\n"}`,
		`{"Action":"output","Package":"ex/hang","Output":"panic: test timed out after 10s\n"}`,
		`{"Action":"fail","Package":"ex/hang","Elapsed":10}`,
		"not json, e.g. interleaved stderr",
	}, "\n")
	res, _, err := Parse(strings.NewReader(in), GoTest)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(res), res)
	}
	if res[0].FullName() != "ex/broken.[package]" || !strings.Contains(res[0].Message, "syntax error") {
		t.Errorf("build failure = %+v", res[0])
	}
	if res[1].Name != "TestHang" || res[1].Status != result.Fail ||
		!strings.Contains(res[1].Message, "waiting...") || !strings.Contains(res[1].Message, "test timed out") {
		t.Errorf("unfinished test = %+v", res[1])
	}
}

func TestGoTestInvalidLine(t *testing.T) {
	in := `{"Action":"run","Package":"p","Test":"T"}` + "\n" + `{"Action":`
	_, _, err := Parse(strings.NewReader(in), GoTest)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want error naming line 2", err)
	}
}

func TestLogGoVerbose(t *testing.T) {
	res, format := parseExample(t, "gotest-verbose.log")
	if format != Log {
		t.Fatalf("format = %s, want log", format)
	}
	got := tally(res)
	if got["TestParsePrice"] != "" {
		t.Errorf("redundant parent failure reported")
	}
	if got["TestParsePrice/negative"] != "fail" || got["TestCheckoutSlow"] != "pass" || got["TestLegacyDiscount"] != "skip" {
		t.Errorf("unexpected tally: %v", got)
	}
	for _, r := range res {
		// go -v prints subtest output above the parent's result line; the
		// subtest must inherit it when the parent is dropped.
		if r.Name == "TestParsePrice/negative" && !strings.Contains(r.Message, "expected error") {
			t.Errorf("subtest message = %q", r.Message)
		}
		if r.Name == "TestCheckoutSlow" && r.Duration < 2*time.Second {
			t.Errorf("TestCheckoutSlow duration = %v", r.Duration)
		}
	}
}

func TestLogPytest(t *testing.T) {
	res, _ := parseExample(t, "pytest.log")
	got := tally(res)
	want := map[string]string{
		"tests/test_auth.py::test_login":         "pass",
		"tests/test_auth.py::test_logout":        "pass",
		"tests/test_search.py::test_query_index": "fail,pass", // RERUN then PASSED
		"tests/test_search.py::test_fuzzy_match": "fail",
		"tests/test_upload.py::test_large_file":  "skip",
		"tests/test_upload.py::test_resume":      "fail",
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	for _, r := range res {
		if r.Name == "tests/test_search.py::test_fuzzy_match" && !strings.Contains(r.Message, "assert [] == ['color']") {
			t.Errorf("fuzzy_match message = %q", r.Message)
		}
		if r.Name == "tests/test_upload.py::test_resume" && !strings.Contains(r.Message, "Connection reset") {
			t.Errorf("resume message = %q", r.Message)
		}
	}
}

func TestLogGeneric(t *testing.T) {
	in := `starting suite
PASS: login (120ms)
connecting to db
error: connection refused
FAIL: checkout_flow 2.5s
[SKIP] export
ok  	example.com/pkg	0.01s
FAIL	example.com/pkg	0.02s
`
	res, _, err := Parse(strings.NewReader(in), Auto)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d results, want 3 (go package summary lines must be ignored): %+v", len(res), res)
	}
	if res[0].Duration != 120*time.Millisecond {
		t.Errorf("login duration = %v", res[0].Duration)
	}
	if res[1].Status != result.Fail || res[1].Duration != 2500*time.Millisecond ||
		res[1].Message != "connecting to db\nerror: connection refused" {
		t.Errorf("checkout_flow = %+v", res[1])
	}
	if res[2].Status != result.Skip {
		t.Errorf("export = %+v", res[2])
	}
}

func TestLogNoResults(t *testing.T) {
	_, _, err := Parse(strings.NewReader("just some build output\n"), Auto)
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("err = %v, want ErrNoResults", err)
	}
}

func TestDropRedundantParentsKeepsIndependentParentFailure(t *testing.T) {
	res := []result.TestResult{
		{Name: "TestA/x", Status: result.Pass},
		{Name: "TestA", Status: result.Fail, Message: "parent's own failure"},
		{Name: "TestB/y", Status: result.Fail},
		{Name: "TestB", Status: result.Fail, Message: "because of y"},
	}
	got := tally(dropRedundantParents(res))
	if got["TestA"] != "fail" || got["TestB"] != "" || got["TestB/y"] != "fail" {
		t.Errorf("got %v", got)
	}
}

func TestParseFilesConcurrent(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 20; i++ {
		p := filepath.Join(dir, "r"+string(rune('a'+i))+".log")
		if err := os.WriteFile(p, []byte("PASS: test_"+string(rune('a'+i))+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	inputs, err := ParseFiles(paths, Auto, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, in := range inputs {
		if in.Path != paths[i] || len(in.Results) != 1 || in.Results[0].Source != paths[i] ||
			in.Results[0].Name != "test_"+string(rune('a'+i)) {
			t.Errorf("input %d out of order or wrong: %+v", i, in)
		}
	}
}

func TestParseFilesReportsEveryBadFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.log")
	empty := filepath.Join(dir, "empty.xml")
	os.WriteFile(good, []byte("PASS: a\n"), 0o644)
	os.WriteFile(empty, []byte("<testsuites/>"), 0o644)
	missing := filepath.Join(dir, "missing.xml")

	_, err := ParseFiles([]string{good, empty, missing}, Auto, 2, nil)
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "empty.xml") || !strings.Contains(msg, "no test results") || !strings.Contains(msg, "missing.xml") {
		t.Errorf("error should name both bad files: %v", msg)
	}
	if strings.Contains(msg, "good.log") {
		t.Errorf("error mentions good file: %v", msg)
	}
}

func TestParseFilesStdin(t *testing.T) {
	inputs, err := ParseFiles([]string{"-"}, Auto, 1, strings.NewReader("--- FAIL: TestX (0.10s)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if inputs[0].Path != "<stdin>" || inputs[0].Results[0].Source != "<stdin>" {
		t.Errorf("got %+v", inputs[0])
	}
}
