package runsvc_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/artifacts"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/libraryimage"
	"github.com/Kubonsang/testplay-runner/internal/librarymaterializer"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
)

type imageTestRunner struct {
	resultsXML []byte
	calls      int
	failTests  bool
}

type failingMaterializer struct{}

func (failingMaterializer) ID() string {
	return "test-failure"
}

func (failingMaterializer) Materialize(
	_ context.Context,
	request librarymaterializer.Request,
) (*librarymaterializer.Result, error) {
	if err := os.MkdirAll(request.DestinationPath, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(
		filepath.Join(request.DestinationPath, "partial.bin"),
		[]byte("partial"),
		0644,
	); err != nil {
		return nil, err
	}
	return nil, errors.New("injected materializer failure")
}

func (r *imageTestRunner) Run(_ context.Context, args []string, _, _ io.Writer) (int, error) {
	r.calls++
	projectPath := argValue(args, "-projectPath")
	resultsPath := argValue(args, "-testResults")
	if resultsPath == "" {
		if err := os.MkdirAll(filepath.Join(projectPath, "Library"), 0755); err != nil {
			return -1, err
		}
		if err := os.WriteFile(filepath.Join(projectPath, "Library", "ArtifactDB"), []byte("base"), 0644); err != nil {
			return -1, err
		}
		// The image backend copies Packages, so this builder mutation must not
		// reach the source project.
		_ = os.WriteFile(filepath.Join(projectPath, "Packages", "generated.lock"), []byte("builder"), 0644)
		return 0, nil
	}

	if r.failTests {
		_ = os.WriteFile(filepath.Join(projectPath, "Library", "ArtifactDB"), []byte("failed-run mutation"), 0644)
		_ = os.WriteFile(filepath.Join(projectPath, "Packages", "generated.lock"), []byte("test"), 0644)
		return 1, nil
	}
	if err := os.WriteFile(resultsPath, r.resultsXML, 0644); err != nil {
		return -1, err
	}
	_ = os.WriteFile(filepath.Join(projectPath, "Library", "ArtifactDB"), []byte("test mutation"), 0644)
	_ = os.WriteFile(filepath.Join(projectPath, "Packages", "generated.lock"), []byte("test"), 0644)
	return 0, nil
}

func TestService_ImageBackendCreatesThenReusesImage(t *testing.T) {
	cfg, project := imageConfig(t)
	runner := &imageTestRunner{
		resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml"),
	}
	svc := imageService(cfg, project, runner)

	first, err := svc.Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.ExitCode != 0 {
		t.Fatalf("first exit = %d, want 0", first.ExitCode)
	}
	if first.Result.Backend != "shadow" {
		t.Fatalf("execution backend = %q, want shadow", first.Result.Backend)
	}
	metrics := first.Result.WorkspaceMetrics
	if metrics == nil {
		t.Fatal("workspace metrics are nil")
	}
	if metrics.WorkspaceBackend != "image" ||
		metrics.ImageResolutionStatus != "missing" ||
		metrics.ImageStatus != "valid" {
		t.Fatalf("first metrics = %+v", metrics)
	}
	if metrics.FallbackUsed {
		t.Fatal("explicit image backend reported fallback")
	}
	if metrics.Materializer != librarymaterializer.PhysicalCopyID {
		t.Fatalf("materializer = %q, want %q", metrics.Materializer, librarymaterializer.PhysicalCopyID)
	}
	if metrics.LibraryMaterializeMs != metrics.LibraryMaterializationMs {
		t.Fatalf("phase materialize = %d, aggregate = %d",
			metrics.LibraryMaterializeMs, metrics.LibraryMaterializationMs)
	}
	for name, duration := range map[string]int64{
		"imageResolveMs":        metrics.ImageResolveMs,
		"imageMetadataVerifyMs": metrics.ImageMetadataVerifyMs,
		"imageFullHashMs":       metrics.ImageFullHashMs,
		"libraryMaterializeMs":  metrics.LibraryMaterializeMs,
		"workspaceVerifyMs":     metrics.WorkspaceVerifyMs,
		"cleanupMs":             metrics.CleanupMs,
	} {
		if duration < 0 {
			t.Fatalf("%s = %d, want non-negative", name, duration)
		}
	}
	if metrics.BaseImageLogicalBytes <= 0 || metrics.BaseImagePhysicalBytes <= 0 {
		t.Fatalf("base image usage was not recorded: %+v", metrics)
	}
	if metrics.ImageBuilderLogicalBytes <= 0 || metrics.ImageBuilderPhysicalBytes <= 0 {
		t.Fatalf("cold builder usage was not recorded: %+v", metrics)
	}
	if metrics.WorkspaceLogicalBytes <= 0 || metrics.WorkspacePhysicalBytes <= 0 {
		t.Fatalf("workspace usage was not recorded: %+v", metrics)
	}
	if metrics.ObservedPeakAdditionalPhysicalBytes <
		metrics.ImageStorePhysicalBytes+metrics.WorkspacePhysicalBytes {
		t.Fatalf("observed peak omits image store or workspace: %+v", metrics)
	}
	if metrics.RetainedPhysicalBytes != metrics.ImageStorePhysicalBytes {
		t.Fatalf("retained bytes = %d, want image store %d",
			metrics.RetainedPhysicalBytes, metrics.ImageStorePhysicalBytes)
	}
	if metrics.CleanupReclaimedPhysicalBytes != metrics.WorkspacePhysicalBytes {
		t.Fatalf("cleanup reclaimed = %d, want workspace %d",
			metrics.CleanupReclaimedPhysicalBytes, metrics.WorkspacePhysicalBytes)
	}
	if runner.calls != 2 {
		t.Fatalf("first run calls = %d, want builder + tests", runner.calls)
	}
	assertSourceProtected(t, project)
	assertNoShadowWorkspace(t, project)

	second, err := svc.Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.ExitCode != 0 {
		t.Fatalf("second exit = %d, want 0", second.ExitCode)
	}
	if second.Result.WorkspaceMetrics.ImageResolutionStatus != "valid" {
		t.Fatalf("second image resolution = %q, want valid",
			second.Result.WorkspaceMetrics.ImageResolutionStatus)
	}
	if second.Result.WorkspaceMetrics.ImageCreationMs != 0 {
		t.Fatalf("warm imageCreationMs = %d, want 0",
			second.Result.WorkspaceMetrics.ImageCreationMs)
	}
	if second.Result.WorkspaceMetrics.ImageBuilderPhysicalBytes != 0 {
		t.Fatalf("warm builder bytes = %d, want 0",
			second.Result.WorkspaceMetrics.ImageBuilderPhysicalBytes)
	}
	if runner.calls != 3 {
		t.Fatalf("total calls = %d, want builder + two test runs", runner.calls)
	}

	key, err := libraryimage.ComputeKey(project, cfg.UnityPath)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := libraryimage.NewStore(project).Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(resolution.Image.LibraryPath, "ArtifactDB"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatalf("base image was mutated by a test workspace: %q", data)
	}
	assertSourceProtected(t, project)
	assertNoShadowWorkspace(t, project)
}

func TestService_ImageBackendInvalidatesAfterPackageChange(t *testing.T) {
	cfg, project := imageConfig(t)
	runner := &imageTestRunner{resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml")}
	svc := imageService(cfg, project, runner)
	request := runsvc.Request{Config: cfg, WorkspaceBackend: runsvc.WorkspaceBackendImage}

	if _, err := svc.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(project, "Packages", "manifest.json"),
		[]byte(`{"dependencies":{"com.unity.test-framework":"2.0.0"}}`),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	second, err := svc.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Result.WorkspaceMetrics.ImageResolutionStatus != "stale" {
		t.Fatalf("status = %q, want stale", second.Result.WorkspaceMetrics.ImageResolutionStatus)
	}
	if runner.calls != 4 {
		t.Fatalf("calls = %d, want two builder + two test runs", runner.calls)
	}
}

func TestService_ImageBackendRecreatesCorruptImage(t *testing.T) {
	cfg, project := imageConfig(t)
	runner := &imageTestRunner{resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml")}
	svc := imageService(cfg, project, runner)
	request := runsvc.Request{Config: cfg, WorkspaceBackend: runsvc.WorkspaceBackendImage}

	first, err := svc.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	key, err := libraryimage.ComputeKey(project, cfg.UnityPath)
	if err != nil {
		t.Fatal(err)
	}
	store := libraryimage.NewStore(project)
	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolution.Image.LibraryPath, "ArtifactDB"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}

	second, err := svc.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Result.WorkspaceMetrics.ImageResolutionStatus != "corrupt" {
		t.Fatalf("status = %q, want corrupt", second.Result.WorkspaceMetrics.ImageResolutionStatus)
	}
	if first.Result.WorkspaceMetrics.ImageKey != second.Result.WorkspaceMetrics.ImageKey {
		t.Fatal("corruption unexpectedly changed the input key")
	}
	if runner.calls != 4 {
		t.Fatalf("calls = %d, want recreate builder invocation", runner.calls)
	}
}

func TestService_ImageBackendFailureDoesNotMutateSourceOrBase(t *testing.T) {
	cfg, project := imageConfig(t)
	runner := &imageTestRunner{resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml")}
	svc := imageService(cfg, project, runner)
	request := runsvc.Request{Config: cfg, WorkspaceBackend: runsvc.WorkspaceBackendImage}
	if _, err := svc.Run(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	key, err := libraryimage.ComputeKey(project, cfg.UnityPath)
	if err != nil {
		t.Fatal(err)
	}
	store := libraryimage.NewStore(project)
	before, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	baseBefore, err := os.ReadFile(filepath.Join(before.Image.LibraryPath, "ArtifactDB"))
	if err != nil {
		t.Fatal(err)
	}

	runner.failTests = true
	failed, err := svc.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("failed Unity run became infrastructure error: %v", err)
	}
	if failed.ExitCode == 0 {
		t.Fatal("failed Unity run reported success")
	}
	after, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	baseAfter, err := os.ReadFile(filepath.Join(after.Image.LibraryPath, "ArtifactDB"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseBefore, baseAfter) {
		t.Fatalf("base changed after failed run: before=%q after=%q", baseBefore, baseAfter)
	}
	assertSourceProtected(t, project)
	assertNoShadowWorkspace(t, project)
}

func TestService_MaterializerFailureDoesNotCorruptOrQuarantineImage(t *testing.T) {
	cfg, project := imageConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	if _, err := imageService(
		cfg,
		project,
		&imageTestRunner{resultsXML: xmlData},
	).Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
	}); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	failedService := imageService(
		cfg,
		project,
		&imageTestRunner{resultsXML: xmlData},
	)
	failedService.LibraryMaterializer = failingMaterializer{}
	_, err := failedService.Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
	})
	if err == nil || !strings.Contains(err.Error(), "materialize Library") {
		t.Fatalf("error = %v, want distinct materialization failure", err)
	}

	key, err := libraryimage.ComputeKey(project, cfg.UnityPath)
	if err != nil {
		t.Fatal(err)
	}
	store := libraryimage.NewStore(project)
	resolution, err := store.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Status != libraryimage.StatusValid {
		t.Fatalf("image status = %q, want valid: %s", resolution.Status, resolution.Reason)
	}
	if entries, err := os.ReadDir(filepath.Join(store.Root(), "quarantine")); err == nil && len(entries) > 0 {
		t.Fatalf("materializer failure quarantined image: %v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	assertSourceProtected(t, project)
	assertNoShadowWorkspace(t, project)
}

func TestService_LegacyAndImageBackendParity(t *testing.T) {
	cfg, project := imageConfig(t)
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")
	legacyRunner := &imageTestRunner{resultsXML: xmlData}
	imageRunner := &imageTestRunner{resultsXML: xmlData}

	legacy, err := imageService(cfg, project, legacyRunner).Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendLegacy,
	})
	if err != nil {
		t.Fatal(err)
	}
	image, err := imageService(cfg, project, imageRunner).Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
	})
	if err != nil {
		t.Fatal(err)
	}

	if legacy.ExitCode != image.ExitCode ||
		legacy.Result.Total != image.Result.Total ||
		legacy.Result.Passed != image.Result.Passed ||
		legacy.Result.Failed != image.Result.Failed ||
		!reflect.DeepEqual(legacy.Result.Tests, image.Result.Tests) ||
		!reflect.DeepEqual(legacy.Result.Errors, image.Result.Errors) {
		t.Fatalf("parity mismatch:\nlegacy=%+v\nimage=%+v", legacy.Result, image.Result)
	}
	if legacy.Result.WorkspaceMetrics.WorkspaceBackend != "legacy" {
		t.Fatalf("legacy metrics backend = %q", legacy.Result.WorkspaceMetrics.WorkspaceBackend)
	}
	if legacy.Result.WorkspaceMetrics.RetainedPhysicalBytes <= 0 ||
		legacy.Result.WorkspaceMetrics.ObservedPeakAdditionalPhysicalBytes <=
			legacy.Result.WorkspaceMetrics.RetainedPhysicalBytes {
		t.Fatalf("legacy lifecycle usage was not separated: %+v",
			legacy.Result.WorkspaceMetrics)
	}
	if image.Result.WorkspaceMetrics.WorkspaceBackend != "image" {
		t.Fatalf("image metrics backend = %q", image.Result.WorkspaceMetrics.WorkspaceBackend)
	}
}

