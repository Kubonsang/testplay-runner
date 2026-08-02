//go:build windows && vhdx_helper_integration

package vhdxprobe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Kubonsang/testplay-runner/internal/vhdxstorage"
)

// HelperParentFixture is integration-only input for the storage helper. The
// production helper never imports the probe package.
type HelperParentFixture struct {
	ParentPath  string
	PayloadHash string
}

func PrepareHelperParentFixture(ctx context.Context, parentPath, mountPath string) (HelperParentFixture, error) {
	if err := ctx.Err(); err != nil {
		return HelperParentFixture{}, err
	}
	if _, err := os.Lstat(parentPath); err == nil {
		return HelperParentFixture{}, fmt.Errorf("parent fixture already exists: %s", parentPath)
	} else if !os.IsNotExist(err) {
		return HelperParentFixture{}, err
	}
	if err := os.MkdirAll(filepath.Dir(parentPath), 0700); err != nil {
		return HelperParentFixture{}, err
	}
	if err := os.Mkdir(mountPath, 0700); err != nil {
		return HelperParentFixture{}, err
	}
	if err := vhdxstorage.CreateDynamic(parentPath, DefaultParentVirtualBytes); err != nil {
		return HelperParentFixture{}, err
	}
	attachment, err := vhdxstorage.OpenAndAttach(parentPath, false)
	if err != nil {
		return HelperParentFixture{}, err
	}
	if err := attachment.InitializeAndMount(ctx, mountPath); err != nil {
		return HelperParentFixture{}, errors.Join(err, attachment.Close(ctx))
	}
	hash, err := seedParent(mountPath, DefaultPayloadBytes)
	if err != nil {
		return HelperParentFixture{}, errors.Join(err, attachment.Close(ctx))
	}
	if err := attachment.Close(ctx); err != nil {
		return HelperParentFixture{}, err
	}
	if err := os.Remove(mountPath); err != nil {
		return HelperParentFixture{}, err
	}
	return HelperParentFixture{ParentPath: parentPath, PayloadHash: hash}, nil
}
