package gnfvhdxbenchmark

import (
	"fmt"

	"github.com/Kubonsang/testplay-runner/internal/unityvhdxfixture"
)

type SemanticResult = unityvhdxfixture.PlatformResult

func ParseSemantic(exitCode int, resultsPath, logPath string, wallClockMs int64) (SemanticResult, error) {
	result, err := unityvhdxfixture.ParsePlatformResult(SelectionPlatform, exitCode, resultsPath, logPath, wallClockMs)
	if err != nil {
		return result, err
	}
	if err := unityvhdxfixture.RequirePassing(result); err != nil {
		return result, err
	}
	if result.Total != 1 || len(result.Tests) != 1 || result.Tests[0].FullName != SelectionFilter {
		return result, benchmarkError(CodeSelectionMismatch, "validate-selection", resultsPath, fmt.Errorf("expected exactly %q; got %#v", SelectionFilter, result.Tests))
	}
	return result, nil
}

func CompareSemantic(reference, candidate SemanticResult) error {
	if err := unityvhdxfixture.CompareSemantic(reference, candidate); err != nil {
		return benchmarkError(CodeSemanticMismatch, "compare-semantic-results", candidate.ResultsXML, err)
	}
	return nil
}
