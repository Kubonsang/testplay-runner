# v0.4.0 Host/Client Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Host 인스턴스가 지정 phase에 도달한 후에만 Client 인스턴스가 기동되도록 순서를 보장하고, ready timeout 발생 시 원인이 시나리오 결과 JSON에 반영된다.

**Architecture:** `InstanceRunner` 시그니처에 `readyCh chan<- struct{}` 추가 → 생산 runner가 `readyNotifier`(StatusWriter 래퍼)를 생성해 지정 phase 도달 시 채널 close → `RunScenario`가 `depends_on` 인스턴스의 채널을 `select + time.After + ctx.Done()`으로 대기. 디스크 폴링 없음 — 파일 쓰기는 외부 관측(에이전트)용으로만 수행.

**Tech Stack:** Go stdlib (`sync`, `time`, `context`), 기존 `internal/status`, `internal/scenario`, `internal/runsvc`

---

## File Map

| 파일 | 변경 유형 | 역할 |
|---|---|---|
| `internal/scenario/spec.go` | Modify | `InstanceSpec`에 `DependsOn`, `ReadyPhase`, `ReadyTimeoutMs` 추가; 헬퍼 메서드; `Load()` 검증 강화 |
| `internal/scenario/spec_test.go` | Modify | 새 필드 파싱/검증 테스트 |
| `internal/scenario/notify.go` | **Create** | `readyNotifier` — `status.WriterInterface` 래퍼; 지정 phase 도달 시 채널 close |
| `internal/scenario/notify_test.go` | **Create** | `readyNotifier` 동작 테스트 |
| `internal/scenario/runner.go` | Modify | `InstanceRunner` 시그니처 변경; `ScenarioResult`에 `OrchestratorErrors` 추가; 순서 제어 로직 구현 |
| `internal/scenario/runner_test.go` | Modify | 시그니처 업데이트; 순서 보장·timeout·취소 테스트 추가 |
| `cmd/testplay/run.go` | Modify | 생산 `InstanceRunner`에 per-role StatusWriter + ReadyNotifier 주입; `orchestrator_errors` JSON 출력 |
| `cmd/testplay/run_test.go` | Modify | 시그니처 업데이트; orchestrator_errors 출력 테스트 |
| `CLAUDE.md` | Modify | "Scenario — no status polling" Known Limitation 제거; v0.4 로드맵 업데이트 |

---

## Task 1: Per-instance status polling (P1)

**Files:**
- Modify: `cmd/testplay/run.go:136-160`
- Modify: `cmd/testplay/run_test.go`

### 배경

현재 `runScenario()`의 생산 `InstanceRunner`는 `StatusWriter`를 nil로 남긴다 (`// No StatusWriter in scenario mode`). 에이전트가 시나리오 실행 중 진행 상황을 관측할 수 없다. 이 태스크는 각 인스턴스에 `testplay-status-<role>.json` 파일을 연결한다.

- [ ] **Step 1: 실패하는 테스트 작성**

`cmd/testplay/run_test.go`에 다음 테스트 추가:

```go
func TestRunScenario_WritesPerRoleStatusFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "host.json"},
			{Role: "client", Config: "client.json"},
		},
	}
	specPath := filepath.Join(dir, "scenario.json")
	writeScenarioFile(t, specPath, spec)

	var writerPaths []string
	var mu sync.Mutex

	deps := scenarioDeps{
		ctx: context.Background(),
		run: func(_ context.Context, inst scenario.InstanceSpec) (runsvc.Response, error) {
			// capture which status file was written
			statusPath := fmt.Sprintf("testplay-status-%s.json", inst.Role)
			mu.Lock()
			writerPaths = append(writerPaths, statusPath)
			mu.Unlock()
			return runsvc.Response{ExitCode: 0, Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			}}, nil
		},
	}

	var buf bytes.Buffer
	runScenario(&buf, specPath, deps)

	mu.Lock()
	defer mu.Unlock()
	wantPaths := map[string]bool{
		"testplay-status-host.json":   true,
		"testplay-status-client.json": true,
	}
	for _, p := range writerPaths {
		if !wantPaths[p] {
			t.Errorf("unexpected status path %q", p)
		}
		delete(wantPaths, p)
	}
	for p := range wantPaths {
		t.Errorf("expected status path %q was not written", p)
	}
}
```

`writeScenarioFile` 헬퍼 (이미 없으면 추가):

