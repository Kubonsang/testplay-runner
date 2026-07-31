//go:build windows

package vhdxprobe

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

// Run executes one isolated differencing-VHDX probe.
func Run(ctx context.Context, config Config) (result *Result, returnErr error) {
	if err := cancellationError(ctx, "start", config.Root); err != nil {
		return nil, err
	}
	if err := vhdxstorage.EnsureAvailable(); err != nil {
		return nil, err
	}
	elevated, err := vhdxstorage.IsElevated(ctx)
	if err != nil {
		return nil, probeError(CodeNotElevated, "check-administrator", config.Root, err)
	}
	if !elevated {
		return nil, probeError(
			CodeNotElevated,
			"check-administrator",
			config.Root,
			fmt.Errorf("run the integration probe from an elevated PowerShell"),
		)
	}
	plan, err := NewPlan(config, "")
	if err != nil {
		return nil, err
	}
	result = &Result{
		OperationID: filepath.Base(plan.Paths.Operation)[len("testplay-vhdx-probe-"):],
		Paths:       plan.Paths,
		Metrics: StorageMetrics{
			ParentVirtualBytes:  plan.Config.ParentVirtualBytes,
			LogicalPayloadBytes: plan.Config.PayloadBytes,
		},
	}
	safeToDelete := true
	closeAfterFailure := func(disk *vhdxstorage.Attachment, primary error) error {
		if closeErr := disk.Close(ctx); closeErr != nil {
			safeToDelete = false
			return errors.Join(primary, closeErr)
		}
		return primary
	}
	rootCreated := false
	if _, statErr := os.Stat(plan.Paths.Root); os.IsNotExist(statErr) {
		if err := os.Mkdir(plan.Paths.Root, 0700); err != nil {
			return nil, probeError(CodeInvalidProbeRoot, "create-root", plan.Paths.Root, err)
		}
		rootCreated = true
	}
	if err := os.Mkdir(plan.Paths.Operation, 0700); err != nil {
		return nil, probeError(CodeParentExists, "create-operation-directory", plan.Paths.Operation, err)
	}
	if err := os.Mkdir(plan.Paths.Mounts, 0700); err != nil {
		return nil, probeError(CodeInvalidProbeRoot, "create-mount-directory", plan.Paths.Mounts, err)
	}
	defer func() {
		cleanupStarted := time.Now()
		if safeToDelete {
			if err := validateCleanupTarget(plan); err == nil {
				err = os.RemoveAll(plan.Paths.Operation)
				if err == nil {
					result.CleanupPassed = true
				}
				if returnErr == nil && err != nil {
					returnErr = probeError(CodeCleanupFailed, "remove-operation-directory", plan.Paths.Operation, err)
				}
			} else if returnErr == nil {
				returnErr = err
			}
		}
		if rootCreated && result.CleanupPassed {
			_ = os.Remove(plan.Paths.Root)
		}
		result.Durations.CleanupMs = time.Since(cleanupStarted).Milliseconds()
	}()

	if err := cancellationError(ctx, "parent-create", plan.Paths.Parent); err != nil {
		return result, err
	}
	started := time.Now()
	if err := vhdxstorage.CreateDynamic(plan.Paths.Parent, plan.Config.ParentVirtualBytes); err != nil {
		return result, err
	}
	result.Durations.ParentCreateMs = time.Since(started).Milliseconds()

	parentMount := filepath.Join(plan.Paths.Mounts, "parent")
	if err := os.Mkdir(parentMount, 0700); err != nil {
		return result, probeError(CodeMountResolutionFailed, "create-parent-mount", parentMount, err)
	}
	if err := cancellationError(ctx, "parent-attach", plan.Paths.Parent); err != nil {
		return result, err
	}
	started = time.Now()
	parent, err := vhdxstorage.OpenAndAttach(plan.Paths.Parent, false)
	if err != nil {
		return result, probeError(CodeParentAttachFailed, "attach-parent", plan.Paths.Parent, err)
	}
	result.ParentPhysicalPath = parent.PhysicalPath()
	result.Durations.ParentAttachMs = time.Since(started).Milliseconds()
	started = time.Now()
	if err := parent.InitializeAndMount(ctx, parentMount); err != nil {
		if closeErr := parent.Close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, probeError(CodeParentInitializeFailed, "initialize-parent", plan.Paths.Parent, err)
	}
	result.Durations.ParentInitializeMs = time.Since(started).Milliseconds()
	if err := cancellationError(ctx, "parent-seed", plan.Paths.Parent); err != nil {
		if closeErr := parent.Close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, err
	}
	started = time.Now()
	baselineHash, err := seedParent(parentMount, plan.Config.PayloadBytes)
	if err != nil {
		primary := probeError(CodeParentSeedFailed, "seed-parent", parentMount, err)
		return result, closeAfterFailure(parent, primary)
	}
	result.BaselinePayloadHash = baselineHash
	result.Durations.ParentSeedMs = time.Since(started).Milliseconds()
	sizeInfo, err := parent.Size()
	if err != nil {
		return result, closeAfterFailure(parent, err)
	}
	result.Metrics.ParentVirtualBytes = int64(sizeInfo.VirtualSize)
	started = time.Now()
	if err := parent.Close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.Durations.ParentDetachMs = time.Since(started).Milliseconds()
	result.ParentHashBefore, err = hashFile(plan.Paths.Parent)
	if err != nil {
		return result, probeError(CodeVerificationFailed, "hash-parent-before", plan.Paths.Parent, err)
	}
	result.Metrics.ParentFileBytes, err = regularFileSize(plan.Paths.Parent)
	if err != nil {
		return result, err
	}

	if err := cancellationError(ctx, "child-create", plan.Paths.ChildA); err != nil {
		return result, err
	}
	started = time.Now()
	for _, childPath := range []string{plan.Paths.ChildA, plan.Paths.ChildB} {
		if err := vhdxstorage.CreateDifferencing(childPath, plan.Paths.Parent); err != nil {
			return result, err
		}
		childHandle, openErr := vhdxstorage.Open(childPath, false)
		if openErr != nil {
			return result, openErr
		}
		verifyErr := childHandle.VerifyParent(plan.Paths.Parent)
		closeErr := childHandle.CloseHandle()
		if verifyErr != nil || closeErr != nil {
			return result, errors.Join(verifyErr, closeErr)
		}
	}
	result.Durations.ChildCreateMs = time.Since(started).Milliseconds()
	result.Metrics.ChildInitialFileBytes, err = regularFileSize(plan.Paths.ChildA)
	if err != nil {
		return result, err
	}

	childAMount := filepath.Join(plan.Paths.Mounts, "child-a")
	if err := os.Mkdir(childAMount, 0700); err != nil {
		return result, err
	}
	if err := cancellationError(ctx, "child-a-attach", plan.Paths.ChildA); err != nil {
		return result, err
	}
	started = time.Now()
	childA, err := vhdxstorage.OpenAndAttach(plan.Paths.ChildA, false)
	if err != nil {
		return result, err
	}
	result.ChildAPhysicalPath = childA.PhysicalPath()
	mountStarted := time.Now()
	if err := childA.MountExisting(ctx, childAMount, false); err != nil {
		if closeErr := childA.Close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, err
	}
	result.Durations.MountResolveMs = time.Since(mountStarted).Milliseconds()
	result.Durations.ChildAttachMs = time.Since(started).Milliseconds()
	result.Metrics.ChildAfterAttachFileBytes, _ = regularFileSize(plan.Paths.ChildA)
	if err := verifyPayloadHash(childAMount, baselineHash); err != nil {
		return result, closeAfterFailure(childA, err)
	}
	if err := cancellationError(ctx, "child-a-mutation", plan.Paths.ChildA); err != nil {
		return result, closeAfterFailure(childA, err)
	}
	started = time.Now()
	childAHash, err := mutateChildA(childAMount, plan.Config.PayloadBytes)
	if err != nil {
		primary := probeError(CodeVerificationFailed, "mutate-child-a", childAMount, err)
		return result, closeAfterFailure(childA, primary)
	}
	result.Durations.MutationMs = time.Since(started).Milliseconds()
	started = time.Now()
	if err := childA.Close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.Durations.ChildDetachMs = time.Since(started).Milliseconds()
	result.Metrics.ChildAfterMutationFileBytes, _ = regularFileSize(plan.Paths.ChildA)
	if err := verifyParentFileHash(plan.Paths.Parent, result.ParentHashBefore); err != nil {
		return result, err
	}

	childBMount := filepath.Join(plan.Paths.Mounts, "child-b")
	if err := os.Mkdir(childBMount, 0700); err != nil {
		return result, err
	}
	childB, err := vhdxstorage.OpenAndAttach(plan.Paths.ChildB, false)
	if err != nil {
		return result, err
	}
	result.ChildBPhysicalPath = childB.PhysicalPath()
	if err := childB.MountExisting(ctx, childBMount, false); err != nil {
		if closeErr := childB.Close(ctx); closeErr != nil {
			safeToDelete = false
			return result, errors.Join(err, closeErr)
		}
		return result, err
	}
	if err := verifyChildBIsBaseline(childBMount, baselineHash); err != nil {
		return result, closeAfterFailure(childB, err)
	}
	childBHash, err := mutateChildB(childBMount, plan.Config.PayloadBytes)
	if err != nil {
		return result, closeAfterFailure(childB, err)
	}
	if err := childB.Close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}

	childAReattachMount := filepath.Join(plan.Paths.Mounts, "child-a-reattach")
	if err := os.Mkdir(childAReattachMount, 0700); err != nil {
		return result, err
	}
	childAReattach, err := vhdxstorage.OpenAndAttach(plan.Paths.ChildA, false)
	if err != nil {
		return result, err
	}
	if err := childAReattach.MountExisting(ctx, childAReattachMount, false); err != nil {
		return result, closeAfterFailure(childAReattach, err)
	}
	if err := verifyChildAReattach(childAReattachMount, childAHash); err != nil {
		return result, closeAfterFailure(childAReattach, err)
	}
	if err := childAReattach.Close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.Metrics.ChildAfterReattachFileBytes, _ = regularFileSize(plan.Paths.ChildA)
	result.ReattachPersistencePassed = true

	childBVerifyMount := filepath.Join(plan.Paths.Mounts, "child-b-verify")
	if err := os.Mkdir(childBVerifyMount, 0700); err != nil {
		return result, err
	}
	childBVerify, err := vhdxstorage.OpenAndAttach(plan.Paths.ChildB, true)
	if err != nil {
		return result, err
	}
	if err := childBVerify.MountExisting(ctx, childBVerifyMount, true); err != nil {
		return result, closeAfterFailure(childBVerify, err)
	}
	if err := verifyChildBMutation(childBVerifyMount, childBHash); err != nil {
		return result, closeAfterFailure(childBVerify, err)
	}
	if err := childBVerify.Close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.SiblingIsolationPassed = true

	parentVerifyMount := filepath.Join(plan.Paths.Mounts, "parent-verify")
	if err := os.Mkdir(parentVerifyMount, 0700); err != nil {
		return result, err
	}
	parentVerify, err := vhdxstorage.OpenAndAttach(plan.Paths.Parent, true)
	if err != nil {
		return result, err
	}
	if err := parentVerify.MountExisting(ctx, parentVerifyMount, true); err != nil {
		return result, closeAfterFailure(parentVerify, err)
	}
	if err := verifyParentBaseline(parentVerifyMount, baselineHash); err != nil {
		return result, closeAfterFailure(parentVerify, err)
	}
	if err := parentVerify.Close(ctx); err != nil {
		safeToDelete = false
		return result, err
	}
	result.ParentHashAfter, err = hashFile(plan.Paths.Parent)
	if err != nil {
		return result, err
	}
	if result.ParentHashAfter != result.ParentHashBefore {
		return result, probeError(
			CodeParentMutated,
			"verify-parent-file",
			plan.Paths.Parent,
			fmt.Errorf("parent VHDX hash changed"),
		)
	}
	result.ParentIsolationPassed = true
	return result, nil
}

