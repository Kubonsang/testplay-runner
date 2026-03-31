# Core v0.2.2 Architecture Blueprint: Hybrid Execution & Shadow Workspace

Created: 2026-03-27
Target Version: v0.2.0
Revision: Artifact 직접 쓰기, UserSettings 제외, 플래그 명확화, .gitignore 자동 처리 반영 (Final)

## 1. Core Philosophy & Decision

`fastplay`는 "에디터를 조작하는 도구"가 아니라 "결정론적인(Deterministic) 테스트 러너"라는 정체성을 유지한다. 에디터 락(Lock) 문제를 해결하기 위해, 원본을 완벽히 보호하면서 격리된 백그라운드 환경을 구축하는 **Shadow Workspace** 방식을 공식 Fallback 백엔드로 채택한다.

유니티 내부 브릿지(Editor-Attached) 방식은 기각한다. 코어 실행은 항상 배치(Batch) 모드이며, Shadow Workspace는 Direct Batch와 동일한 JSON/exit code/status/artifact 계약을 유지하는 호환 레이어다.

## 2. Execution Backends (Auto Fallback)

`fastplay run` 명령어는 다음 두 가지 백엔드 중 하나를 상황에 맞게 자동(Auto)으로 선택한다.

| 백엔드 | 조건 | 동작 |
|---|---|---|
| **Direct Batch Mode** (기본값) | `Temp/UnityLockfile` 부재 | 현재 경로에서 직접 유니티 배치 모드 실행. 오버헤드 0. CI 환경과 동일. |
| **Shadow Workspace Mode** (Fallback) | `Temp/UnityLockfile` 존재 | `.fastplay-shadow/` 격리 워크스페이스 구축 후 실행. |

두 백엔드의 stdout JSON, exit code, status, artifact 계약은 동일하다. 사용자와 에이전트는 백엔드 선택을 인식할 필요가 없다.

## 3. Technical Requirements & Implementation Guide

### A. Hybrid Copy/Link Strategy (격리와 성능의 타협)

유니티의 쓰기(Write) 동작으로부터 원본 에디터 상태를 보호하기 위해 실용적 분리 기준을 적용한다.

| 디렉토리 | 처리 방식 | 이유 |
|---|---|---|
| `Assets/` | **물리적 복사 (Copy)** | Unity의 `.meta` 파일 생성/수정이 원본에 반영되는 것을 방지 |
| `ProjectSettings/` | **물리적 복사 (Copy)** | Unity batch가 설정 파일을 수정할 수 있음 |
| `Packages/` | **OS별 링크** (Junction / Symlink) | 쓰기 발생 확률 극히 낮음. 링크로 처리해 복사 비용 절감 |
| `Library/` | **Shadow 전용 (영구 보존)** | 최초 1회 전체 임포트 후 증분 컴파일로 재사용 |
| `Temp/` | **Shadow 전용 (매번 삭제)** | 실행마다 초기화 필요 |
| `UserSettings/` | **제외 (Ignore)** | 에디터 개인 설정. 테스트 실행에 불필요 |

**OS별 링크 구현:**
- **macOS / Linux:** `os.Symlink`
- **Windows:** Directory Junction (`exec.Command("cmd", "/c", "mklink", "/J", target, source)`) — 관리자 권한 불필요

### B. Output Injection & Direct Artifact Writing

**수직 관통 의존성 주입 (Option C):**
`cmd layer(run.go)`부터 status writer까지 `output_dir`을 명시적으로 전달하는 구조적 리팩토링을 수행한다. Direct Batch 경로도 이 변경의 영향권에 들어가므로 기존 동작을 회귀 테스트로 보호한 후 진행한다.

**Artifact 직접 쓰기:**
Shadow 실행 시에도 유니티 실행 인자인 `-testResults`와 로그 출력 경로는 **원본 프로젝트의 `.fastplay/runs/<run_id>/`** 를 직접 가리키도록 강제한다. 별도의 파일 복사 과정 없이 결과물이 원본 위치에 즉시 생성된다.

**경로 세탁 (Path Remapping):**
stdout으로 출력되는 JSON 결과의 경로/스택 필드는 다음으로 일괄 치환한다.

```go
output = strings.ReplaceAll(output, shadowRoot, sourceRoot)
```

이를 통해 사용자와 에이전트가 Shadow의 존재를 인식하지 못하도록 완전히 추상화한다.

### C. System Resource Optimization

섀도우 실행 시 RAM/CPU 오버헤드 최소화를 위해 실행 인자를 명확히 구분하여 주입한다.

| 플래그 | 적용 범위 | 목적 |
|---|---|---|
| `-batchmode` | Direct / Shadow 공통 | UI 비활성화 |
| `-nographics` | **Shadow 전용** | GPU 및 렌더링 초기화 건너뜀 |
| `-disable-assembly-updater` | **Shadow 전용** | 구형 스크립트 변환기 비활성화로 초기 로딩 스파이크 방지 |

## 4. Shadow Recovery & Lifecycle

### 복구 전략

영구 보존되는 `Library` 캐시가 오염되거나 유니티 버전이 변경될 경우를 대비하여 명확한 탈출구를 제공한다.

```bash
fastplay run --reset-shadow
```

실행 전 기존 `.fastplay-shadow/` 디렉토리를 강제 삭제하고 새로 구축한다. 다음 실행은 Library 전체를 재임포트하므로 최초 실행과 동일한 시간이 소요된다.

### .gitignore 자동 처리

`.fastplay-shadow/` 디렉토리는 프로젝트 루트에 생성된다. 이 디렉토리(Library 포함, 수 GB)가 git에 추적되는 것을 방지하기 위해, Shadow Workspace를 최초 생성할 때 원본 프로젝트의 `.gitignore`에 다음 항목이 없으면 자동으로 추가한다.

```
.fastplay-shadow/
```

### Lifecycle 요약

```
최초 실행 (Shadow 모드):
  1. .fastplay-shadow/ 생성
  2. Assets/, ProjectSettings/ 복사
  3. Packages/ 링크
  4. Library/ 빈 상태로 초기화 (유니티가 전체 임포트)
  5. .gitignore에 .fastplay-shadow/ 자동 추가
  6. 실행 완료 후 Temp/ 삭제, Library/ 보존

이후 실행 (Shadow 모드):
  1. Assets/, ProjectSettings/ 재복사 (변경 반영)
  2. Library/ 기존 캐시 재사용 → 증분 컴파일만 수행
  3. Temp/ 삭제 후 재생성

오염/버전 변경 시:
  fastplay run --reset-shadow → 전체 재구축
```

## 5. 범위 외 항목 (Out of Scope)

다음 항목은 코어 범위가 아니며 공식 애드온 영역으로 분리한다.

- 멀티프로세스 오케스트레이션 (NGO 등)
- 네트워크 환경 테스트 하네스
- Unsaved editor state 반영
- Shadow Library 캐시 크기 관리 자동화
- Git worktree 기반 Shadow 최적화
