package main

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
)

type resultDeps struct {
	store   *history.Store
	last    int
	warning string
}

func runResult(w io.Writer, deps resultDeps) int {
	runs, err := deps.store.List(deps.last)
	if err != nil {
		writeJSON(w, map[string]any{
			"error": err.Error(),
			"runs":  make([]*history.RunResult, 0),
		})
		return 5
	}

	if runs == nil {
		runs = make([]*history.RunResult, 0)
	}

	out := map[string]any{"runs": runs}
	if deps.warning != "" {
		out["warning"] = deps.warning
	}
	writeJSON(w, out)
	return 0
}

var resultLast int

var resultCmd = &cobra.Command{
	Use:   "result",
	Short: "View stored test result history",
	RunE: func(cmd *cobra.Command, args []string) error {
		resultDir := ".fastplay/results"
		var warn string
		cfg, err := config.Load(configPath)
		if err == nil {
			if valErr := cfg.Validate(false); valErr == nil && cfg.ResultDir != "" {
				resultDir = cfg.ResultDir
			} else if valErr != nil {
				warn = "fastplay.json validation failed, using default result_dir: " + valErr.Error()
			}
		} else if !errors.Is(err, config.ErrConfigNotFound) {
			// fastplay.json exists but is malformed
			warn = "fastplay.json load failed, using default result_dir: " + err.Error()
		}
		store := history.NewStore(resultDir)
		deps := resultDeps{store: store, last: resultLast, warning: warn}
		code := runResult(cmd.OutOrStdout(), deps)
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resultCmd)
	resultCmd.Flags().IntVar(&resultLast, "last", 0, "Show only the last N results (0 = all)")
}
