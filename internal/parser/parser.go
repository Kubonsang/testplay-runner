package parser

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// fileLineRe matches Unity NUnit stack trace lines like:
//
//	at Foo() in /path/file.cs:line 42
//	at Foo() in C:\path\file.cs:line 42
var fileLineRe = regexp.MustCompile(`(?m) in (.+):line (\d+)`)

// fileLineReAlt matches Unity editor-style stack trace lines like:
//
//	Foo () (at /path/file.cs:42)
var fileLineReAlt = regexp.MustCompile(`(?m)\(at (.+):(\d+)\)`)

// Result holds the parsed NUnit test-run output.
type Result struct {
	Total    int        `json:"total"`
	Passed   int        `json:"passed"`
	Failed   int        `json:"failed"`
	Skipped  int        `json:"skipped"`
	Duration float64    `json:"duration_s"`
	Tests    []TestCase `json:"tests"`
}

// TestCase represents a single NUnit test-case element.
type TestCase struct {
	Name               string  `json:"name"`
	Result             string  `json:"result"`
	Duration           float64 `json:"duration_s"`
	Message            string  `json:"message,omitempty"`
	File               string  `json:"file,omitempty"`
	AbsolutePath       string  `json:"absolute_path,omitempty"`
	Line               int     `json:"line,omitempty"`
	ParameterizedGroup string  `json:"parameterized_group,omitempty"`
}

// FailedTests returns all test cases with Result == "Failed".
func (r *Result) FailedTests() []TestCase {
	out := make([]TestCase, 0)
	for _, tc := range r.Tests {
		if tc.Result == "Failed" {
			out = append(out, tc)
		}
	}
	return out
}

// xmlTestRun is the XML unmarshalling target.
type xmlTestRun struct {
	XMLName    xml.Name       `xml:"test-run"`
	Total      int            `xml:"total,attr"`
	Passed     int            `xml:"passed,attr"`
	Failed     int            `xml:"failed,attr"`
	Skipped    int            `xml:"skipped,attr"`
	Duration   string         `xml:"duration,attr"`
	TestSuites []xmlTestSuite `xml:"test-suite"`
}

type xmlTestSuite struct {
	Type       string         `xml:"type,attr"`
	Name       string         `xml:"name,attr"`
	FullName   string         `xml:"fullname,attr"`
	TestCases  []xmlTestCase  `xml:"test-case"`
	TestSuites []xmlTestSuite `xml:"test-suite"`
}

type xmlTestCase struct {
	Name     string   `xml:"name,attr"`
	FullName string   `xml:"fullname,attr"`
	Result   string   `xml:"result,attr"`
	Duration string   `xml:"duration,attr"`
	Failure  *xmlFail `xml:"failure"`
}

type xmlFail struct {
	Message    string `xml:"message"`
	StackTrace string `xml:"stack-trace"`
}

// Parse parses NUnit XML bytes into a Result.
func Parse(data []byte) (*Result, error) {
	var run xmlTestRun
	if err := xml.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("parsing NUnit XML: %w", err)
	}

	dur, _ := parseDuration(run.Duration)
	result := &Result{
		Total:    run.Total,
		Passed:   run.Passed,
		Failed:   run.Failed,
		Skipped:  run.Skipped,
		Duration: dur,
		Tests:    make([]TestCase, 0),
	}

	for _, suite := range run.TestSuites {
		collectCases(&result.Tests, suite, "")
	}

	return result, nil
}

func collectCases(out *[]TestCase, suite xmlTestSuite, paramGroup string) {
	group := paramGroup
	if suite.Type == "ParameterizedMethod" {
		group = suite.FullName
	}

	for _, sub := range suite.TestSuites {
		collectCases(out, sub, group)
	}
	for _, xc := range suite.TestCases {
		tc := TestCase{
			Name:               xc.FullName,
			Result:             xc.Result,
			ParameterizedGroup: group,
		}
		if d, err := parseDuration(xc.Duration); err == nil {
			tc.Duration = d
		}
		if xc.Failure != nil {
			tc.Message = strings.TrimSpace(xc.Failure.Message)
			absPath, line := extractFileAndLine(xc.Failure.StackTrace)
			tc.AbsolutePath = absPath
			tc.File = absPath // file = absolute_path for now; caller can make relative
			tc.Line = line
		}
		*out = append(*out, tc)
	}
}

// MakeRelative returns the path relative to projectPath.
// If absPath is not under projectPath, it returns absPath unchanged.
func MakeRelative(projectPath, absPath string) string {
	rel, err := filepath.Rel(projectPath, absPath)
	if err != nil {
		return absPath
	}
	// If the result starts with "..", it's outside the project
	if strings.HasPrefix(rel, "..") {
		return absPath
	}
	return rel
}

// parseDuration parses an NUnit duration string to float64.
// It normalises comma decimal separators (e.g. "1,5" → "1.5") produced by
// Unity on Windows systems with a non-English locale.
func parseDuration(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
}

// extractFileAndLine extracts file path and line number from a Unity stack trace.
// It tries the NUnit format first ("in path:line N"), then the editor format ("(at path:N)").
// Returns ("", 0) if neither pattern matches.
func extractFileAndLine(stackTrace string) (string, int) {
	if m := fileLineRe.FindStringSubmatch(stackTrace); m != nil {
		line, _ := strconv.Atoi(m[2])
		return strings.TrimSpace(m[1]), line
	}
	if m := fileLineReAlt.FindStringSubmatch(stackTrace); m != nil {
		line, _ := strconv.Atoi(m[2])
		return strings.TrimSpace(m[1]), line
	}
	return "", 0
}
