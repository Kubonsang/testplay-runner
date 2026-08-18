package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "testplay",
	Short: "Unity test runner for AI agents",
	// A bare invocation must not exit 0: for an automated caller exit 0 means
	// "all tests passed", and cobra's default help would land on stdout,
	// which is reserved for JSON.
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("no command specified; use one of: init, check, list, run, capability, result, version, storage, workspace")
	},
}

// configPath is set by the --config persistent flag.
// Default "testplay.json" preserves existing behaviour when the flag is absent.
var configPath = "testplay.json"

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "testplay.json",
		"Path to testplay.json (default: testplay.json in cwd)")

	// stdout carries exactly one JSON object per command; every human-facing
	// cobra output path is redirected or disabled.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		if cmd.Long != "" {
			fmt.Fprintln(os.Stderr, cmd.Long)
		} else if cmd.Short != "" {
			fmt.Fprintln(os.Stderr, cmd.Short)
		}
		fmt.Fprint(os.Stderr, cmd.UsageString())
	})
}

func main() {
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	// Cobra's hidden shell-completion transport commands write candidates and
	// a directive line directly to stdout. That would violate TestPlay's
	// machine contract (exactly one JSON value), even though the public
	// `completion` command is disabled.
	if isInternalCompletionInvocation(os.Args[1:]) {
		writeJSON(os.Stdout, map[string]any{"error": "shell completion transport commands are disabled"})
		os.Exit(5)
	}
	if err := rootCmd.Execute(); err != nil {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]any{
			"schema_version": "1",
			"error":          err.Error(),
		})
		// Usage errors (unknown flag/command, bare invocation) are caller
		// input errors — exit 5, same family as an invalid testplay.json.
		// Subcommands os.Exit with their own codes and never reach here.
		os.Exit(5)
	}
}

func isInternalCompletionInvocation(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--config" {
			// --config is the sole persistent flag that may legally precede the
			// command name. Its next argument is a value, not a command.
			i++
			continue
		}
		if strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "__complete" || arg == "__completeNoDesc"
	}
	return false
}
