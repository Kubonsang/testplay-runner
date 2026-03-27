package unity

import (
	"context"
	"io"
	"os/exec"
)

// Runner abstracts the Unity subprocess, allowing tests to inject a fake.
//
// Current scope: single Unity process per invocation. Each call to Run starts
// one Unity batch-mode process and waits for it to exit. There is no inter-process
// communication, log streaming, or multi-process orchestration.
//
// Future work: network harness / NGO orchestration will require running multiple
// processes concurrently (e.g. server + client Unity instances). That will need a
// different abstraction — Runner is intentionally kept minimal until then.
type Runner interface {
	// Run executes Unity with the given args, streaming stdout and stderr to the
	// provided writers. Either writer may be nil (output is discarded).
	Run(ctx context.Context, args []string, stdout, stderr io.Writer) (exitCode int, err error)
}

// ProcessRunner is the real implementation backed by exec.Cmd.
type ProcessRunner struct {
	UnityPath string
}

// Run executes the Unity binary with the provided args, streaming output to
// stdout and stderr. A nil writer discards the corresponding output.
func (r *ProcessRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, r.UnityPath, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setSysProcAttr(cmd)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
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
