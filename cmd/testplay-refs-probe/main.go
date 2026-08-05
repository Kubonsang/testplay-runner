package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/Kubonsang/testplay-runner/internal/refsworkspace"
	"github.com/spf13/cobra"
)

var (
	rootPath            string
	poolFile            string
	mountRoot           string
	maximumBytes        int64
	softBudget          int64
	workerReserve       int64
	minimumHostFree     int64
	vhdxOverheadReserve int64
	recoveryKeyDigest   string
	recoveryLeaseID     string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := newRootCommand()
	command.SetContext(ctx)
	command.SilenceErrors = true
	command.SilenceUsage = true
	if err := command.Execute(); err != nil {
		writeError(err)
		os.Exit(exitCode(err))
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "testplay-refs-probe",
		Short: "Standalone Managed ReFS Library Pool architecture probe",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("one command is required: setup, status, probe, remove, recover-incomplete-setup, or recover-released-worker-residual")
		},
	}
	root.PersistentFlags().StringVar(&rootPath, "root", "", "absolute host storage root (defaults to %LOCALAPPDATA%\\TestPlay\\Storage)")
	root.PersistentFlags().StringVar(&poolFile, "pool-file", "", "absolute Dynamic VHDX path (must be a direct child of root)")
	root.PersistentFlags().StringVar(&mountRoot, "mount-root", "", "absolute private mount path (must be a direct child of root)")
	root.PersistentFlags().Int64Var(&maximumBytes, "max-bytes", 0, "dynamic VHDX guest virtual-size ceiling")
	root.PersistentFlags().Int64Var(&softBudget, "soft-budget-bytes", 0, "testplay soft allocation budget")
	root.PersistentFlags().Int64Var(&workerReserve, "worker-reserve-bytes", 0, "reservation required before each worker")
	root.PersistentFlags().Int64Var(&minimumHostFree, "minimum-host-free-bytes", 0, "minimum host free-space floor")
	root.PersistentFlags().Int64Var(&vhdxOverheadReserve, "vhdx-overhead-reserve-bytes", 0, "experimental VHDX metadata/allocation overhead reserve")
	for _, operation := range []string{"setup", "status", "probe", "remove", "recover-incomplete-setup", "recover-released-worker-residual"} {
		op := operation
		command := &cobra.Command{
			Use:   op,
			Short: op + " the standalone Managed ReFS Library Pool",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				config, err := commandConfig()
				if err != nil {
					return &refsworkspace.Error{Code: refsworkspace.CodeUnsupportedPlatform, Operation: "default-config", Cause: err}
				}
				service := refsworkspace.NewNativeService()
				var result *refsworkspace.Result
				switch op {
				case "setup":
					result, err = service.Setup(cmd.Context(), config)
				case "status":
					result, err = service.Status(cmd.Context(), config)
				case "probe":
					result, err = service.Probe(cmd.Context(), config)
				case "remove":
					result, err = service.Remove(cmd.Context(), config)
				case "recover-incomplete-setup":
					result, err = service.RecoverIncompleteSetup(cmd.Context(), config)
				case "recover-released-worker-residual":
					result, err = service.RecoverReleasedWorkerResidual(cmd.Context(), config, recoveryKeyDigest, recoveryLeaseID)
				}
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(result)
			},
		}
		if op == "recover-released-worker-residual" {
			command.Flags().StringVar(&recoveryKeyDigest, "key-digest", "", "exact canonical baseline compatibility-key digest")
			command.Flags().StringVar(&recoveryLeaseID, "lease-id", "", "exact released worker lease id")
			_ = command.MarkFlagRequired("key-digest")
			_ = command.MarkFlagRequired("lease-id")
		}
		root.AddCommand(command)
	}
	return root
}

func commandConfig() (refsworkspace.Config, error) {
	var config refsworkspace.Config
	var err error
	if rootPath == "" {
		config, err = refsworkspace.DefaultConfig()
		if err != nil {
			return config, err
		}
	} else {
		config.Root = rootPath
	}
	if poolFile != "" {
		config.VHDXPath = poolFile
	}
	if mountRoot != "" {
		config.MountRoot = mountRoot
	}
	if maximumBytes != 0 {
		config.MaximumBytes = maximumBytes
	}
	if softBudget != 0 {
		config.SoftBudgetBytes = softBudget
	}
	if workerReserve != 0 {
		config.WorkerReserveBytes = workerReserve
	}
	if minimumHostFree != 0 {
		config.MinimumHostFreeBytes = minimumHostFree
	}
	if vhdxOverheadReserve != 0 {
		config.VHDXOverheadReserveBytes = vhdxOverheadReserve
	}
	return config, nil
}

