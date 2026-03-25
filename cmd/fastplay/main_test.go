package main

import (
	"strings"
	"testing"
)

func TestRootCmd_Short_NoPlayMode(t *testing.T) {
	if strings.Contains(rootCmd.Short, "Play Mode") {
		t.Errorf("rootCmd.Short must not contain 'Play Mode' (currently EditMode only): %q", rootCmd.Short)
	}
}

func TestRunCmd_Short_NoPlayMode(t *testing.T) {
	if strings.Contains(runCmd.Short, "Play Mode") {
		t.Errorf("runCmd.Short must not contain 'Play Mode': %q", runCmd.Short)
	}
}