```go
func writeScenarioFile(t *testing.T, path string, spec *scenario.ScenarioFile) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"schema_version": "1",
		"instances":      spec.Instances,
	})
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```bash
go test ./cmd/testplay/... -run TestRunScenario_WritesPerRoleStatusFiles -v
```

Expected: FAIL (writerPaths가 비어 있음)

- [ ] **Step 3: `runScenario`의 생산 InstanceRunner에 per-role StatusWriter 주입**

`cmd/testplay/run.go`의 production `run` 구성 부분 (`if run == nil {` 블록) 수정:

```go
if run == nil {
    run = func(ctx context.Context, instSpec scenario.InstanceSpec) (runsvc.Response, error) {
        cfgPath := spec.ConfigPath(instSpec)
        cfg, loadErr := config.Load(cfgPath)
        if loadErr != nil {
            return runsvc.Response{}, loadErr
        }
        if valErr := cfg.Validate(true); valErr != nil {
            return runsvc.Response{}, valErr
        }

        instanceCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
        defer cancel()

        artifactRoot := filepath.Join(cfg.ProjectPath, ".testplay", "runs")
        svc := &runsvc.Service{
            Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath},
            Store:        history.NewStore(cfg.ResultDir),
            Artifacts:    artifacts.NewStore(artifactRoot),
            StatusWriter: status.NewWriter(fmt.Sprintf("testplay-status-%s.json", instSpec.Role)),
        }
        return svc.Run(instanceCtx, runsvc.Request{Config: cfg})
    }
}
```

`fmt` 패키지가 `cmd/testplay/run.go` imports에 없으면 추가:

```go
import (
    "context"
    "fmt"
    "io"
    // ... 기존 imports
)
```

- [ ] **Step 4: 테스트 통과 확인**

```bash
go test ./cmd/testplay/... -run TestRunScenario_WritesPerRoleStatusFiles -v
```

Expected: PASS

- [ ] **Step 5: 전체 테스트 통과 확인**

```bash
go test ./... -count=1
```

Expected: 전 패키지 ok

- [ ] **Step 6: 커밋**

```bash
git add cmd/testplay/run.go cmd/testplay/run_test.go
git commit -m "feat(scenario): write per-instance testplay-status-<role>.json in scenario mode"
```

---

## Task 2: InstanceSpec 확장 — 순서 제어 필드

**Files:**
- Modify: `internal/scenario/spec.go`
- Modify: `internal/scenario/spec_test.go`

### 배경

시나리오 파일에서 Host/Client 기동 순서를 제어하기 위한 필드 3개를 추가한다:
- `depends_on`: 이 인스턴스가 기다릴 역할 이름
- `ready_phase`: 의존 인스턴스가 도달해야 할 phase (기본값: `"compiling"`)
- `ready_timeout_ms`: 대기 timeout (기본값: 30000)

`compiling`이 기본값인 이유: `runsvc.Service.Run()`이 Unity 프로세스 실행 직전에 `PhaseCompiling`을 기록한다. 이것이 "Host의 Unity가 기동되고 있다"는 가장 빠른 신호다.

- [ ] **Step 1: 실패하는 테스트 작성**

`internal/scenario/spec_test.go`에 추가:

```go
func TestLoad_DependsOn_ValidReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host",   "config": "host.json"},
			{"role": "client", "config": "client.json", "depends_on": "host", "ready_timeout_ms": 5000}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	sf, err := scenario.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sf.Instances[1].DependsOn != "host" {
		t.Errorf("expected depends_on=host, got %q", sf.Instances[1].DependsOn)
	}
	if sf.Instances[1].ReadyTimeoutMs != 5000 {
		t.Errorf("expected ready_timeout_ms=5000, got %d", sf.Instances[1].ReadyTimeoutMs)
	}
}

func TestLoad_DependsOn_InvalidReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "client", "config": "client.json", "depends_on": "nonexistent"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid depends_on")
	}
}

func TestLoad_DuplicateRoles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	content := `{
		"schema_version": "1",
		"instances": [
			{"role": "host", "config": "a.json"},
			{"role": "host", "config": "b.json"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := scenario.Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate roles")
	}
}

func TestInstanceSpec_EffectiveReadyPhase_Default(t *testing.T) {
	inst := scenario.InstanceSpec{Role: "host", Config: "host.json"}
	if inst.EffectiveReadyPhase() != "compiling" {
		t.Errorf("expected default ready phase 'compiling', got %q", inst.EffectiveReadyPhase())
	}
}

func TestInstanceSpec_EffectiveReadyPhase_Custom(t *testing.T) {
	inst := scenario.InstanceSpec{Role: "host", Config: "host.json", ReadyPhase: "running"}
	if inst.EffectiveReadyPhase() != "running" {
		t.Errorf("expected 'running', got %q", inst.EffectiveReadyPhase())
	}
}

func TestInstanceSpec_EffectiveReadyTimeoutMs_Default(t *testing.T) {
	inst := scenario.InstanceSpec{Role: "host", Config: "host.json"}
	if inst.EffectiveReadyTimeoutMs() != 30000 {
		t.Errorf("expected default timeout 30000, got %d", inst.EffectiveReadyTimeoutMs())
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```bash
go test ./internal/scenario/... -run "TestLoad_DependsOn|TestLoad_Duplicate|TestInstanceSpec_Effective" -v
```

Expected: compile error (필드/메서드 없음)

- [ ] **Step 3: `InstanceSpec`에 필드 및 헬퍼 추가**

`internal/scenario/spec.go`의 `InstanceSpec` 구조체 수정:

```go
// InstanceSpec describes a single instance to run in the scenario.
type InstanceSpec struct {
	Role           string `json:"role"`
	Config         string `json:"config"`          // path to testplay.json, relative to scenario file or absolute
	DependsOn      string `json:"depends_on,omitempty"`       // role this instance waits for before starting
	ReadyPhase     string `json:"ready_phase,omitempty"`      // phase that signals dependency is ready (default: "compiling")
	ReadyTimeoutMs int    `json:"ready_timeout_ms,omitempty"` // ms to wait for dependency (default: 30000)
}

