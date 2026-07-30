package vhdxprobe

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var operationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)

// Plan describes paths without creating or deleting anything.
type Plan struct {
	Config Config
	Paths  Paths
}

// NewPlan validates the requested root and constructs collision-free paths.
func NewPlan(config Config, operationID string) (Plan, error) {
	root, err := validateProbeRoot(config.Root)
	if err != nil {
		return Plan{}, err
	}
	if operationID == "" {
		operationID, err = newOperationID()
		if err != nil {
			return Plan{}, probeError(CodeInvalidProbeRoot, "operation-id", "", err)
		}
	}
	if !operationIDPattern.MatchString(operationID) {
		return Plan{}, probeError(
			CodeInvalidProbeRoot,
			"operation-id",
			operationID,
			fmt.Errorf("must be a Windows-safe lowercase identifier"),
		)
	}
	if config.ParentVirtualBytes == 0 {
		config.ParentVirtualBytes = DefaultParentVirtualBytes
	}
	if config.PayloadBytes == 0 {
		config.PayloadBytes = DefaultPayloadBytes
	}
	if config.ParentVirtualBytes < 128<<20 ||
		config.ParentVirtualBytes%512 != 0 ||
		config.PayloadBytes <= 0 ||
		config.PayloadBytes >= config.ParentVirtualBytes/2 {
		return Plan{}, probeError(
			CodeInvalidProbeRoot,
			"validate-sizes",
			root,
			fmt.Errorf("invalid parent or payload size"),
		)
	}
	config.Root = root
	operation := filepath.Join(root, "testplay-vhdx-probe-"+operationID)
	mounts := filepath.Join(operation, "mounts")
	return Plan{
		Config: config,
		Paths: Paths{
			Root:      root,
			Operation: operation,
			Parent:    filepath.Join(operation, "parent.vhdx"),
			ChildA:    filepath.Join(operation, "child-a.vhdx"),
			ChildB:    filepath.Join(operation, "child-b.vhdx"),
			Mounts:    mounts,
		},
	}, nil
}

func validateProbeRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", probeError(
			CodeInvalidProbeRoot,
			"validate-root",
			root,
			fmt.Errorf("probe root must be absolute"),
		)
	}
	clean := filepath.Clean(root)
	volume := filepath.VolumeName(clean)
	if clean == string(os.PathSeparator) ||
		(volume != "" && strings.EqualFold(clean, volume+string(os.PathSeparator))) {
		return "", probeError(
			CodeInvalidProbeRoot,
			"validate-root",
			clean,
			fmt.Errorf("drive root is forbidden"),
		)
	}
	parent := filepath.Dir(clean)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return "", probeError(
			CodeInvalidProbeRoot,
			"validate-root-parent",
			parent,
			fmt.Errorf("parent directory must already exist"),
		)
	}
	if info, err := os.Lstat(clean); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", probeError(
				CodeInvalidProbeRoot,
				"validate-root",
				clean,
				fmt.Errorf("existing root must be a real directory"),
			)
		}
		entries, readErr := os.ReadDir(clean)
		if readErr != nil {
			return "", probeError(CodeInvalidProbeRoot, "read-root", clean, readErr)
		}
		if len(entries) != 0 {
			return "", probeError(
				CodeInvalidProbeRoot,
				"validate-root",
				clean,
				fmt.Errorf("existing probe root must be empty"),
			)
		}
	} else if !os.IsNotExist(err) {
		return "", probeError(CodeInvalidProbeRoot, "validate-root", clean, err)
	}
	return clean, nil
}

func newOperationID() (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "op-" + hex.EncodeToString(random[:]), nil
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func validateCleanupTarget(plan Plan) error {
	if !pathWithin(plan.Paths.Root, plan.Paths.Operation) ||
		filepath.Dir(plan.Paths.Operation) != plan.Paths.Root ||
		!strings.HasPrefix(filepath.Base(plan.Paths.Operation), "testplay-vhdx-probe-") {
		return probeError(
			CodeCleanupFailed,
			"validate-cleanup-target",
			plan.Paths.Operation,
			fmt.Errorf("cleanup target escaped the probe root"),
		)
	}
	return nil
}
