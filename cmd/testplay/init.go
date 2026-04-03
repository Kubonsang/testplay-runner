package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

type initDeps struct {
	unityPath    string
	projectDir   string
	testPlatform string
	force        bool
	outputPath   string
	fileExists   func(string) bool
	envLookup    func(string) string // nil → os.Getenv
}

func runInit(w io.Writer, deps initDeps) int {
	// Check if file already exists
	if deps.fileExists(deps.outputPath) && !deps.force {
		writeJSON(w, map[string]any{
			"error": fmt.Sprintf("%s already exists; use --force to overwrite", deps.outputPath),
		})
		return 5
	}

	// Resolve Unity path: flag → env var → empty
	unityPath := deps.unityPath
	if unityPath == "" {
		lookup := deps.envLookup
		if lookup == nil {
			lookup = os.Getenv
		}
		unityPath = lookup("UNITY_PATH")
	}

	// Default and validate test platform
	testPlatform := deps.testPlatform
	if testPlatform == "" {
		testPlatform = "edit_mode"
	}
	switch testPlatform {
	case "edit_mode", "play_mode":
		// valid
	default:
		writeJSON(w, map[string]any{
			"error": fmt.Sprintf("invalid test_platform %q: must be \"edit_mode\" or \"play_mode\"", testPlatform),
		})
		return 5
	}

	cfg := map[string]any{
		"schema_version": "1",
		"unity_path":     unityPath,
		"project_path":   deps.projectDir,
		"test_platform":  testPlatform,
		"timeout": map[string]any{
			"total_ms": 300000,
		},
		"result_dir": ".testplay/results",
		"retention": map[string]any{
			"max_runs": 30,
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return 1
	}

	if err := os.WriteFile(deps.outputPath, data, 0644); err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return 1
	}

	output := map[string]any{
		"created":      deps.outputPath,
		"unity_path":   unityPath,
		"project_path": deps.projectDir,
	}
	if unityPath == "" {
		fmt.Fprintln(os.Stderr, "warning: unity_path is empty — set it in testplay.json or export UNITY_PATH")
		output["warnings"] = []string{"unity_path is empty — set it in testplay.json or export UNITY_PATH"}
	}

	writeJSON(w, output)
	return 0
}

// Flag variables for init command
var initForce bool
var initUnityPath string
var initTestPlatform string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a testplay.json configuration file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectDir, err := os.Getwd()
		if err != nil {
			writeJSON(cmd.OutOrStdout(), map[string]any{"error": err.Error()})
			os.Exit(1)
		}

		deps := initDeps{
			unityPath:    initUnityPath,
			projectDir:   projectDir,
			testPlatform: initTestPlatform,
			force:        initForce,
			outputPath:   configPath, // uses --config flag (default: testplay.json)
			fileExists: func(path string) bool {
				_, err := os.Stat(path)
				return err == nil
			},
		}
		code := runInit(cmd.OutOrStdout(), deps)
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing testplay.json")
	initCmd.Flags().StringVar(&initUnityPath, "unity-path", "", "Unity binary path (falls back to UNITY_PATH env var)")
	initCmd.Flags().StringVar(&initTestPlatform, "test-platform", "", "Test platform: edit_mode (default) or play_mode")
}