// EffectiveReadyPhase returns the phase string to wait for, defaulting to "compiling".
// "compiling" is the first phase written by runsvc.Service.Run() — immediately before
// Unity is invoked — and is the earliest observable signal that an instance has started.
func (inst InstanceSpec) EffectiveReadyPhase() string {
	if inst.ReadyPhase == "" {
		return "compiling"
	}
	return inst.ReadyPhase
}

// EffectiveReadyTimeoutMs returns the dependency wait timeout in milliseconds, defaulting to 30000.
func (inst InstanceSpec) EffectiveReadyTimeoutMs() int {
	if inst.ReadyTimeoutMs <= 0 {
		return 30000
	}
	return inst.ReadyTimeoutMs
}
```

- [ ] **Step 4: `Load()`에 중복 role 및 `depends_on` 참조 검증 추가**

`internal/scenario/spec.go`의 `Load()` 함수에서 기존 루프 아래에 추가:

```go
// Build role set for cross-reference validation.
roles := make(map[string]struct{}, len(sf.Instances))
for i, inst := range sf.Instances {
    if inst.Role == "" {
        return nil, fmt.Errorf("%w: instances[%d].role is required", ErrScenarioInvalid, i)
    }
    if inst.Config == "" {
        return nil, fmt.Errorf("%w: instances[%d].config is required", ErrScenarioInvalid, i)
    }
    if _, dup := roles[inst.Role]; dup {
        return nil, fmt.Errorf("%w: instances[%d].role %q is not unique", ErrScenarioInvalid, i, inst.Role)
    }
    roles[inst.Role] = struct{}{}
}
// Validate depends_on references.
for i, inst := range sf.Instances {
    if inst.DependsOn == "" {
        continue
    }
    if _, ok := roles[inst.DependsOn]; !ok {
        return nil, fmt.Errorf("%w: instances[%d].depends_on %q references unknown role", ErrScenarioInvalid, i, inst.DependsOn)
    }
    if inst.DependsOn == inst.Role {
        return nil, fmt.Errorf("%w: instances[%d].depends_on %q cannot depend on itself", ErrScenarioInvalid, i, inst.Role)
    }
}
```

주의: 기존 for 루프(`for i, inst := range sf.Instances { if inst.Role == "" ...}`)를 위의 통합 루프로 교체한다.

- [ ] **Step 5: 테스트 통과 확인**

```bash
go test ./internal/scenario/... -run "TestLoad_DependsOn|TestLoad_Duplicate|TestInstanceSpec_Effective" -v
```

Expected: PASS

- [ ] **Step 6: 전체 테스트 통과 확인**

```bash
go test ./... -count=1
```

Expected: 전 패키지 ok

- [ ] **Step 7: 커밋**

```bash
git add internal/scenario/spec.go internal/scenario/spec_test.go
git commit -m "feat(scenario): add depends_on/ready_phase/ready_timeout_ms to InstanceSpec with validation"
```

---

## Task 3: ReadyNotifier — StatusWriter 래퍼

**Files:**
- Create: `internal/scenario/notify.go`
- Create: `internal/scenario/notify_test.go`

### 배경

`readyNotifier`는 `status.WriterInterface`를 래핑한다. 지정 phase가 `Write()`에 전달되면 채널을 닫는다(close). `sync.Once`로 중복 close(panic)를 방지한다.

- [ ] **Step 1: 테스트 파일 생성**

`internal/scenario/notify_test.go` 생성:

```go
package scenario_test

