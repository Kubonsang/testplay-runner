package main

import (
	"context"
	"io"
	"os"
)

type fakeCmdRunner struct {
	resultsXML []byte
	stderr     []byte
	exitCode   int
	lastArgs   []string // captured on each Run call; use for arg assertions
}

func (f *fakeCmdRunner) Run(_ context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	f.lastArgs = args
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && f.resultsXML != nil {
			_ = os.WriteFile(args[i+1], f.resultsXML, 0644)
		}
	}
	if stderr != nil && len(f.stderr) > 0 {
		_, _ = stderr.Write(f.stderr)
	}
	return f.exitCode, nil
}
