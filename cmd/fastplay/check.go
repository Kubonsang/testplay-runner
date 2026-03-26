package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

type checkDeps struct {
	loadConfig func(string) (*config.Config, error)
	fileExists func(string) bool
	configPath string
}

func runCheck(w io.Writer, deps checkDeps) int {
	cfg, err := deps.loadConfig(deps.configPath)
	if err != nil {
		writeJSON(w, map[string]any{
			"ready": false,
			"error": err.Error(),
		})
		return 5
	}

	// Validate config (fills defaults, checks unity path)
	if err := cfg.Validate(true); err != nil {
		writeJSON(w, map[string]any{
			"ready": false,
			"error": err.Error(),
		})
		return 5
	}

	// Check Unity binary exists
	if !deps.fileExists(cfg.UnityPath) {
		writeJSON(w, map[string]any{
			"ready": false,
			"error": fmt.Sprintf("Unity binary not found: %s", cfg.UnityPath),
			"hint":  "Ensure unity_path in fastplay.json points to a valid Unity binary",
		})
		return 1
	}

	// Check project directory exists
	if !deps.fileExists(cfg.ProjectPath) {
		writeJSON(w, map[string]any{
			"ready": false,
			"error": fmt.Sprintf("Project directory not found: %s", cfg.ProjectPath),
			"hint":  "Ensure project_path in fastplay.json points to a valid Unity project",
		})
		return 1
	}

	writeJSON(w, map[string]any{
		"ready":        true,
		"unity_path":   cfg.UnityPath,
		"project_path": cfg.ProjectPath,
	})
	return 0
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate Unity path, project path, and fastplay.json",
	RunE: func(cmd *cobra.Command, args []string) error {
		deps := checkDeps{
			loadConfig: config.Load,
			fileExists: func(path string) bool {
				_, err := os.Stat(path)
				return err == nil
			},
			configPath: "fastplay.json",
		}
		code := runCheck(cmd.OutOrStdout(), deps)
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(checkCmd)
}
