package unity_test

import (
	"context"
	"io"
	"os"
)

// fakeRunner implements Runner for testing.
type fakeRunner struct {
	stdout     []byte
	stderr     []byte
	exitCode   int
	err        error
	// resultsXML, if non-nil, will be written to the resultsFilePath arg
	resultsXML []byte
	// runFn, if non-nil, overrides all other fields and is called directly.
	runFn func(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error)
}

func (f *fakeRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	if f.runFn != nil {
		return f.runFn(ctx, args, stdout, stderr)
	}
	// Find -testResults arg and write resultsXML to that path
	for i, a := range args {
		if a == "-testResults" && i+1 < len(args) && f.resultsXML != nil {
			_ = os.WriteFile(args[i+1], f.resultsXML, 0644)
		}
	}
	if stdout != nil && len(f.stdout) > 0 {
		_, _ = stdout.Write(f.stdout)
	}
	if stderr != nil && len(f.stderr) > 0 {
		_, _ = stderr.Write(f.stderr)
	}
	return f.exitCode, f.err
}
