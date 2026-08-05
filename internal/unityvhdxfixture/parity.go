package unityvhdxfixture

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/parser"
)

type canonicalResult struct {
	Platform      string         `json:"platform"`
	ExitCode      int            `json:"exitCode"`
	Total         int            `json:"total"`
	Passed        int            `json:"passed"`
	Failed        int            `json:"failed"`
	Skipped       int            `json:"skipped"`
	Inconclusive  int            `json:"inconclusive"`
	Tests         []SemanticTest `json:"tests"`
	CompileErrors []string       `json:"compileErrors"`
}

func ParsePlatformResult(platform string, exitCode int, resultsPath, logPath string, wallClockMs int64) (PlatformResult, error) {
	data, err := os.ReadFile(resultsPath)
	if err != nil {
		return PlatformResult{}, fixtureError(CodeUnityRunFailed, "read-results", resultsPath, err)
	}
	parsed, err := parser.Parse(data)
	if err != nil {
		return PlatformResult{}, fixtureError(CodeUnityRunFailed, "parse-results", resultsPath, err)
	}
	tests := make([]SemanticTest, 0, len(parsed.Tests))
	inconclusive := 0
	for _, test := range parsed.Tests {
		outcome := strings.TrimSpace(test.Result)
		if strings.EqualFold(outcome, "Inconclusive") {
			inconclusive++
		}
		tests = append(tests, SemanticTest{FullName: test.Name, Outcome: outcome})
	}
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].FullName == tests[j].FullName {
			return tests[i].Outcome < tests[j].Outcome
		}
		return tests[i].FullName < tests[j].FullName
	})
	compileErrors, err := parseCompileErrors(logPath)
	if err != nil {
		return PlatformResult{}, fixtureError(CodeUnityRunFailed, "read-editor-log", logPath, err)
	}
	canonical := canonicalResult{Platform: platform, ExitCode: exitCode, Total: parsed.Total, Passed: parsed.Passed, Failed: parsed.Failed, Skipped: parsed.Skipped, Inconclusive: inconclusive, Tests: tests, CompileErrors: compileErrors}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return PlatformResult{}, err
	}
	digest := sha256.Sum256(encoded)
	return PlatformResult{Platform: platform, ExitCode: exitCode, Total: parsed.Total, Passed: parsed.Passed, Failed: parsed.Failed, Skipped: parsed.Skipped, Inconclusive: inconclusive, Tests: tests, CompileErrors: compileErrors, SemanticDigest: hex.EncodeToString(digest[:]), ResultsXML: resultsPath, EditorLog: logPath, WallClockMs: wallClockMs}, nil
}

func parseCompileErrors(logPath string) ([]string, error) {
	file, err := os.Open(logPath)
	if err != nil {
		// Synthetic parser callers historically omit a log. Real Unity runs go
		// through UnityExecutor, which always materializes the requested log.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(line)
		index := strings.Index(lower, "error cs")
		if index < 0 {
			continue
		}
		// Keep only the stable compiler diagnostic. Unity prefixes diagnostics
		// with absolute or project-relative paths that differ per workspace.
		diagnostic := strings.TrimSpace(line[index:])
		if diagnostic != "" {
			seen[diagnostic] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(seen))
	for diagnostic := range seen {
		result = append(result, diagnostic)
	}
	sort.Strings(result)
	return result, nil
}

func RequirePassing(result PlatformResult) error {
	if result.ExitCode != 0 || result.Failed != 0 || result.Total == 0 || result.Passed == 0 || len(result.CompileErrors) != 0 {
		return fixtureError(CodeUnityRunFailed, "validate-test-result", result.ResultsXML, fmt.Errorf("platform=%s exit=%d total=%d passed=%d failed=%d", result.Platform, result.ExitCode, result.Total, result.Passed, result.Failed))
	}
	return nil
}

// RequireExpectedTests rejects an apparently successful Unity invocation when
// the requested selection silently changed or only a subset ran.
func RequireExpectedTests(result PlatformResult, expected []string) error {
	if err := RequirePassing(result); err != nil {
		return err
	}
	actual := make([]string, 0, len(result.Tests))
	for _, test := range result.Tests {
		if !strings.EqualFold(test.Outcome, "Passed") {
			return fixtureError(CodeUnityRunFailed, "validate-test-outcome", result.ResultsXML, fmt.Errorf("%s=%s", test.FullName, test.Outcome))
		}
		actual = append(actual, test.FullName)
	}
	want := append([]string(nil), expected...)
	sort.Strings(actual)
	sort.Strings(want)
	if len(actual) != len(want) {
		return fixtureError(CodeUnityRunFailed, "validate-test-set", result.ResultsXML, fmt.Errorf("actual=%v expected=%v", actual, want))
	}
	for index := range want {
		if actual[index] != want[index] {
			return fixtureError(CodeUnityRunFailed, "validate-test-set", result.ResultsXML, fmt.Errorf("actual=%v expected=%v", actual, want))
		}
	}
	return nil
}

func CompareSemantic(physical, vhdx PlatformResult) error {
	if physical.Platform != vhdx.Platform || physical.ExitCode != vhdx.ExitCode || physical.Total != vhdx.Total || physical.Passed != vhdx.Passed || physical.Failed != vhdx.Failed || physical.Skipped != vhdx.Skipped || physical.Inconclusive != vhdx.Inconclusive || physical.SemanticDigest != vhdx.SemanticDigest {
		return fixtureError(CodeSemanticParityFailed, "compare-results", physical.Platform, fmt.Errorf("physical=%s vhdx=%s", physical.SemanticDigest, vhdx.SemanticDigest))
	}
	return nil
}
