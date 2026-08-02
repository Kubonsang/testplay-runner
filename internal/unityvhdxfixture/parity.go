package unityvhdxfixture

import (
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
	Platform     string         `json:"platform"`
	ExitCode     int            `json:"exitCode"`
	Total        int            `json:"total"`
	Passed       int            `json:"passed"`
	Failed       int            `json:"failed"`
	Skipped      int            `json:"skipped"`
	Inconclusive int            `json:"inconclusive"`
	Tests        []SemanticTest `json:"tests"`
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
	canonical := canonicalResult{Platform: platform, ExitCode: exitCode, Total: parsed.Total, Passed: parsed.Passed, Failed: parsed.Failed, Skipped: parsed.Skipped, Inconclusive: inconclusive, Tests: tests}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return PlatformResult{}, err
	}
	digest := sha256.Sum256(encoded)
	return PlatformResult{Platform: platform, ExitCode: exitCode, Total: parsed.Total, Passed: parsed.Passed, Failed: parsed.Failed, Skipped: parsed.Skipped, Inconclusive: inconclusive, Tests: tests, SemanticDigest: hex.EncodeToString(digest[:]), ResultsXML: resultsPath, EditorLog: logPath, WallClockMs: wallClockMs}, nil
}

func RequirePassing(result PlatformResult) error {
	if result.ExitCode != 0 || result.Failed != 0 || result.Total == 0 || result.Passed == 0 {
		return fixtureError(CodeUnityRunFailed, "validate-test-result", result.ResultsXML, fmt.Errorf("platform=%s exit=%d total=%d passed=%d failed=%d", result.Platform, result.ExitCode, result.Total, result.Passed, result.Failed))
	}
	return nil
}

func CompareSemantic(physical, vhdx PlatformResult) error {
	if physical.Platform != vhdx.Platform || physical.ExitCode != vhdx.ExitCode || physical.Total != vhdx.Total || physical.Passed != vhdx.Passed || physical.Failed != vhdx.Failed || physical.Skipped != vhdx.Skipped || physical.Inconclusive != vhdx.Inconclusive || physical.SemanticDigest != vhdx.SemanticDigest {
		return fixtureError(CodeSemanticParityFailed, "compare-results", physical.Platform, fmt.Errorf("physical=%s vhdx=%s", physical.SemanticDigest, vhdx.SemanticDigest))
	}
	return nil
}
