package unityvhdxfixture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeDriver struct {
	calls        []string
	physicalFail string
	acquireErr   error
	mountFail    string
	vhdxDigest   map[string]string
	parentErr    error
	releaseErr   error
	releases     int
}

func passing(platform, digest string) PlatformResult {
	return PlatformResult{Platform: platform, ExitCode: 0, Total: 1, Passed: 1, Tests: []SemanticTest{{FullName: "Fixture.Pass", Outcome: "Passed"}}, SemanticDigest: digest, ResultsXML: platform + ".xml", WallClockMs: 1}
}

func (f *fakeDriver) Prepare(context.Context, *Evidence) error {
	f.calls = append(f.calls, "prepare")
	return nil
}
func (f *fakeDriver) RunPhysical(_ context.Context, platform string) (PlatformResult, error) {
	f.calls = append(f.calls, "physical:"+platform)
	if f.physicalFail == platform {
		return PlatformResult{}, errors.New("physical failed")
	}
	return passing(platform, platform+"-digest"), nil
}
func (f *fakeDriver) Acquire(context.Context, *Evidence) error {
	f.calls = append(f.calls, "acquire")
	return f.acquireErr
}
func (f *fakeDriver) CheckMount(_ context.Context, phase string, evidence *Evidence) error {
	f.calls = append(f.calls, "mount:"+phase)
	if f.mountFail == phase {
		return fixtureError(CodeLibraryMountLost, "check", phase, nil)
	}
	if phase != MountReleased {
		evidence.MountIntegrity = true
	}
	return nil
}
func (f *fakeDriver) RunVHDX(_ context.Context, platform string) (PlatformResult, error) {
	f.calls = append(f.calls, "vhdx:"+platform)
	digest := platform + "-digest"
	if value := f.vhdxDigest[platform]; value != "" {
		digest = value
	}
	return passing(platform, digest), nil
}
func (f *fakeDriver) Release(_ context.Context, evidence *Evidence) error {
	f.calls = append(f.calls, "release")
	f.releases++
	if f.releaseErr == nil {
		evidence.CleanupPassed = true
	}
	return f.releaseErr
}
func (f *fakeDriver) VerifyParent(_ context.Context, evidence *Evidence) error {
	f.calls = append(f.calls, "parent")
	if f.parentErr == nil {
		evidence.ParentIsolation = true
	}
	return f.parentErr
}

func TestHarnessOrderAndEvidence(t *testing.T) {
	driver := &fakeDriver{}
	evidence := NewEvidence("run-test")
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := Run(context.Background(), driver, &evidence, RunOptions{EvidencePath: path}); err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare", "physical:edit_mode", "physical:play_mode", "acquire", "mount:ready", "vhdx:edit_mode", "mount:after-editmode", "vhdx:play_mode", "mount:after-playmode", "mount:before-release", "release", "mount:released", "parent"}
	if !reflect.DeepEqual(driver.calls, want) {
		t.Fatalf("calls=%v want=%v", driver.calls, want)
	}
	if !evidence.SemanticParity || !evidence.ParentIsolation || !evidence.CleanupPassed {
		t.Fatalf("evidence=%#v", evidence)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var written Evidence
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	if written.RunID != "run-test" || written.Metrics.ChildCreateMs != nil {
		t.Fatalf("written=%#v", written)
	}
}

func TestOutliersRecordMeasuredValuesOnly(t *testing.T) {
	long := int64(30001)
	outliers := phaseOutliers(Metrics{VHDXEditModeMs: &long})
	if len(outliers) != 1 || outliers[0] != "vhdxEditModeMs=30001" {
		t.Fatalf("outliers=%v", outliers)
	}
}

func TestPhysicalFailurePreventsAcquire(t *testing.T) {
	driver := &fakeDriver{physicalFail: PlatformEditMode}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if err == nil || containsCall(driver.calls, "acquire") {
		t.Fatalf("err=%v calls=%v", err, driver.calls)
	}
}

func TestAcquireFailurePreventsVHDX(t *testing.T) {
	driver := &fakeDriver{acquireErr: errors.New("attach failed")}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if err == nil || containsPrefix(driver.calls, "vhdx:") || driver.releases != 0 {
		t.Fatalf("err=%v calls=%v releases=%d", err, driver.calls, driver.releases)
	}
}

func TestMountLossStillReleases(t *testing.T) {
	driver := &fakeDriver{mountFail: MountAfterEdit}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if ErrorCode(err) != CodeLibraryMountLost || driver.releases != 1 {
		t.Fatalf("err=%v releases=%d calls=%v", err, driver.releases, driver.calls)
	}
}

func TestEditModeParityFailure(t *testing.T) {
	driver := &fakeDriver{vhdxDigest: map[string]string{PlatformEditMode: "different"}}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if ErrorCode(err) != CodeSemanticParityFailed || driver.releases != 1 {
		t.Fatalf("err=%v releases=%d", err, driver.releases)
	}
}

func TestPlayModeParityFailure(t *testing.T) {
	driver := &fakeDriver{vhdxDigest: map[string]string{PlatformPlayMode: "different"}}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if ErrorCode(err) != CodeSemanticParityFailed || driver.releases != 1 {
		t.Fatalf("err=%v releases=%d", err, driver.releases)
	}
}

func TestParentIsolationFailure(t *testing.T) {
	driver := &fakeDriver{parentErr: fixtureError(CodeParentIsolationFailed, "hash", "parent.vhdx", nil)}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if ErrorCode(err) != CodeParentIsolationFailed || driver.releases != 1 {
		t.Fatalf("err=%v releases=%d", err, driver.releases)
	}
}

func TestReleaseFailureIsReturned(t *testing.T) {
	driver := &fakeDriver{releaseErr: fixtureError(CodeCleanupFailed, "release", "child.vhdx", nil)}
	err := Run(context.Background(), driver, nil, RunOptions{})
	if ErrorCode(err) != CodeCleanupFailed || driver.releases != 2 {
		t.Fatalf("err=%v releases=%d", err, driver.releases)
	}
}

func TestUnsupportedPlatformCode(t *testing.T) {
	err := fixtureError(CodeUnsupportedPlatform, "fixture", "linux", nil)
	if ErrorCode(err) != CodeUnsupportedPlatform {
		t.Fatal(err)
	}
}

func containsCall(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