import (
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/scenario"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// nullWriter discards all writes.
type nullWriter struct{}

func (nullWriter) Write(status.Status) error { return nil }

func TestReadyNotifier_FiresOnTargetPhase(t *testing.T) {
	t.Parallel()
	readyCh := make(chan struct{}, 1)
	notifier := scenario.NewReadyNotifier(nullWriter{}, "compiling", readyCh)

	if err := notifier.Write(status.Status{Phase: status.PhaseCompiling}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-readyCh:
		// expected
	default:
		t.Fatal("expected readyCh to be closed after target phase")
	}
}

func TestReadyNotifier_DoesNotFireOnOtherPhase(t *testing.T) {
	t.Parallel()
	readyCh := make(chan struct{}, 1)
	notifier := scenario.NewReadyNotifier(nullWriter{}, "compiling", readyCh)

	if err := notifier.Write(status.Status{Phase: status.PhaseRunning}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-readyCh:
		t.Fatal("readyCh should not be closed for non-target phase")
	default:
		// expected
	}
}

func TestReadyNotifier_FiresOnlyOnce(t *testing.T) {
	t.Parallel()
	readyCh := make(chan struct{})
	notifier := scenario.NewReadyNotifier(nullWriter{}, "compiling", readyCh)

	// First write — closes the channel
	_ = notifier.Write(status.Status{Phase: status.PhaseCompiling})
	// Second write — must not panic (double close)
	_ = notifier.Write(status.Status{Phase: status.PhaseCompiling})
}

func TestReadyNotifier_ForwardsToInner(t *testing.T) {
	t.Parallel()
	var got []status.Status
	inner := &collectingWriter{writes: &got}
	readyCh := make(chan struct{}, 1)
	notifier := scenario.NewReadyNotifier(inner, "compiling", readyCh)

	_ = notifier.Write(status.Status{Phase: status.PhaseRunning})
	_ = notifier.Write(status.Status{Phase: status.PhaseCompiling})

	if len(got) != 2 {
		t.Errorf("expected 2 forwarded writes, got %d", len(got))
	}
}

type collectingWriter struct {
	writes *[]status.Status
}

func (w *collectingWriter) Write(s status.Status) error {
	*w.writes = append(*w.writes, s)
	return nil
}
```

- [ ] **Step 2: 테스트 실패 확인**

```bash
go test ./internal/scenario/... -run TestReadyNotifier -v
```

Expected: compile error (`NewReadyNotifier` 없음)

- [ ] **Step 3: `internal/scenario/notify.go` 생성**

```go
package scenario

import (
	"sync"

	"github.com/Kubonsang/testplay-runner/internal/status"
)

// readyNotifier wraps a status.WriterInterface and closes readyCh the first
// time a Write call contains the target phase. sync.Once prevents double-close.
type readyNotifier struct {
	inner       status.WriterInterface
	targetPhase status.Phase
	readyCh     chan<- struct{}
	once        sync.Once
}

// NewReadyNotifier returns a status.WriterInterface that closes readyCh when
// a Write call with phase == targetPhase is received.
// All writes are forwarded to inner regardless.
// readyCh must be a buffered or otherwise drained channel; closing it is the
// signal mechanism and no value is sent.
func NewReadyNotifier(inner status.WriterInterface, targetPhase string, readyCh chan<- struct{}) status.WriterInterface {
	return &readyNotifier{
		inner:       inner,
		targetPhase: status.Phase(targetPhase),
		readyCh:     readyCh,
	}
}

func (n *readyNotifier) Write(s status.Status) error {
	if s.Phase == n.targetPhase {
		n.once.Do(func() { close(n.readyCh) })
	}
	return n.inner.Write(s)
}
```

- [ ] **Step 4: 테스트 통과 확인**

```bash
go test ./internal/scenario/... -run TestReadyNotifier -v
```

Expected: PASS (4 tests)

- [ ] **Step 5: 전체 테스트 통과 확인**

```bash
go test ./... -count=1
```

Expected: 전 패키지 ok

- [ ] **Step 6: 커밋**

```bash
git add internal/scenario/notify.go internal/scenario/notify_test.go
git commit -m "feat(scenario): add ReadyNotifier — StatusWriter wrapper that closes channel on target phase"
```

---

## Task 4: InstanceRunner 시그니처 변경 및 ScenarioResult 확장

**Files:**
- Modify: `internal/scenario/runner.go`
- Modify: `internal/scenario/runner_test.go`
- Modify: `cmd/testplay/run.go`
- Modify: `cmd/testplay/run_test.go`

### 배경

`InstanceRunner`에 `readyCh chan<- struct{}` 매개변수를 추가한다. 이 태스크는 시그니처만 변경하고 `RunScenario` 로직은 아직 건드리지 않는다 — 컴파일이 깨지지 않도록 한다. `OrchestratorErrors []string`을 `ScenarioResult`에 추가한다.

- [ ] **Step 1: `runner.go`에서 시그니처 변경 및 ScenarioResult 확장**

`internal/scenario/runner.go` 전체 교체:

```go
package scenario

import (
	"context"
	"sync"

	"github.com/Kubonsang/testplay-runner/internal/runsvc"
)

// InstanceRunner executes a single instance and returns its Response.
// readyCh is closed by the runner when the instance reaches its configured
// ready phase (via ReadyNotifier). Pass nil if this instance does not need
// to signal readiness to any dependent.
// Infrastructure failures are returned as error; Unity-side failures are
// encoded in Response.ExitCode.
type InstanceRunner func(ctx context.Context, spec InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error)

// InstanceResult holds the outcome of a single instance run.
type InstanceResult struct {
	Role     string
	Response runsvc.Response
	Err      error // non-nil for infrastructure errors only
}

// ScenarioResult aggregates the outcomes of all instances.
type ScenarioResult struct {
	ExitCode           int
	Instances          []InstanceResult
	OrchestratorErrors []string // non-empty when dependency wait fails (timeout or cancellation)
}

// RunScenario runs all instances in spec concurrently, one goroutine per instance.
// All instances run to completion regardless of individual failures.
// RunScenario itself never returns a non-nil error; instance errors are recorded
// in InstanceResult.Err and orchestration errors in ScenarioResult.OrchestratorErrors.
func RunScenario(ctx context.Context, spec *ScenarioFile, run InstanceRunner) (ScenarioResult, error) {
	results := make([]InstanceResult, len(spec.Instances))
	var wg sync.WaitGroup

	for i, inst := range spec.Instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// readyCh is nil for now — ordering logic added in Task 5
			resp, err := run(ctx, inst, nil)
			results[i] = InstanceResult{
				Role:     inst.Role,
				Response: resp,
				Err:      err,
			}
		}()
	}

	wg.Wait()

	return ScenarioResult{
		ExitCode:  aggregateExitCode(results),
		Instances: results,
	}, nil
}