func writeError(err error) {
	_ = json.NewEncoder(os.Stdout).Encode(errorPayload(err))
}

func errorPayload(err error) map[string]any {
	operation := ""
	path := ""
	cleanupState := ""
	ownerCommitted := false
	ownedVHDX := ""
	manualRecovery := false
	mountPath := ""
	poolMetadataPath := ""
	var mountReadyTimeoutMs int64
	lastObservedError := ""
	var nativeEvidence *refsworkspace.NativeEvidence
	var setupTransaction *refsworkspace.SetupTransactionEvidence
	var probeErr *refsworkspace.Error
	if errors.As(err, &probeErr) {
		operation = probeErr.Operation
		path = probeErr.Path
		cleanupState = probeErr.CleanupState
		ownerCommitted = probeErr.OwnerMetadataCommitted
		ownedVHDX = probeErr.OwnedVHDXPath
		manualRecovery = probeErr.ManualRecoveryRequired
		nativeEvidence = probeErr.NativeEvidence
		setupTransaction = probeErr.SetupTransaction
		mountPath = probeErr.MountPath
		poolMetadataPath = probeErr.PoolMetadataPath
		mountReadyTimeoutMs = probeErr.MountReadyTimeoutMs
		lastObservedError = probeErr.LastObservedError
	}
	payload := map[string]any{
		"schemaVersion":               "2",
		"status":                      "FAILED",
		"architecture":                "Managed ReFS Library Pool",
		"windowsProvider":             refsworkspace.WindowsProviderDevDriveVHDX,
		"volumeKind":                  refsworkspace.VolumeKindDevDrive,
		"code":                        refsworkspace.ErrorCode(err),
		"operation":                   operation,
		"path":                        path,
		"message":                     err.Error(),
		"cleanupState":                cleanupState,
		"ownerMetadataCommitted":      ownerCommitted,
		"ownedVhdxPath":               ownedVHDX,
		"manualRecoveryRequired":      manualRecovery,
		"mountPath":                   mountPath,
		"poolMetadataPath":            poolMetadataPath,
		"mountReadyTimeoutMs":         mountReadyTimeoutMs,
		"lastObservedFilesystemError": lastObservedError,
		"fallbackUsed":                false,
		"physicalImageCreated":        false,
		"differencingChildCreated":    false,
		"nativeWindowsStatus":         "NOT MEASURED",
	}
	if nativeEvidence != nil {
		payload["nativeWindowsStatus"] = "PARTIALLY_MEASURED"
		payload["nativeEvidence"] = nativeEvidence
		payload["lastCompletedMilestone"] = nativeEvidence.LastCompletedMilestone
		payload["nativeMilestones"] = nativeEvidence.Milestones
		if nativeEvidence.DevDrive != nil {
			payload["devDrive"] = nativeEvidence.DevDrive
		}
		if nativeEvidence.Filesystem != nil {
			payload["filesystem"] = nativeEvidence.Filesystem
		}
		if nativeEvidence.ClusterSize != nil {
			payload["clusterSize"] = nativeEvidence.ClusterSize
		}
		if nativeEvidence.BlockCloneSupported != nil {
			payload["blockCloneSupported"] = nativeEvidence.BlockCloneSupported
		}
		if nativeEvidence.RegularBlockCloneIOCTLAttempted != nil {
			payload["regularBlockCloneIOCTLAttempted"] = nativeEvidence.RegularBlockCloneIOCTLAttempted
		}
		if nativeEvidence.SparseBlockCloneIOCTLAttempted != nil {
			payload["sparseBlockCloneIOCTLAttempted"] = nativeEvidence.SparseBlockCloneIOCTLAttempted
		}
	}
	if setupTransaction != nil {
		payload["setupTransaction"] = setupTransaction
	}
	return payload
}

func exitCode(err error) int {
	switch refsworkspace.ErrorCode(err) {
	case refsworkspace.CodeUnsupportedPlatform, refsworkspace.CodeReFSFormatUnavailable, refsworkspace.CodeBlockCloneUnavailable,
		refsworkspace.CodeDevDriveUnavailable, refsworkspace.CodeDevDriveDisabled, refsworkspace.CodeDevDriveFormatFailed,
		refsworkspace.CodeDevDriveVerificationFailed, refsworkspace.CodeTemporaryDriveLetterUnavailable,
		refsworkspace.CodeTemporaryDriveLetterCleanupFailed:
		return 6
	case refsworkspace.CodeNotElevated:
		return 7
	case refsworkspace.CodeCancelled:
		return 8
	case refsworkspace.CodeInvalidConfiguration:
		return 5
	default:
		return 1
	}
}
