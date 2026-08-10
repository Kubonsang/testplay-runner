package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/vhdxworkspace"
	"github.com/spf13/cobra"
)

var (
	storageRoot         string
	storageJSON         bool
	storageDryRun       bool
	storagePreserveData bool
	brokerConfigPath    string
	brokerConsole       bool
)

var storageCmd = &cobra.Command{Use: "storage", Short: "Manage the privileged VHDX workspace broker"}

var storageInstallCmd = &cobra.Command{Use: "install", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	result, err := platformInstallStorage(storageRoot)
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), result)
	return nil
}}

var storageStatusCmd = &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationStatus, false, "", "")
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), response.Status)
	return nil
}}

var storageGCCmd = &cobra.Command{Use: "gc", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationGC, storageDryRun, "", "")
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), map[string]any{"status": "PASS", "dryRun": storageDryRun, "metrics": response.Metrics})
	return nil
}}

var storageUpgradeCmd = &cobra.Command{Use: "upgrade", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	result, err := platformUpgradeStorage()
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), result)
	return nil
}}

var storageUninstallCmd = &cobra.Command{Use: "uninstall", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	result, err := platformUninstallStorage(storagePreserveData)
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), result)
	return nil
}}

var brokerRunCmd = &cobra.Command{Use: "broker-run", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	if brokerConsole {
		return vhdxworkspace.RunBrokerConsole(cmd.Context(), brokerConfigPath)
	}
	return vhdxworkspace.RunWindowsService(brokerConfigPath)
}}

var workspaceCmd = &cobra.Command{Use: "workspace", Short: "Manage retained VHDX workspaces"}

var workspaceAttachCmd = &cobra.Command{Use: "attach <run-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationAttachRetained, false, args[0], args[0])
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), response)
	return nil
}}

var workspaceRemoveCmd = &cobra.Command{Use: "remove <run-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationRemoveRetained, false, args[0], "")
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), response)
	return nil
}}

func callStorageBroker(ctx context.Context, operation string, dryRun bool, runID, workspaceID string) (vhdxworkspace.Response, error) {
	requestID := "cli-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	request := vhdxworkspace.NewRequest(operation, requestID)
	request.DryRun, request.RunID, request.WorkspaceID = dryRun, runID, workspaceID
	if sid, err := vhdxworkspace.CurrentUserSID(); err == nil {
		request.UserSID = sid
	}
	return vhdxworkspace.DefaultClient().Call(ctx, request)
}

func requireAbsoluteOptionalRoot(root string) (string, error) {
	if root == "" {
		return "", nil
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("--root must be absolute")
	}
	return filepath.Clean(root), nil
}

func init() {
	rootCmd.AddCommand(storageCmd, workspaceCmd)
	storageCmd.AddCommand(storageInstallCmd, storageStatusCmd, storageGCCmd, storageUpgradeCmd, storageUninstallCmd, brokerRunCmd)
	workspaceCmd.AddCommand(workspaceAttachCmd, workspaceRemoveCmd)
	storageInstallCmd.Flags().StringVar(&storageRoot, "root", "", "Absolute VHDX store root")
	storageStatusCmd.Flags().BoolVar(&storageJSON, "json", false, "Emit JSON (the CLI always emits JSON)")
	storageGCCmd.Flags().BoolVar(&storageDryRun, "dry-run", false, "Report reclaimable parents without deleting them")
	storageUninstallCmd.Flags().BoolVar(&storagePreserveData, "preserve-data", false, "Remove the broker service but preserve store data")
	brokerRunCmd.Flags().StringVar(&brokerConfigPath, "service-config", "", "Broker service config path")
	brokerRunCmd.Flags().BoolVar(&brokerConsole, "console", false, "Run broker in the current console")
	_ = storageJSON
}
