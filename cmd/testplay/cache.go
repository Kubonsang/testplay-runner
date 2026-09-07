package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/contentcache"
	"github.com/spf13/cobra"
)

func newCacheCommand() *cobra.Command {
	command := &cobra.Command{Use: "cache", Short: "Inspect and collect unreferenced shared cache contents"}
	for _, action := range []string{"usage", "prune"} {
		action := action
		var root string
		var execute, dryRun bool
		leaf := &cobra.Command{Use: action, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if execute && dryRun {
				return fmt.Errorf("--execute and --dry-run are mutually exclusive")
			}
			if root == "" {
				var err error
				root, err = contentcache.DefaultRoot()
				if err != nil {
					return err
				}
			}
			result, err := contentcache.New(root).Inspect(context.Background(), action == "prune" && execute)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
		leaf.Flags().StringVar(&root, "store-root", "", "Absolute shared cache root (default: user cache directory)")
		leaf.Flags().Bool("json", false, "JSON output (always enabled)")
		if action == "prune" {
			leaf.Flags().BoolVar(&execute, "execute", false, "Delete unreferenced blobs under the selected store")
			leaf.Flags().BoolVar(&dryRun, "dry-run", false, "Report without deleting (default)")
		}
		command.AddCommand(leaf)
	}
	var execute, dryRun bool
	retire := &cobra.Command{Use: "retire-local", Args: cobra.NoArgs, Short: "Preview retiring a verified legacy cache after a successful shared-cache run", RunE: func(cmd *cobra.Command, _ []string) error {
		if execute && dryRun {
			return fmt.Errorf("--execute and --dry-run are mutually exclusive")
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		if cfg.Workspace == nil || cfg.Workspace.CacheMode != "shared-content" {
			return fmt.Errorf("shared-content must be explicitly configured first")
		}
		root := cfg.Workspace.CacheRoot
		if root == "" {
			root, err = contentcache.DefaultRoot()
			if err != nil {
				return err
			}
		}
		key, err := contentcache.Key(cfg.ProjectPath, cfg.UnityPath)
		if err != nil {
			return err
		}
		result, err := contentcache.New(root).RetireLocal(cmd.Context(), cfg.ProjectPath, key, execute)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	retire.Flags().BoolVar(&execute, "execute", false, "Remove only the verified local .testplay/cache directory")
	retire.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without deletion (default)")
	retire.Flags().Bool("json", false, "JSON output (always enabled)")
	command.AddCommand(retire)
	return command
}
func init() { rootCmd.AddCommand(newCacheCommand()) }
