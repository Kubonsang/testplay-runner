//go:build darwin || linux

package vhdxstorage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type fakeCloneCommandRunner struct {
	lookPathErr error
	output      string
	runErr      error
	path        string
	args        []string
	environment []string
}

func (f *fakeCloneCommandRunner) LookPath(string) (string, error) {
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return "/usr/bin/cp", nil
}

func (f *fakeCloneCommandRunner) CombinedOutput(_ context.Context, path string, args, environment []string) ([]byte, error) {
	f.path = path
	f.args = append([]string(nil), args...)
	f.environment = append([]string(nil), environment...)
	return []byte(f.output), f.runErr
}

func TestLinuxCloneErrorClassification(t *testing.T) {
	tests := []struct {
		name          string
		detail        string
		cancelContext bool
		want          cloneErrorKind
	}{
		{name: "reflink unsupported", detail: "cp: failed to clone 'dst' from 'src': Operation not supported", want: cloneErrorUnavailable},
		{name: "cross device", detail: "cp: cannot create regular file 'dst': Invalid cross-device link", want: cloneErrorUnavailable},
		{name: "permission denied", detail: "cp: cannot open 'src' for reading: Permission denied", want: cloneErrorFailed},
		{name: "disk full", detail: "cp: error writing 'dst': No space left on device", want: cloneErrorFailed},
		{name: "IO error", detail: "cp: error reading 'src': Input/output error", want: cloneErrorFailed},
		{name: "source read failure", detail: "cp: cannot stat 'src': No such file or directory", want: cloneErrorFailed},
		{name: "unrelated unsupported source read", detail: "cp: cannot read 'src': Operation not supported", want: cloneErrorFailed},
		{name: "unknown", detail: "cp: unexpected failure", want: cloneErrorFailed},
		{name: "cancelled", detail: "signal: killed", cancelContext: true, want: cloneErrorCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.cancelContext {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if got := classifyLinuxCloneError(ctx, test.detail, errors.New("exit status 1")); got != test.want {
				t.Fatalf("classification=%v want=%v", got, test.want)
			}
		})
	}
}

func TestLinuxCloneCommandRunnerMapsErrorsAndUsesCLocale(t *testing.T) {
	tests := []struct {
		name            string
		output          string
		cancelContext   bool
		wantUnavailable bool
		wantCancelled   bool
	}{
		{name: "unsupported", output: "cp: failed to clone: Operation not supported", wantUnavailable: true},
		{name: "cross device", output: "cp: failed to clone: Invalid cross-device link", wantUnavailable: true},
		{name: "permission", output: "cp: cannot create: Permission denied"},
		{name: "disk full", output: "cp: cannot create: No space left on device"},
		{name: "IO", output: "cp: error reading: Input/output error"},
		{name: "unknown", output: "cp: surprising failure"},
		{name: "cancelled", output: "signal: killed", cancelContext: true, wantCancelled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			if test.cancelContext {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			runner := &fakeCloneCommandRunner{output: test.output, runErr: errors.New("exit status 1")}
			err := cloneTreeWithRunner(ctx, "/source", "/destination", runner)
			if errors.Is(err, errCoWUnavailable) != test.wantUnavailable {
				t.Fatalf("error=%v unavailable=%v", err, errors.Is(err, errCoWUnavailable))
			}
			if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) != test.wantCancelled {
				t.Fatalf("error=%v cancelled=%v", err, test.wantCancelled)
			}
			if runner.path != "/usr/bin/cp" {
				t.Fatalf("path=%q", runner.path)
			}
			wantArgs := []string{"--archive", "--no-preserve=ownership", "--reflink=always", "--no-target-directory", "--", "/source", "/destination"}
			if !reflect.DeepEqual(runner.args, wantArgs) {
				t.Fatalf("args=%q want=%q", runner.args, wantArgs)
			}
			if !containsExact(runner.environment, "LC_ALL=C") || !containsExact(runner.environment, "LANG=C") {
				t.Fatalf("C locale not fixed in environment: %q", runner.environment)
			}
		})
	}
}

func TestLinuxCloneCommandMissingIsUnavailable(t *testing.T) {
	runner := &fakeCloneCommandRunner{lookPathErr: fmt.Errorf("executable not found")}
	err := cloneTreeWithRunner(context.Background(), "/source", "/destination", runner)
	if !errors.Is(err, errCoWUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestCLocaleEnvironmentReplacesLocaleVariables(t *testing.T) {
	got := cLocaleEnvironment([]string{"PATH=/bin", "LANG=ko_KR.UTF-8", "LC_ALL=en_US.UTF-8", "OTHER=value"})
	want := []string{"PATH=/bin", "OTHER=value", "LC_ALL=C", "LANG=C"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment=%q want=%q", got, want)
	}
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