func cancellationError(ctx context.Context, operation, path string) error {
	if err := ctx.Err(); err != nil {
		return probeError(CodeCancelled, operation, path, err)
	}
	return nil
}

func seedParent(mountPath string, payloadBytes int64) (string, error) {
	baseline := filepath.Join(mountPath, "baseline")
	if err := os.Mkdir(baseline, 0700); err != nil {
		return "", err
	}
	payloadPath := filepath.Join(baseline, "payload.bin")
	file, err := os.OpenFile(payloadPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	const blockBytes = 1 << 20
	block := make([]byte, blockBytes)
	var written int64
	for index := uint64(0); written < payloadBytes; index++ {
		fillDeterministicBlock(block, index, 0x41)
		toWrite := int64(len(block))
		if remaining := payloadBytes - written; remaining < toWrite {
			toWrite = remaining
		}
		if _, err := writer.Write(block[:toWrite]); err != nil {
			_ = file.Close()
			return "", err
		}
		written += toWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	manifest, err := json.MarshalIndent(map[string]any{
		"payloadBytes": payloadBytes,
		"sha256":       digest,
		"pattern":      "block-index-v1",
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(baseline, "manifest.json"), manifest, 0600); err != nil {
		return "", err
	}
	return digest, nil
}

func fillDeterministicBlock(block []byte, index uint64, salt byte) {
	for position := range block {
		block[position] = byte((uint64(position)*31 + index*17 + uint64(salt)) % 251)
	}
	binary.LittleEndian.PutUint64(block[:8], index)
}

func mutateChildA(mountPath string, payloadBytes int64) (string, error) {
	payload := filepath.Join(mountPath, "baseline", "payload.bin")
	if err := overwritePayloadRegion(payload, payloadBytes/2, 0xa1); err != nil {
		return "", err
	}
	baseline := filepath.Join(mountPath, "baseline")
	if err := os.Rename(
		filepath.Join(baseline, "manifest.json"),
		filepath.Join(baseline, "manifest-child-a.json"),
	); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(baseline, "child-a-marker.txt"), []byte("child-a\n"), 0600); err != nil {
		return "", err
	}
	for index := 0; index < 4; index++ {
		path := filepath.Join(baseline, fmt.Sprintf("child-a-small-%d.txt", index))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("child-a-%d\n", index)), 0600); err != nil {
			return "", err
		}
	}
	return hashFile(payload)
}