func TestService_ExternalWorkspaceStoreRootIsIsolatedFromProject(t *testing.T) {
	cfg, project := imageConfig(t)
	storeRoot := t.TempDir()
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")

	image, err := imageService(cfg, project, &imageTestRunner{resultsXML: xmlData}).Run(
		context.Background(),
		runsvc.Request{
			Config:             cfg,
			WorkspaceBackend:   runsvc.WorkspaceBackendImage,
			WorkspaceStoreRoot: storeRoot,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if image.ExitCode != 0 {
		t.Fatalf("image exit = %d", image.ExitCode)
	}

	legacy, err := imageService(cfg, project, &imageTestRunner{resultsXML: xmlData}).Run(
		context.Background(),
		runsvc.Request{
			Config:             cfg,
			WorkspaceBackend:   runsvc.WorkspaceBackendLegacy,
			WorkspaceStoreRoot: storeRoot,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ExitCode != 0 {
		t.Fatalf("legacy exit = %d", legacy.ExitCode)
	}

	imageMarkers, err := filepath.Glob(filepath.Join(
		storeRoot, "*", "library-images", "images", "*", "COMPLETE",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(imageMarkers) != 1 {
		t.Fatalf("external image markers = %v, want one", imageMarkers)
	}
	legacyArtifacts, err := filepath.Glob(filepath.Join(
		storeRoot, "*", "legacy-cache", "Library", "ArtifactDB",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyArtifacts) != 1 {
		t.Fatalf("external legacy cache artifacts = %v, want one", legacyArtifacts)
	}
	for _, rel := range []string{
		filepath.Join(".testplay", "library-images"),
		filepath.Join(".testplay", "cache", "Library"),
	} {
		if _, err := os.Stat(filepath.Join(project, rel)); !os.IsNotExist(err) {
			t.Fatalf("project-local persistent store %s unexpectedly exists: %v", rel, err)
		}
	}
}

func TestService_ImageClearCacheDoesNotDeleteExternalLegacyCache(t *testing.T) {
	cfg, project := imageConfig(t)
	storeRoot := t.TempDir()
	xmlData := mustReadFixture(t, "../../internal/parser/testdata/passing.xml")

	if _, err := imageService(cfg, project, &imageTestRunner{resultsXML: xmlData}).Run(
		context.Background(),
		runsvc.Request{
			Config:             cfg,
			WorkspaceBackend:   runsvc.WorkspaceBackendLegacy,
			WorkspaceStoreRoot: storeRoot,
		},
	); err != nil {
		t.Fatal(err)
	}
	legacyArtifacts, err := filepath.Glob(filepath.Join(
		storeRoot, "*", "legacy-cache", "Library", "ArtifactDB",
	))
	if err != nil || len(legacyArtifacts) != 1 {
		t.Fatalf("legacy cache setup failed: paths=%v err=%v", legacyArtifacts, err)
	}

	if _, err := imageService(cfg, project, &imageTestRunner{resultsXML: xmlData}).Run(
		context.Background(),
		runsvc.Request{
			Config:             cfg,
			WorkspaceBackend:   runsvc.WorkspaceBackendImage,
			WorkspaceStoreRoot: storeRoot,
			ClearCache:         true,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyArtifacts[0]); err != nil {
		t.Fatalf("image clear-cache deleted Legacy cache: %v", err)
	}
}

func TestService_RejectsUnsafeWorkspaceStoreRoot(t *testing.T) {
	cfg, project := imageConfig(t)
	svc := imageService(cfg, project, &imageTestRunner{
		resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml"),
	})

	for _, root := range []string{"relative/store", project, filepath.Dir(project)} {
		_, err := svc.Run(context.Background(), runsvc.Request{
			Config:             cfg,
			WorkspaceBackend:   runsvc.WorkspaceBackendImage,
			WorkspaceStoreRoot: root,
		})
		if err == nil {
			t.Fatalf("unsafe store root %q was accepted", root)
		}
	}
}

func TestService_KeepWorkspace(t *testing.T) {
	cfg, project := imageConfig(t)
	runner := &imageTestRunner{resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml")}
	resp, err := imageService(cfg, project, runner).Run(context.Background(), runsvc.Request{
		Config:           cfg,
		WorkspaceBackend: runsvc.WorkspaceBackendImage,
		KeepWorkspace:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Result.WorkspaceMetrics.WorkspaceKept {
		t.Fatal("workspaceKept = false")
	}
	if resp.Result.WorkspaceMetrics.CleanupReclaimedPhysicalBytes != 0 {
		t.Fatalf("kept workspace reclaimed %d bytes",
			resp.Result.WorkspaceMetrics.CleanupReclaimedPhysicalBytes)
	}
	if resp.Result.WorkspaceMetrics.RetainedPhysicalBytes !=
		resp.Result.WorkspaceMetrics.ImageStorePhysicalBytes+
			resp.Result.WorkspaceMetrics.WorkspacePhysicalBytes {
		t.Fatalf("kept retained bytes do not include image and workspace: %+v",
			resp.Result.WorkspaceMetrics)
	}
	matches, err := filepath.Glob(filepath.Join(project, ".testplay-shadow-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("kept workspaces = %d, want 1: %v", len(matches), matches)
	}
	if err := os.RemoveAll(matches[0]); err != nil {
		t.Fatal(err)
	}
}

func imageConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	project := t.TempDir()
	for _, dir := range []string{"Assets", "Packages", "ProjectSettings", "Library"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "Assets", "Test.cs"), []byte("// source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Packages", "manifest.json"), []byte(`{"dependencies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Packages", "packages-lock.json"), []byte(`{"dependencies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.3.8f1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectSettings.asset"), []byte("PlayerSettings:\n  scriptingBackend: 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Library", "source.sentinel"), []byte("source library"), 0644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		SchemaVersion: "1",
		UnityPath:     "/fake/unity",
		ProjectPath:   project,
		ResultDir:     filepath.Join(project, ".testplay", "results"),
		Timeout:       config.Timeouts{TotalMs: 300000},
		TestPlatform:  "edit_mode",
	}, project
}

func imageService(cfg *config.Config, project string, runner *imageTestRunner) *runsvc.Service {
	return &runsvc.Service{
		Runner:    runner,
		Store:     history.NewStore(cfg.ResultDir),
		Artifacts: artifacts.NewStore(filepath.Join(project, ".testplay", "runs")),
	}
}

func argValue(args []string, name string) string {
	for index, arg := range args {
		if arg == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func assertSourceProtected(t *testing.T, project string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(project, "Packages", "generated.lock")); !os.IsNotExist(err) {
		t.Fatalf("source Packages was mutated: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, "Assets", "Test.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "// source" {
		t.Fatalf("source Asset changed: %q", data)
	}
	libraryData, err := os.ReadFile(filepath.Join(project, "Library", "source.sentinel"))
	if err != nil {
		t.Fatal(err)
	}
	if string(libraryData) != "source library" {
		t.Fatalf("source Library changed: %q", libraryData)
	}
}

func assertNoShadowWorkspace(t *testing.T, project string) {
	t.Helper()
	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".testplay-shadow-") {
			t.Fatalf("shadow workspace was not cleaned: %s", entry.Name())
		}
	}
}
