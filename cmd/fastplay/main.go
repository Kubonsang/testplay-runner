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

// configPath is set by the --config persistent flag.
// Default "fastplay.json" preserves existing behaviour when the flag is absent.
var configPath = "fastplay.json"

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "fastplay.json",
		"Path to fastplay.json (default: fastplay.json in cwd)")
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
