package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/fastplay/runner/internal/config"
	"github.com/fastplay/runner/internal/history"
)

type resultDeps struct {
	store *history.Store
	last  int
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

	writeJSON(w, map[string]any{"runs": runs})
	return 0
}

var resultLast int

var resultCmd = &cobra.Command{
	Use:   "result",
	Short: "View stored test result history",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("fastplay.json")
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}

		store := history.NewStore(cfg.ResultDir)
		deps := resultDeps{store: store, last: resultLast}
		code := runResult(cmd.OutOrStdout(), deps)
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resultCmd)
	resultCmd.Flags().IntVar(&resultLast, "last", 0, "Show only the last N results (0 = all)")
}