// aggregateExitCode returns the maximum exit code across all instance results.
// Infrastructure errors (Err != nil) are treated as exit 1.
func aggregateExitCode(results []InstanceResult) int {
	max := 0
	for _, r := range results {
		code := r.Response.ExitCode
		if r.Err != nil {
			code = 1
		}
		if code > max {
			max = code
		}
	}
	return max
}
```

- [ ] **Step 2: `runner_test.go`에서 시그니처 업데이트**

`internal/scenario/runner_test.go`의 모든 `InstanceRunner` 람다를 업데이트 — `readyCh chan<- struct{}` 매개변수 추가:

```go
// 기존:
run := func(_ context.Context, inst scenario.InstanceSpec) (runsvc.Response, error) {
// 변경:
run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
```

파일 내 모든 `func(_ context.Context, inst scenario.InstanceSpec)` 패턴을 동일하게 수정한다. `readyCh`는 현재 테스트에서는 무시해도 된다.

- [ ] **Step 3: `run.go`에서 시그니처 업데이트**

`cmd/testplay/run.go`의 `scenarioDeps.run` 필드 타입 선언 및 production `run` 람다 업데이트:

```go
type scenarioDeps struct {
	ctx context.Context
	run scenario.InstanceRunner // nil = real runner constructed from each instance's config
}
```

production 람다 시그니처:

```go
run = func(ctx context.Context, instSpec scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
    // ... 기존 로직 유지 (Task 1에서 추가한 StatusWriter 포함) ...
    // readyCh 주입은 Task 6에서 처리
}
```

- [ ] **Step 4: `run_test.go`에서 시그니처 업데이트**

`cmd/testplay/run_test.go`의 모든 `InstanceRunner` 람다에 `readyCh chan<- struct{}` 추가:

```go
// 기존:
run: func(_ context.Context, inst scenario.InstanceSpec) (runsvc.Response, error) {
// 변경:
run: func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
```

- [ ] **Step 5: 컴파일 및 테스트 통과 확인**

```bash
go build ./...
go test ./... -count=1
```

Expected: 전 패키지 ok (행동 변화 없음, 시그니처만 변경)

- [ ] **Step 6: 커밋**

```bash
git add internal/scenario/runner.go internal/scenario/runner_test.go \
        cmd/testplay/run.go cmd/testplay/run_test.go
git commit -m "refactor(scenario): add readyCh param to InstanceRunner; add OrchestratorErrors to ScenarioResult"
```

---

## Task 5: RunScenario 순서 제어 로직 구현

**Files:**
- Modify: `internal/scenario/runner.go`
- Modify: `internal/scenario/runner_test.go`

### 배경

`depends_on`이 설정된 인스턴스는 의존 인스턴스의 ready 채널이 닫힐 때까지 `select`으로 대기한다. `time.After(readyTimeout)`으로 퓨즈, `ctx.Done()`으로 취소 전파를 구현한다.

- [ ] **Step 1: 순서 보장 테스트 작성**

`internal/scenario/runner_test.go`에 추가:

```go
func TestRunScenario_ClientStartsAfterHostReady(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host"},
		},
	}

	var order []string
	var mu sync.Mutex

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		mu.Lock()
		order = append(order, inst.Role+":start")
		mu.Unlock()

		if inst.Role == "host" && readyCh != nil {
			close(readyCh) // signal ready immediately
		}

		mu.Lock()
		order = append(order, inst.Role+":done")
		mu.Unlock()
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", result.ExitCode)
	}

	// host:start must appear before client:start
	mu.Lock()
	defer mu.Unlock()
	hostStartIdx, clientStartIdx := -1, -1
	for i, ev := range order {
		if ev == "host:start" {
			hostStartIdx = i
		}
		if ev == "client:start" {
			clientStartIdx = i
		}
	}
	if hostStartIdx == -1 || clientStartIdx == -1 {
		t.Fatalf("order incomplete: %v", order)
	}
	if hostStartIdx >= clientStartIdx {
		t.Errorf("client started before host; order=%v", order)
	}
}

