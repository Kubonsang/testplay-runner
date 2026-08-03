//go:build linux

package vhdxstorage

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const platformProvider = ProviderReflink

func cloneTree(ctx context.Context, source, destination string) error {
	path, err := exec.LookPath("cp")
	if err != nil {
		return fmt.Errorf("%w: GNU cp was not found: %v", errCoWUnavailable, err)
	}
	command := exec.CommandContext(
		ctx,
		path,
		"--archive",
		"--no-preserve=ownership",
		"--reflink=always",
		"--no-target-directory",
		"--",
		source,
		destination,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%w: cp --reflink=always: %s", errCoWUnavailable, detail)
}
