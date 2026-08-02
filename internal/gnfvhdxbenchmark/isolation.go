package gnfvhdxbenchmark

import "fmt"

func VerifyParentHash(before, after string) error {
	if before == "" || after == "" || before != after {
		return benchmarkError(CodeParentChanged, "compare-parent-hash", "", fmt.Errorf("before=%q after=%q", before, after))
	}
	return nil
}

// VerifyMarkers requires the current Child marker, rejects older markers in
// the current Child, and rejects the current marker in the immutable Parent.
func VerifyMarkers(current string, childMarkers, parentMarkers []string) error {
	foundCurrent := false
	for _, marker := range childMarkers {
		if marker == current {
			foundCurrent = true
		} else {
			return benchmarkError(CodeContamination, "verify-child-markers", marker, fmt.Errorf("previous Child marker is visible"))
		}
	}
	for _, marker := range parentMarkers {
		if marker == current {
			return benchmarkError(CodeContamination, "verify-parent-markers", marker, fmt.Errorf("Child marker reached Parent"))
		}
	}
	if !foundCurrent {
		return benchmarkError(CodeContamination, "verify-child-markers", current, fmt.Errorf("current marker is missing"))
	}
	return nil
}
