package unityvhdxfixture

import (
	"context"
	"errors"
	"time"
)

const (
	PlatformEditMode   = "edit_mode"
	PlatformPlayMode   = "play_mode"
	MountReady         = "ready"
	MountAfterEdit     = "after-editmode"
	MountAfterPlay     = "after-playmode"
	MountBeforeRelease = "before-release"
	MountReleased      = "released"
)

type Driver interface {
	Prepare(context.Context, *Evidence) error
	RunPhysical(context.Context, string) (PlatformResult, error)
	Acquire(context.Context, *Evidence) error
	CheckMount(context.Context, string, *Evidence) error
	RunVHDX(context.Context, string) (PlatformResult, error)
	Release(context.Context, *Evidence) error
	VerifyParent(context.Context, *Evidence) error
}

type RunOptions struct {
	EvidencePath string
	StartedAt    time.Time
}

func Run(ctx context.Context, driver Driver, evidence *Evidence, options RunOptions) (returnErr error) {
	started := time.Now()
	if !options.StartedAt.IsZero() {
		started = options.StartedAt
	}
	if evidence == nil {
		value := NewEvidence("")
		evidence = &value
	}
	defer func() {
		evidence.Metrics.TotalWallClockMs = milliseconds(time.Since(started).Milliseconds())
		evidence.Outliers = phaseOutliers(evidence.Metrics)
		if returnErr != nil {
			var value *Error
			if errors.As(returnErr, &value) {
				evidence.Error = value
			}
		}
		if options.EvidencePath != "" {
			if err := WriteEvidence(options.EvidencePath, *evidence); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	if err := driver.Prepare(ctx, evidence); err != nil {
		return err
	}
	physicalEdit, err := driver.RunPhysical(ctx, PlatformEditMode)
	if err != nil {
		return err
	}
	evidence.PhysicalEditMode = &physicalEdit
	evidence.Metrics.PhysicalEditModeMs = milliseconds(physicalEdit.WallClockMs)
	if err := RequirePassing(physicalEdit); err != nil {
		return err
	}
	physicalPlay, err := driver.RunPhysical(ctx, PlatformPlayMode)
	if err != nil {
		return err
	}
	evidence.PhysicalPlayMode = &physicalPlay
	evidence.Metrics.PhysicalPlayModeMs = milliseconds(physicalPlay.WallClockMs)
	if err := RequirePassing(physicalPlay); err != nil {
		return err
	}
	if err := driver.Acquire(ctx, evidence); err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			if err := driver.Release(context.Background(), evidence); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	if err := driver.CheckMount(ctx, MountReady, evidence); err != nil {
		return err
	}
	vhdxEdit, err := driver.RunVHDX(ctx, PlatformEditMode)
	if err != nil {
		return err
	}
	evidence.VHDXEditMode = &vhdxEdit
	evidence.Metrics.VHDXEditModeMs = milliseconds(vhdxEdit.WallClockMs)
	if err := RequirePassing(vhdxEdit); err != nil {
		return err
	}
	if err := driver.CheckMount(ctx, MountAfterEdit, evidence); err != nil {
		return err
	}
	vhdxPlay, err := driver.RunVHDX(ctx, PlatformPlayMode)
	if err != nil {
		return err
	}
	evidence.VHDXPlayMode = &vhdxPlay
	evidence.Metrics.VHDXPlayModeMs = milliseconds(vhdxPlay.WallClockMs)
	if err := RequirePassing(vhdxPlay); err != nil {
		return err
	}
	if err := driver.CheckMount(ctx, MountAfterPlay, evidence); err != nil {
		return err
	}
	if err := driver.CheckMount(ctx, MountBeforeRelease, evidence); err != nil {
		return err
	}
	if err := driver.Release(ctx, evidence); err != nil {
		return err
	}
	released = true
	if err := driver.CheckMount(ctx, MountReleased, evidence); err != nil {
		return err
	}
	if err := CompareSemantic(physicalEdit, vhdxEdit); err != nil {
		return err
	}
	if err := CompareSemantic(physicalPlay, vhdxPlay); err != nil {
		return err
	}
	evidence.SemanticParity = true
	if err := driver.VerifyParent(ctx, evidence); err != nil {
		return err
	}
	return nil
}