func mutateChildB(mountPath string, payloadBytes int64) (string, error) {
	payload := filepath.Join(mountPath, "baseline", "payload.bin")
	if err := overwritePayloadRegion(payload, payloadBytes/4, 0xb2); err != nil {
		return "", err
	}
	if err := os.WriteFile(
		filepath.Join(mountPath, "baseline", "child-b-marker.txt"),
		[]byte("child-b\n"),
		0600,
	); err != nil {
		return "", err
	}
	return hashFile(payload)
}

func overwritePayloadRegion(path string, offset int64, salt byte) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	const mutationBytes = 1 << 20
	offset -= offset % mutationBytes
	block := make([]byte, mutationBytes)
	fillDeterministicBlock(block, uint64(offset/mutationBytes), salt)
	if _, err := file.WriteAt(block, offset); err != nil {
		return err
	}
	return file.Sync()
}

func verifyPayloadHash(mountPath, expected string) error {
	actual, err := hashFile(filepath.Join(mountPath, "baseline", "payload.bin"))
	if err != nil {
		return probeError(CodeVerificationFailed, "hash-payload", mountPath, err)
	}
	if actual != expected {
		return probeError(
			CodeVerificationFailed,
			"verify-payload",
			mountPath,
			fmt.Errorf("hash %s does not match baseline %s", actual, expected),
		)
	}
	return nil
}