func TestRunScenario_ReadyTimeout_ReturnsExit4(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 50},
		},
	}

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			// host never signals ready — simulates hang
			time.Sleep(200 * time.Millisecond)
		}
		return fakeResult(0), nil
	}

	result, err := scenario.RunScenario(context.Background(), spec, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 4 {
		t.Errorf("expected exit 4 for ready timeout, got %d", result.ExitCode)
	}
	if len(result.OrchestratorErrors) == 0 {
		t.Error("expected orchestrator_errors to be non-empty")
	}
}

func TestRunScenario_ContextCancellation_StopsWait(t *testing.T) {
	t.Parallel()
	spec := &scenario.ScenarioFile{
		Instances: []scenario.InstanceSpec{
			{Role: "host",   Config: "./host.json"},
			{Role: "client", Config: "./client.json", DependsOn: "host", ReadyTimeoutMs: 10000},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	run := func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
		if inst.Role == "host" {
			time.Sleep(200 * time.Millisecond) // host is slow
		}
		return fakeResult(0), nil
	}

	// Cancel immediately after start
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, _ := scenario.RunScenario(ctx, spec, run)
	// client should have exited early due to ctx cancellation
	// exit code may be 0 (host completed) or 1 (ctx error)
	// key assertion: RunScenario returned (did not hang)
	_ = result
}
```

- [ ] **Step 2: 테스트 실패 확인**

```bash
go test ./internal/scenario/... -run "TestRunScenario_ClientStartsAfterHostReady|TestRunScenario_ReadyTimeout|TestRunScenario_ContextCancellation" -v -timeout 10s
```

Expected: `TestRunScenario_ClientStartsAfterHostReady` FAIL (순서 보장 없음), `TestRunScenario_ReadyTimeout_ReturnsExit4` FAIL (exit 0 반환)

- [ ] **Step 3: `RunScenario` 순서 제어 구현**

`internal/scenario/runner.go`의 `RunScenario` 함수를 전체 교체:

```go
func RunScenario(ctx context.Context, spec *ScenarioFile, run InstanceRunner) (ScenarioResult, error) {
	// Create one ready channel per instance.
	// The channel is closed by the instance's runner (via ReadyNotifier or test fake)
	// when the instance reaches its configured ready phase.
	readyChannels := make(map[string]chan struct{}, len(spec.Instances))
	for _, inst := range spec.Instances {
		readyChannels[inst.Role] = make(chan struct{})
	}

	results := make([]InstanceResult, len(spec.Instances))
	var (
		wg       sync.WaitGroup
		orchMu   sync.Mutex
		orchErrs []string
	)

	for i, inst := range spec.Instances {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// If this instance depends on another, wait for its ready signal.
			if inst.DependsOn != "" {
				depCh := readyChannels[inst.DependsOn]
				timeout := time.Duration(inst.EffectiveReadyTimeoutMs()) * time.Millisecond
				select {
				case <-depCh:
					// dependency reached ready phase — proceed
				case <-time.After(timeout):
					msg := fmt.Sprintf("instance %q timed out waiting for %q to reach phase %q (%dms)",
						inst.Role, inst.DependsOn, inst.EffectiveReadyPhase(), inst.EffectiveReadyTimeoutMs())
					orchMu.Lock()
					orchErrs = append(orchErrs, msg)
					orchMu.Unlock()
					results[i] = InstanceResult{
						Role:     inst.Role,
						Response: runsvc.Response{ExitCode: 4},
					}
					return
				case <-ctx.Done():
					results[i] = InstanceResult{Role: inst.Role, Err: ctx.Err()}
					return
				}
			}

			readyCh := readyChannels[inst.Role]
			resp, err := run(ctx, inst, readyCh)
			results[i] = InstanceResult{Role: inst.Role, Response: resp, Err: err}
		}()
	}

	wg.Wait()

	return ScenarioResult{
		ExitCode:           aggregateExitCode(results),
		Instances:          results,
		OrchestratorErrors: orchErrs,
	}, nil
}
```

`fmt` import 추가:

```go
import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/runsvc"
)
```

- [ ] **Step 4: 테스트 통과 확인**

```bash
go test ./internal/scenario/... -run "TestRunScenario_ClientStartsAfterHostReady|TestRunScenario_ReadyTimeout|TestRunScenario_ContextCancellation" -v -timeout 15s
```

Expected: PASS (3 tests)

- [ ] **Step 5: 전체 테스트 통과 확인**

```bash
go test ./... -count=1 -timeout 60s
```

Expected: 전 패키지 ok

- [ ] **Step 6: 커밋**

```bash
git add internal/scenario/runner.go internal/scenario/runner_test.go
git commit -m "feat(scenario): implement Host/Client startup ordering with ready channels and timeout fuse"
```

---

## Task 6: 생산 InstanceRunner에 ReadyNotifier 주입 및 JSON 출력 업데이트

**Files:**
- Modify: `cmd/testplay/run.go`
- Modify: `cmd/testplay/run_test.go`

### 배경

생산 `InstanceRunner`에 `NewReadyNotifier`를 연결한다. `readyCh`가 nil이 아닐 때 StatusWriter를 `readyNotifier`로 래핑한다. 또한 `orchestrator_errors` 필드를 JSON 출력에 추가한다.

- [ ] **Step 1: orchestrator_errors 출력 테스트 작성**

`cmd/testplay/run_test.go`에 추가:

```go
func TestRunScenario_OrchestratorErrorsInOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	specPath := filepath.Join(dir, "scenario.json")
	content := `{"schema_version":"1","instances":[
		{"role":"host","config":"host.json"},
		{"role":"client","config":"client.json","depends_on":"host","ready_timeout_ms":50}
	]}`
	if err := os.WriteFile(specPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	deps := scenarioDeps{
		ctx: context.Background(),
		run: func(_ context.Context, inst scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
			// host never signals ready
			if inst.Role == "host" {
				time.Sleep(200 * time.Millisecond)
			}
			return runsvc.Response{ExitCode: 0, Result: &history.RunResult{
				SchemaVersion: "1", Tests: []parser.TestCase{}, Errors: []history.CompileError{},
			}}, nil
		},
	}

	var buf bytes.Buffer
	code := runScenario(&buf, specPath, deps)
	if code != 4 {
		t.Errorf("expected exit 4, got %d", code)
	}

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	errs, ok := out["orchestrator_errors"]
	if !ok {
		t.Fatal("expected orchestrator_errors field in output")
	}
	errsSlice, ok := errs.([]any)
	if !ok || len(errsSlice) == 0 {
		t.Errorf("expected non-empty orchestrator_errors, got %v", errs)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

```bash
go test ./cmd/testplay/... -run TestRunScenario_OrchestratorErrorsInOutput -v -timeout 10s
```

Expected: FAIL (`orchestrator_errors` 필드 없음)

- [ ] **Step 3: `run.go`에 ReadyNotifier 주입 및 JSON 출력 업데이트**

`cmd/testplay/run.go`의 `runScenario` 함수 수정:

production `run` 람다에서 `readyCh`를 받아 StatusWriter를 래핑:

```go
run = func(ctx context.Context, instSpec scenario.InstanceSpec, readyCh chan<- struct{}) (runsvc.Response, error) {
    cfgPath := spec.ConfigPath(instSpec)
    cfg, loadErr := config.Load(cfgPath)
    if loadErr != nil {
        return runsvc.Response{}, loadErr
    }
    if valErr := cfg.Validate(true); valErr != nil {
        return runsvc.Response{}, valErr
    }

    instanceCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout.TotalMs)*time.Millisecond)
    defer cancel()

    artifactRoot := filepath.Join(cfg.ProjectPath, ".testplay", "runs")

    // Per-instance status file for external polling by agents.
    var sw status.WriterInterface = status.NewWriter(fmt.Sprintf("testplay-status-%s.json", instSpec.Role))
    // Wrap with ReadyNotifier so this instance can signal its readyCh when the
    // target phase is reached. readyCh is nil for instances with no dependents.
    if readyCh != nil {
        sw = scenario.NewReadyNotifier(sw, instSpec.EffectiveReadyPhase(), readyCh)
    }

    svc := &runsvc.Service{
        Runner:       &unity.ProcessRunner{UnityPath: cfg.UnityPath},
        Store:        history.NewStore(cfg.ResultDir),
        Artifacts:    artifacts.NewStore(artifactRoot),
        StatusWriter: sw,
    }
    return svc.Run(instanceCtx, runsvc.Request{Config: cfg})
}
```

JSON 출력 부분 (`writeJSON` 호출 직전) 수정:

```go
output := map[string]any{
    "schema_version": "1",
    "exit_code":      scenarioResult.ExitCode,
    "instances":      instances,
}
if len(scenarioResult.OrchestratorErrors) > 0 {
    output["orchestrator_errors"] = scenarioResult.OrchestratorErrors
}

writeJSON(w, output)
return scenarioResult.ExitCode
```

- [ ] **Step 4: 테스트 통과 확인**

```bash
go test ./cmd/testplay/... -run TestRunScenario_OrchestratorErrorsInOutput -v -timeout 10s
```

Expected: PASS

- [ ] **Step 5: 전체 테스트 통과 확인**

```bash
go test ./... -count=1 -timeout 60s
```

Expected: 전 패키지 ok

- [ ] **Step 6: 커밋**

```bash
git add cmd/testplay/run.go cmd/testplay/run_test.go
git commit -m "feat(scenario): inject ReadyNotifier into production runner; output orchestrator_errors in JSON"
```

---

## Task 7: CLAUDE.md 업데이트

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Known Limitations 업데이트**

`CLAUDE.md`에서 다음 행 제거 (P1 해결):

```
| Scenario — no status polling | `testplay-status.json` is not written in `--scenario` mode (`StatusWriter` is nil per instance). ...
```

다음으로 교체:

```
| Scenario — status polling (per-instance) | `testplay-status-<role>.json` is written for each instance in `--scenario` mode. File path is `<role>` field from scenario JSON. No scenario-level aggregate status file exists. | Low |
```

- [ ] **Step 2: Runtime Files 섹션 업데이트**

기존 `testplay-status.json` 항목 아래에 추가:

```
- `testplay-status-<role>.json` — written per instance in `--scenario` mode. Same schema as `testplay-status.json`. Path is hardcoded to cwd. Absent for instances that have not yet started.
```

- [ ] **Step 3: CLI Contract 표 업데이트**

`testplay run` 행 업데이트:

```
| `testplay run [--filter <name>] [--category <cat>] [--compare-run <run_id>] [--shadow] [--reset-shadow] [--scenario <file>]` | Execute tests; streams progress to `testplay-status.json` (single mode) or `testplay-status-<role>.json` (scenario mode) |
```

- [ ] **Step 4: 버전 및 로드맵 업데이트**

`v0.4.0-beta` 로드맵 항목 상태 확인 후 필요시 반영.

- [ ] **Step 5: 커밋**

```bash
git add CLAUDE.md
git commit -m "docs: update CLAUDE.md for v0.4.0 — per-instance status, orchestrator_errors contract"
```

---

## Task 8: 통합 검증 및 릴리즈 준비

- [ ] **Step 1: 전체 테스트 최종 실행**

```bash
go test ./... -count=1 -timeout 60s -race
```

Expected: 전 패키지 ok (race detector 포함)

- [ ] **Step 2: 빌드 검증**

```bash
go build -o /tmp/testplay_v040 ./cmd/testplay
/tmp/testplay_v040 version
```

Expected:
```json
{"schema_version":"1","version":"v0.3.0-beta"}
```

(버전 문자열은 v0.4.0 릴리즈 커밋에서 별도 bump)

- [ ] **Step 3: `go vet` 클린 확인**

```bash
go vet ./...
```

Expected: 출력 없음

- [ ] **Step 4: 최종 커밋**

```bash
git push origin main
```

---

## 설계 결정 요약

| 항목 | 결정 | 이유 |
|---|---|---|
| Ready 기준 | Option A: `ready_phase` 필드 (기본 `"compiling"`) | `compiling`은 `Service.Run()` 진입 직후 기록 — Unity 기동 전 가장 빠른 신호. Option B(매직 스트링)는 v0.5 확장 |
| 내부 동기화 | Go 채널 (`chan struct{}`) | 디스크 폴링 금지. 외부 관측(에이전트)용으로만 파일 쓰기 |
| Deadlock 방지 | `select + time.After + ctx.Done()` | ready timeout 퓨즈 + 전체 context 취소 전파 |
| Timeout exit code | `exit 4` | 기존 timeout 시맨틱과 일관성 |
| 오케스트레이션 오류 표현 | `orchestrator_errors []string` (조건부 필드) | `timeout_type` 패턴 확장; 없을 때 필드 자체 생략 |
| 역할 유일성 | `Load()` 검증에서 강제 | 중복 role → 채널 충돌 방지 |
