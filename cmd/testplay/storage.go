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
	storageRoot              string
	storageJSON              bool
	storageDryRun            bool
	storagePreserveData      bool
	brokerConfigPath         string
	brokerConsole            bool
	brokerProbeOperation     string
	brokerProbeClaimedSID    string
	brokerProbeWorkspaceID   string
	brokerProbeWorkspaceRoot string
)

var storageCmd = &cobra.Command{Use: "storage", Short: "Manage the privileged VHDX workspace broker"}

var storageInstallCmd = &cobra.Command{Use: "install", Short: "Install the Windows VHDX broker for the current user", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	result, err := platformInstallStorage(storageRoot)
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), result)
	return nil
}}

var storageStatusCmd = &cobra.Command{Use: "status", Short: "Report broker capacity and protected workspace state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationStatus, false, "", "")
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), response.Status)
	return nil
}}

var storageGCCmd = &cobra.Command{Use: "gc", Short: "Collect expired unprotected parent VHDXs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationGC, storageDryRun, "", "")
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), map[string]any{"status": "PASS", "dryRun": storageDryRun, "metrics": response.Metrics})
	return nil
}}

var storageUpgradeCmd = &cobra.Command{Use: "upgrade", Short: "Upgrade the installed broker executable", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	result, err := platformUpgradeStorage()
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), result)
	return nil
}}

var storageUninstallCmd = &cobra.Command{Use: "uninstall", Short: "Uninstall the broker with ownership-safe data handling", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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

var brokerProbeCmd = &cobra.Command{Use: "broker-probe", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
	requestID := "security-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	request := vhdxworkspace.NewRequest(brokerProbeOperation, requestID)
	request.UserSID = brokerProbeClaimedSID
	request.WorkspaceID = brokerProbeWorkspaceID
	request.WorkspaceRoot = brokerProbeWorkspaceRoot
	callerSID, sidErr := vhdxworkspace.CurrentUserSID()
	response, callErr := vhdxworkspace.DefaultClient().Call(cmd.Context(), request)
	status := "PASS"
	if callErr != nil {
		status = "REJECTED"
	}
	result := map[string]any{
		"schemaVersion":         1,
		"status":                status,
		"callerSid":             callerSID,
		"operation":             brokerProbeOperation,
		"claimedUserSid":        brokerProbeClaimedSID,
		"workspaceId":           brokerProbeWorkspaceID,
		"workspaceRoot":         brokerProbeWorkspaceRoot,
		"response":              response,
		"transportAccessDenied": brokerProbeAccessDenied(callErr),
	}
	if sidErr != nil {
		result["callerSidError"] = sidErr.Error()
	}
	if callErr != nil {
		result["callError"] = callErr.Error()
	}
	writeJSON(cmd.OutOrStdout(), result)
	return nil
}}

var workspaceCmd = &cobra.Command{Use: "workspace", Short: "Manage retained VHDX workspaces"}

var workspaceAttachCmd = &cobra.Command{Use: "attach <run-id>", Short: "Attach an exact retained VHDX workspace", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	response, err := callStorageBroker(cmd.Context(), vhdxworkspace.OperationAttachRetained, false, args[0], args[0])
	if err != nil {
		return err
	}
	writeJSON(cmd.OutOrStdout(), response)
	return nil
}}

var workspaceRemoveCmd = &cobra.Command{Use: "remove <run-id>", Short: "Remove an exact retained VHDX workspace", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
	storageCmd.AddCommand(storageInstallCmd, storageStatusCmd, storageGCCmd, storageUpgradeCmd, storageUninstallCmd, brokerRunCmd, brokerProbeCmd)
	workspaceCmd.AddCommand(workspaceAttachCmd, workspaceRemoveCmd)
	storageInstallCmd.Flags().StringVar(&storageRoot, "root", "", "Absolute VHDX store root")
	storageStatusCmd.Flags().BoolVar(&storageJSON, "json", false, "Emit JSON (the CLI always emits JSON)")
	storageGCCmd.Flags().BoolVar(&storageDryRun, "dry-run", false, "Report reclaimable parents without deleting them")
	storageUninstallCmd.Flags().BoolVar(&storagePreserveData, "preserve-data", false, "Remove the broker service but preserve store data")
	brokerRunCmd.Flags().StringVar(&brokerConfigPath, "service-config", "", "Broker service config path")
	brokerRunCmd.Flags().BoolVar(&brokerConsole, "console", false, "Run broker in the current console")
	brokerProbeCmd.Flags().StringVar(&brokerProbeOperation, "operation", vhdxworkspace.OperationHello, "Raw broker operation")
	brokerProbeCmd.Flags().StringVar(&brokerProbeClaimedSID, "claimed-user-sid", "", "Optional user SID asserted in the request")
	brokerProbeCmd.Flags().StringVar(&brokerProbeWorkspaceID, "workspace-id", "", "Optional workspace identifier")
	brokerProbeCmd.Flags().StringVar(&brokerProbeWorkspaceRoot, "workspace-root", "", "Optional client-selected workspace root")
	_ = storageJSON
}
