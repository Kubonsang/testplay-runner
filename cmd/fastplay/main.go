package main

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "fastplay",
	Short: "Unity test runner for AI agents",
}

func main() {
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	if err := rootCmd.Execute(); err != nil {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(map[string]any{
			"schema_version": "1",
			"error":          err.Error(),
		})
		os.Exit(1)
	}
}
