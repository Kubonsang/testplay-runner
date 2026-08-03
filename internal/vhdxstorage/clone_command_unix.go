//go:build darwin || linux

package vhdxstorage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type cloneErrorKind int

const (
	cloneErrorUnavailable cloneErrorKind = iota
	cloneErrorCancelled
	cloneErrorFailed
)

type cloneCommandRunner interface {
	LookPath(file string) (string, error)
	CombinedOutput(ctx context.Context, path string, args, environment []string) ([]byte, error)
}

type execCloneCommandRunner struct{}

func (execCloneCommandRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (execCloneCommandRunner) CombinedOutput(ctx context.Context, path string, args, environment []string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Env = environment
	return command.CombinedOutput()
}

func cloneTreeWithRunner(ctx context.Context, source, destination string, runner cloneCommandRunner) error {
	path, err := runner.LookPath("cp")
	if err != nil {
		return fmt.Errorf("%w: GNU cp was not found: %v", errCoWUnavailable, err)
	}
	args := []string{
		"--archive",
		"--no-preserve=ownership",
		"--reflink=always",
		"--no-target-directory",
		"--",
		source,
		destination,
	}
	output, runErr := runner.CombinedOutput(ctx, path, args, cLocaleEnvironment(os.Environ()))
	if runErr == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = runErr.Error()
	}
	switch classifyLinuxCloneError(ctx, detail, runErr) {
	case cloneErrorUnavailable:
		return fmt.Errorf("%w: cp --reflink=always: %s", errCoWUnavailable, detail)
	case cloneErrorCancelled:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return runErr
	default:
		return fmt.Errorf("cp --reflink=always failed: %s: %w", detail, runErr)
	}
}

func classifyLinuxCloneError(ctx context.Context, detail string, runErr error) cloneErrorKind {
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return cloneErrorCancelled
	}
	lowerDetail := strings.ToLower(detail)
	for _, phrase := range []string{"invalid cross-device link", "cross-device link"} {
		if strings.Contains(lowerDetail, phrase) {
			return cloneErrorUnavailable
		}
	}
	cloneDiagnostic := strings.Contains(lowerDetail, "failed to clone") || strings.Contains(lowerDetail, "reflink")
	if cloneDiagnostic && (strings.Contains(lowerDetail, "operation not supported") || strings.Contains(lowerDetail, "function not implemented")) {
		return cloneErrorUnavailable
	}
	return cloneErrorFailed
}

func cLocaleEnvironment(base []string) []string {
	environment := make([]string, 0, len(base)+2)
	for _, value := range base {
		if strings.HasPrefix(value, "LC_ALL=") || strings.HasPrefix(value, "LANG=") {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, "LC_ALL=C", "LANG=C")
}