func verifyChildBIsBaseline(mountPath, baselineHash string) error {
	if err := verifyPayloadHash(mountPath, baselineHash); err != nil {
		return probeError(CodeSiblingIsolationFailed, "verify-child-b-baseline", mountPath, err)
	}
	for _, name := range []string{"manifest-child-a.json", "child-a-marker.txt"} {
		if _, err := os.Stat(filepath.Join(mountPath, "baseline", name)); !os.IsNotExist(err) {
			return probeError(
				CodeSiblingIsolationFailed,
				"verify-child-a-marker-absent",
				filepath.Join(mountPath, "baseline", name),
				fmt.Errorf("Child A mutation leaked into Child B"),
			)
		}
	}
	return nil
}

func verifyChildAReattach(mountPath, expectedHash string) error {
	actual, err := hashFile(filepath.Join(mountPath, "baseline", "payload.bin"))
	if err != nil || actual != expectedHash {
		return probeError(
			CodeReattachPersistenceFailed,
			"verify-child-a-payload",
			mountPath,
			fmt.Errorf("payload hash=%q expected=%q: %w", actual, expectedHash, err),
		)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-a-marker.txt")); err != nil {
		return probeError(CodeReattachPersistenceFailed, "verify-child-a-marker", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-b-marker.txt")); !os.IsNotExist(err) {
		return probeError(
			CodeSiblingIsolationFailed,
			"verify-child-b-marker-absent",
			mountPath,
			fmt.Errorf("Child B mutation leaked into Child A"),
		)
	}
	return nil
}

func verifyChildBMutation(mountPath, expectedHash string) error {
	actual, err := hashFile(filepath.Join(mountPath, "baseline", "payload.bin"))
	if err != nil || actual != expectedHash {
		return probeError(
			CodeSiblingIsolationFailed,
			"verify-child-b-payload",
			mountPath,
			fmt.Errorf("payload hash=%q expected=%q: %w", actual, expectedHash, err),
		)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-b-marker.txt")); err != nil {
		return probeError(CodeSiblingIsolationFailed, "verify-child-b-marker", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "child-a-marker.txt")); !os.IsNotExist(err) {
		return probeError(
			CodeSiblingIsolationFailed,
			"verify-child-a-marker-absent",
			mountPath,
			fmt.Errorf("Child A mutation leaked into Child B"),
		)
	}
	return nil
}

func verifyParentBaseline(mountPath, baselineHash string) error {
	if err := verifyPayloadHash(mountPath, baselineHash); err != nil {
		return probeError(CodeParentMutated, "verify-parent-payload", mountPath, err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "baseline", "manifest.json")); err != nil {
		return probeError(CodeParentMutated, "verify-parent-manifest", mountPath, err)
	}
	for _, name := range []string{
		"manifest-child-a.json",
		"child-a-marker.txt",
		"child-b-marker.txt",
	} {
		if _, err := os.Stat(filepath.Join(mountPath, "baseline", name)); !os.IsNotExist(err) {
			return probeError(
				CodeParentMutated,
				"verify-child-marker-absent",
				filepath.Join(mountPath, "baseline", name),
				fmt.Errorf("child mutation leaked into Parent"),
			)
		}
	}
	return nil
}

func verifyParentFileHash(path, expected string) error {
	actual, err := hashFile(path)
	if err != nil {
		return probeError(CodeVerificationFailed, "hash-parent", path, err)
	}
	if actual != expected {
		return probeError(
			CodeParentMutated,
			"verify-parent-file",
			path,
			fmt.Errorf("hash %s does not match %s", actual, expected),
		)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func regularFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", path)
	}
	return info.Size(), nil
}

func boolDigit(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
