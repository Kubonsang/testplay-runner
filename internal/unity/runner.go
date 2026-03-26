package unity

import (
	"bytes"
	"context"
	"os/exec"
)

// Runner abstracts the Unity subprocess, allowing tests to inject a fake.
type Runner interface {
	// Run executes Unity with the given args.
	Run(ctx context.Context, args []string) (stdout []byte, stderr []byte, exitCode int, err error)
}

// ProcessRunner is the real implementation backed by exec.Cmd.
type ProcessRunner struct {
	UnityPath string
}

// Run executes the Unity binary with the provided args.
func (r *ProcessRunner) Run(ctx context.Context, args []string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, r.UnityPath, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	stdout, err := cmd.Output()
	stderr := stderrBuf.Bytes()
	if exitErr, ok := err.(*exec.ExitError); ok {
		// exitErr.Stderr is populated by cmd.Output() only when cmd.Stderr is nil.
		// Since we set cmd.Stderr, use stderrBuf instead.
		return stdout, stderr, exitErr.ExitCode(), nil
	}
	if err != nil {
		return nil, stderr, -1, err
	}
	return stdout, stderr, 0, nil
}

// BuildCompileArgs constructs the Unity CLI arguments for a compile-only run
// (no -runTests, exits after compilation via -quit). Used by the two-phase
// executor to enforce a separate compile timeout.
func BuildCompileArgs(projectPath string) []string {
	return []string{
		"-batchmode",
		"-nographics",
		"-projectPath", projectPath,
		"-quit",
	}
}

// RunOptions configures a Unity test run.
type RunOptions struct {
	Filter          string
	Category        string
	ResultsFilePath string
	TestPlatform    string // "edit_mode" | "play_mode"; defaults to EditMode if empty
}

// BuildRunArgs constructs the Unity CLI arguments for a test run.
func BuildRunArgs(projectPath string, opts *RunOptions) []string {
	platform := "EditMode"
	if opts.TestPlatform == "play_mode" {
		platform = "PlayMode"
	}

	args := []string{
		"-batchmode",
		"-nographics",
		"-runTests",
		"-testPlatform", platform,
		"-projectPath", projectPath,
	}

	if opts.ResultsFilePath != "" {
		args = append(args, "-testResults", opts.ResultsFilePath)
	}
	if opts.Filter != "" {
		args = append(args, "-testFilter", opts.Filter)
	}
	if opts.Category != "" {
		args = append(args, "-testCategory", opts.Category)
	}

	return args
}
