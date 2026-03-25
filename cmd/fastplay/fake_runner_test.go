package main

import (
	"context"
	"os"
)

type fakeCmdRunner struct {
	resultsXML []byte
	stderr     []byte
	exitCode   int
}

func (f *fakeCmdRunner) Run(_ context.Context, args []string) ([]byte, []byte, int, error) {
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && f.resultsXML != nil {
			_ = os.WriteFile(args[i+1], f.resultsXML, 0644)
		}
	}
	return nil, f.stderr, f.exitCode, nil
}
