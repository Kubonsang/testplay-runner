# testplay-runner Image Workspace + ReFS Block Clone 연구·구현 명세 v0.3

- **문서 유형:** Engineering Research Plan + Technical Design + Validation Protocol
- **상태:** Proposed / Physical Image baseline validated / ReFS spike pending
- **작성일:** 2026-07-30
- **대상 저장소:** `Kubonsang/testplay-runner`
- **주 독자:** testplay-runner 개발 Agent, Windows Storage 구현자, Reviewer
- **연계 문서:** `HoneyBee_Windows_CoW_Workspace_Product_Architecture_Spec_v0.2.md`
- **이전 문서:** `TestPlay_Runner_ReFS_Block_Clone_Research_Implementation_Spec_v0.2.md`

---

## 0. 문서의 역할

이 문서는 현재 검증된 **불변 Library Image**와 향후 구현할 **ReFS Block Clone Materializer**를 하나의 연속된 Workspace 구조로 정의한다.

- **불변 Image(Immutable Image):** 생성과 검증이 끝난 뒤 내용을 변경하지 않는 기준 `Library` 저장본이다.
- **Materializer:** 기준 Image를 Unity가 실제로 실행할 수 있는 쓰기 가능한 `Library`로 준비하는 구체적인 파일 배치 구현이다.
- **ReFS Block Clone:** Windows ReFS 파일시스템에서 파일의 디스크 블록을 공유해 빠르게 복제하고, 변경된 블록만 새로 저장하는 기능이다.
- **CoW(Copy-on-Write, 쓰기 시 복사):** 복제 시 전체 데이터를 즉시 복사하지 않고 원본 블록을 공유하다가 파일이 변경될 때 변경된 부분만 별도로 저장하는 방식이다.

v0.2는 `ReFS CoW Backend`를 독립 Backend로 가정했다. 그러나 Physical Image 실험을 통해 다음 구조가 더 적절하다는 근거가 생겼다.

```text
Image 생명주기
Key / Metadata / Lock / Staging / Validation / Commit / Quarantine
                         │
                         ▼
Library Materializer
Physical Copy / ReFS Block Clone / APFS Clone / Linux reflink
```

따라서 v0.3은 **검증된 Image 생명주기는 재사용하고, 느린 Physical Copy Materializer만 ReFS Block Clone으로 교체하는 방향**을 우선한다.

이번 문서는 ReFS, APFS Clone 또는 Linux reflink 구현 완료를 선언하지 않는다. Physical Image 결과는 검증된 기준선이며, 나머지는 후속 플랫폼별 Spike 후보이다.

---

## 0.1 용어 설명 규칙

새 기술 용어는 처음 등장할 때 짧게 뜻을 설명한다.

- **Parity(결과 동등성):** 같은 입력에서 기존 Backend와 새 Backend가 동일한 테스트 목록, 성공·실패 결과와 종료 코드를 반환하는지 확인하는 기준이다.
- **Atomic Rename(원자적 이름 변경):** 완성된 임시 결과를 최종 경로로 한 번에 이동해 미완성 상태가 정상 결과처럼 노출되지 않게 하는 방식이다.
- **Quarantine(격리 보관):** 손상됐거나 완료 여부가 불명확한 Image나 Workspace를 정상 경로에서 분리해 재사용하지 못하게 보관하는 처리다.
- **Integrity Hash(무결성 해시):** 파일 내용이 생성 당시와 동일한지 확인하기 위해 계산하는 식별값이다.
- **Logical Size(논리 크기):** 파일 내용 길이의 합계다.
- **Allocated/Physical Size(할당·물리 크기):** 저장장치에서 실제로 점유한 공간이다.
- **Silent Fallback:** 선택한 Backend가 실패했는데 사용자에게 알리지 않고 다른 Backend로 몰래 재실행하는 동작이다.
- **Control Plane(제어 계층):** 생성, 잠금, 검증, 상태 전이와 실패 복구를 관리하는 부분이다.
- **Data Plane(데이터 처리 계층):** 실제 파일 데이터를 복사하거나 Clone하는 부분이다.

---

## 0.2 비협상 제품 원칙 — Standalone CLI

`testplay-runner`는 Honey Bee의 하위 모듈이나 전용 Sidecar가 아니다. **독립적으로 설치·실행·배포되는 CLI**라는 기존 철학을 유지한다. Honey Bee는 가능한 여러 호출자 중 하나일 뿐이다.

```text
Terminal / CI / Script / Other Agent IDE / Honey Bee
                        │
                        ▼
                 testplay CLI
                        │
       Bridge | Legacy | Image Workspace
```

다음 조건은 모든 구현 단계에서 MUST다.

1. `testplay`의 `check`, `list`, `run`, `result`, Bridge, Legacy와 Image 기능은 Honey Bee 설치 없이 동작한다.
2. testplay-runner는 Honey Bee Extension, Node Runtime, Session 모델, Workspace UI, Honey Bee 설정 파일 또는 내부 TypeScript package를 import하거나 실행 전제조건으로 요구하지 않는다.
3. 모든 기능은 CLI argument, testplay 자체 설정, environment variable 또는 파일 기반 공개 계약으로 호출 가능해야 한다.
4. Honey Bee가 이미 준비한 Workspace를 전달할 수는 있지만, 이는 일반적인 `existing workspace` 입력 모드이며 Honey Bee 전용 metadata가 없어도 사용 가능해야 한다.
5. JSON schema, exit code, result ownership과 replay-safety는 testplay-runner가 독립적으로 소유한다.
6. Image와 ReFS 최적화는 선택 기능이다. 지원되지 않는 환경에서도 기존 Bridge/Legacy 경로와 CLI 계약은 유지된다.
7. 공통 Storage Helper를 추출하더라도 Honey Bee에 종속되지 않는 기술 중립 컴포넌트여야 한다.
8. 릴리스와 CI에는 Honey Bee가 존재하지 않는 깨끗한 환경의 standalone smoke test를 포함한다.

금지되는 결합:

```text
X testplay run → Honey Bee Extension activation 필요
X testplay package → @honeybee/* import
X testplay config → Honey Bee Session ID 필수
X Storage Helper → Honey Bee 전용 IPC만 제공
X testplay release → Honey Bee 배포 주기와 lockstep 강제
```

허용되는 결합:

```text
O Honey Bee → testplay executable 호출
O Honey Bee → 공개 JSON/exit-code 결과 소비
O Honey Bee → 일반 workspace path와 shard manifest 전달
O 양쪽 → neutral versioned Storage Helper 사용
```

---

# 1. 현재 Pipeline과 구현 상태

## 1.1 Bridge

현재 `com.testplay.bridge`는 다음 특성을 가진다.

- Unity 6의 열린 Editor에서 `TestRunnerApi` 실행
- EditMode 한정
- 한 번에 한 run
- 실행 전 명확한 거절은 cold fallback 가능
- 실행 가능성이 생긴 뒤 완료 여부가 모호하면 exit 9
- CLI JSON/exit-code 계약 유지

Bridge는 로컬 단일 실행 최적화로 가치가 있으나 복수 Worktree 병렬 실행의 기반은 아니다.

## 1.2 Legacy Shadow

기존 Shadow는 `--shadow`, `--reset-shadow` 또는 `Temp/UnityLockfile` 존재 시 선택된다.

```text
Assets 물리 복사
→ ProjectSettings 물리 복사
→ Packages 링크
→ Legacy cache가 유효하면 Library 복사
→ Unity 실행
→ 결과 경로를 원본 경로로 remap
→ 성공 시 Library 전체 write-back
→ Shadow cleanup
```

**Write-back**은 실행 중 변경된 `Library`를 다음 실행에서 재사용하기 위해 Cache에 다시 기록하는 작업이다.

GNF_ warm 실행에서는 Legacy cache write-back이 평균 `98.489초`를 사용해 가장 큰 단일 병목이었다.

## 1.3 Physical Image Backend — 구현 완료

현재 다음 명시적 Backend 선택이 구현되어 있다.

```bash
testplay run --workspace-backend=legacy
testplay run --workspace-backend=image
```

외장 Image 저장소도 지원한다.

```bash
testplay run \
  --workspace-backend=image \
  --workspace-store-root="<store-root>"
```

Image Backend는 다음을 제공한다.

- Unity 버전, Package 파일, 전체 `ProjectSettings`, 프로젝트 identity 등을 포함한 SHA-256 Image Key
- `valid`, `missing`, `stale`, `corrupt`, `unsupported` 상태
- `staging → 전체 검증 → COMPLETE → atomic rename`
- PID/token 기반 생성 Lock
- Corrupt Image Quarantine
- Physical Copy 기반 writable `Library` Materialization
- 명시적 Image 실패 시 오류 반환
- Silent Legacy fallback 없음
- Image 전용 `--clear-cache`
- `--keep-workspace`
- 기존 결과 계약을 깨지 않는 additive `workspace_metrics`

현재 구현은 **Physical Copy**다. CoW가 아니며, 실행용 `Library` 크기만큼 실제 공간과 복사 시간이 필요하다.

## 1.4 현재 구조의 판정

```text
Image Control Plane
Key / Metadata / Lock / Staging / Validation / Commit / Quarantine
→ 정확성과 재사용 흐름이 검증됨

Image Data Plane
Physical Copy Materialization
→ Warm 전체 성능은 개선했으나 Workspace 준비 SLO에는 미달
```

---

# 2. Validated Physical Image Baseline

## 2.1 실험 환경

```text
Project: GNF_
Unity: 6000.3.8f1
Concurrency: 1
Comparison: Legacy/Image cold 1회 + warm 10회
Test result per run: 17 passed, 0 failed
```

실제 Unity E2E:

```bash
go test -tags=e2e ./e2e \
  -run '^TestE2E_LibraryImageParity$' \
  -v -count=1
```

E2E 결과는 PASS였으며 측정 실행은 27.46초였다.

## 2.2 검증 결과

- 비교 실행 22/22 exit 0
- 매 실행 17 passed, 0 failed
- 테스트 목록과 결과 동일
- Stable result digest 22개 모두 동일
- Image warm reuse 10/10 `valid`
- fallback 0회
- GNF_ 원본 입력 hash 실행 전후 동일
- 모든 warm 실행에서 Base Image 전체 digest 동일
- 잔여 Shadow Workspace와 Image Lock 없음

**Stable result digest**는 테스트 결과를 정규화한 뒤 계산한 해시이며, 22개가 모두 같았다는 것은 Backend가 달라도 관찰된 결과 내용이 같았음을 의미한다.

## 2.3 Cold Benchmark

| Cold 지표 | Legacy | Image |
|---|---:|---:|
| 전체 시간 | 240.80s | 398.14s |
| Workspace 준비 | 3.859s | 41.238s |
| Image 생성 | — | 267.986s |
| Unity 테스트 실행 | 198.312s | 49.697s |
| 추가 물리 피크 | 22.963GB | 23.172GB |
| 결과 | 17/17 | 17/17 |

Cold Image는 Legacy보다 `157.34초`, 비율로는 약 `65.3%` 느렸다.

## 2.4 Warm 10회 평균 Benchmark

| Warm 10회 평균 | Legacy | Image | 변화 |
|---|---:|---:|---:|
| 전체 시간 | 175.774s | 119.404s | **-32.1%** |
| Workspace 준비 | 18.400s | 38.669s | **+110.2%** |
| Library materialization | 13.847s | 11.927s | -13.9% |
| Unity 실행 | 49.128s | 48.624s | -1.0% |
| Cache write-back | 98.489s | 0s | -100% |
| 준비+write-back+cleanup | 124.966s | 45.669s | **-63.5%** |
| 추가 물리 피크 | 39.770GB | 23.171GB | **-41.7%** |
| 실패 | 0/10 | 0/10 | 동일 |

Image는 Unity 테스트 실행 자체를 크게 가속하지 않았다. 주된 개선은 Legacy의 전체 `Library` write-back을 제거한 데서 발생했다.

## 2.5 단순 손익분기점

```text
Cold 추가비용
398.14 - 240.80 = 157.34초

Warm 1회 절감
175.774 - 119.404 = 56.37초

157.34 ÷ 56.37 ≈ 2.79
```

동일 Image Key가 유지되고 비슷한 Warm 비용이 반복된다는 단순 가정에서는 약 3회 Warm reuse 후 초기 생성비용을 회수한다.

이는 제품 보장이 아니라 현재 Benchmark를 이용한 단순 추정이다.

## 2.6 Verdict

```text
Image lifecycle correctness: PROVEN
Physical Copy Image performance: PROMISING
Default backend eligibility: NOT YET
ReFS materializer readiness: READY FOR SPIKE
```

- 정확성과 오염 방지는 강하게 검증됐다.
- 반복 Warm 실행에서는 실용적인 개선이 확인됐다.
- Workspace 준비시간과 Cold 비용은 목표에 미달했다.
- 기본 Backend 변경은 정당화되지 않는다.

---

# 3. 문제 정의

목표는 Image Backend를 폐기하는 것이 아니라, 검증된 생명주기 위에서 Physical Copy를 CoW로 교체하는 것이다.

```text
현재
Valid Immutable Image
→ Physical Copy
→ Writable Worker Library

목표
Valid Immutable Image
→ ReFS Block Clone
→ Writable Worker Library
```

핵심 문제:

1. Image를 다시 검증하고 물리 복사하는 준비시간이 길다.
2. Physical Copy는 Worker마다 `Library` 전체 공간이 필요하다.
3. 여러 Worker를 동시에 만들면 복사 I/O와 저장공간이 선형으로 증가한다.
4. Unity `Library/Bee`의 절대경로가 다른 Worker 경로에서 재컴파일·재임포트를 유발할 수 있다.
5. 결과 Parity와 Source/Image/Sibling 격리를 유지해야 한다.

---

# 4. 목표와 합격 기준

## 4.1 H0 — Physical Image Baseline

상태: **완료 / PROMISING**

- Image Lifecycle correctness: 통과
- Legacy/Image Parity: 통과
- Source/Base Image 오염 방지: 통과
- Warm 전체 시간 개선: 통과
- Workspace 준비 SLO: 미달
- Physical Allocation 효율: 미달

## 4.2 H1 — ReFS Clone 준비 시간

GNF_ 약 5.3 GiB급 `Library`를 **5초 이내**에 Materialize할 수 있다.

- 목표: 5초
- 실패 상한: 10초
- 비교 기준: Physical Image 평균 11.927초 Materialization 및 38.669초 전체 Workspace 준비

## 4.3 H2 — 생성 직후 물리 용량

Clone 직후 Worker의 실제 Volume 추가 사용량은 **250 MiB 이하**다.

## 4.4 H3 — 테스트 후 물리 용량

일반 EditMode 테스트 1회 후 Worker의 누적 추가 물리량은 **1 GiB 이하**다.

## 4.5 H4 — 격리

Worker에서 파일을 수정해도 다음 내용은 변하지 않는다.

- Source Project
- Base Image
- Sibling Worker

## 4.6 H5 — 결과 Parity

같은 Commit, Filter, Category와 Test Platform에서 Legacy, Physical Image와 ReFS Image는 다음 결과가 같아야 한다.

- exit code
- executed test set
- per-test outcome
- failure classification
- normalized JSON contract
- `new_failures`
- stable result digest

## 4.7 H6 — 병렬성

4개 Worker가 병렬로 실행되어도:

- 준비 30초 이내
- 총 추가 물리량 4 GiB 이하
- 중복·누락 테스트 0건
- Source/Image/Sibling 오염 0건

## 4.8 종합 판정

- **PROVEN:** H1~H6 모두 통과
- **PROMISING:** H4/H5 필수 통과, H1~H3 또는 H6 추가 최적화 필요
- **NOT YET VIABLE:** 격리/Parity 실패 또는 물리 용량 이점 없음

---

# 5. 목표 아키텍처

## 5.1 전체 구조

```text
                         ┌─ Bridge Backend
testplay run ─ selector ─┼─ Legacy Shadow Backend
                         └─ Image Workspace Backend
                                      │
                     ┌────────────────┴────────────────┐
                     │                                 │
                Image Store                   Library Materializer
          Key / Lock / Metadata             Physical Copy
          Staging / Validation              ReFS Block Clone
          Commit / Quarantine               향후 플랫폼별 Clone
```

Bridge는 열린 Editor 실행 경로이고, Image Workspace와 같은 파일 Materialization Backend로 억지로 통합하지 않는다.

## 5.2 Image Store 책임

Image Store는 다음을 소유한다.

- Image Key 계산
- 상태 판정
- Key-scoped Lock
- Staging 경로
- Metadata와 Manifest
- Integrity Verification
- `COMPLETE` Marker
- Atomic Commit
- Corruption Quarantine
- Stale 판정
- 향후 Retention/GC

Image Store는 Unity Test 결과, NUnit 의미론, Bridge 정책을 알지 않는다.

## 5.3 Library Materializer 책임

```go
type LibraryMaterializer interface {
    ID() string

    Probe(
        ctx context.Context,
        req MaterializerProbeRequest,
    ) (MaterializerCapability, error)

    Materialize(
        ctx context.Context,
        req MaterializeRequest,
    ) (*MaterializationResult, error)
}
```

초기 구현:

```text
physical-copy        — 현재 구현·검증됨
refs-block-clone     — 후속 Windows Spike
apfs-clone           — 향후 macOS 후보, 미구현
linux-reflink        — 향후 Linux 후보, 미구현
```

`Release`와 cleanup 책임을 Materializer에 둘지 Workspace Backend에 둘지는 코드 구조를 검토해 결정한다. 단, Image Lifecycle과 Unity Process Lifecycle을 하나의 거대한 함수에 결합하지 않는다.

## 5.4 Backend와 Materializer CLI

현재 구현:

```bash
testplay run --workspace-backend=image
```

v0.3에서는 다음 두 후보를 비교하며 최종 CLI는 Interface 분리 후 확정한다.

### 후보 A — Image + Materializer 분리

```bash
testplay run \
  --workspace-backend=image \
  --workspace-materializer=refs-block-clone
```

장점:

- 검증된 Image 생명주기 재사용
- Physical Copy와 ReFS를 같은 결과 계약으로 비교 가능
- 플랫폼별 Materializer 확장 가능
- 책임 중복 감소

### 후보 B — ReFS를 별도 Backend로 노출

```bash
testplay run --workspace-backend=refs-cow
```

장점:

- 사용자가 최적화 경로를 명확히 인식
- 기존 v0.2 구조와 단순 호환

단점:

- Image Store 생명주기 중복 위험
- Cache/Image/ReFS 상태 모델이 분산될 수 있음

현재 권장 방향은 후보 A다. 실제 코드 Interface와 호환성을 확인하기 전 CLI를 확정하지 않는다.

## 5.5 결과 JSON

현재 구현은 기존 top-level `backend`를 호환성을 위해 `"shadow"`로
유지하고, 실제 Workspace 구현은 additive `workspace_metrics` 안의
camelCase 필드로 구분한다.

```json
{
  "backend": "shadow",
  "workspace_metrics": {
    "workspaceBackend": "image",
    "imageStatus": "valid",
    "imageResolutionStatus": "valid",
    "imageKey": "sha256...",
    "libraryMaterializationMs": 11927,
    "fallbackUsed": false
  }
}
```

Materializer Interface 분리 후 검토할 additive 후보는 다음과 같다.
아래 필드는 아직 구현되지 않았다.

```json
{
  "backend": "shadow",
  "workspace_metrics": {
    "workspaceBackend": "image",
    "imageStatus": "valid",
    "materializer": "refs-block-clone",
    "cloneMs": 842,
    "logicalBytes": 5690831667,
    "physicalBytesDelta": 81788928,
    "pathStrategy": "common-image"
  }
}
```

---

# 6. Image Key와 상태 모델

## 6.1 현재 Image Key와 향후 입력

현재 Image schema v1의 실제 입력은 다음과 같다.

- Image schema version
- `ProjectSettings/ProjectVersion.txt`의 Unity Editor version
- 설정된 Unity 실행 파일 경로 hash
- `Packages/manifest.json` hash
- `Packages/packages-lock.json` hash 또는 명시적 `missing`
- 전체 `ProjectSettings` tree hash
- Host build identity (`GOOS/GOARCH`)
- Scripting Backend 항목 또는 전체 ProjectSettings hash coverage marker
- Canonical absolute project identity hash

향후 명시적 Build Target, Test Platform 또는 Materializer compatibility가
runner 입력으로 추가되면 다음 항목을 Image Key에 포함할지 검토한다.

- 프로젝트 identity
- `ProjectSettings/ProjectVersion.txt`
- `Packages/manifest.json`
- `Packages/packages-lock.json`
- 전체 또는 선정된 `ProjectSettings` hash
- Build Target
- Test Platform
- Scripting Backend 등 실행에 영향을 주는 설정
- Image schema version
- Materialization compatibility version
- Path strategy identifier

현재 schema v1에는 별도의 Test Platform, Materialization compatibility
version 또는 Path strategy identifier가 없다. 이 항목들이 아직
구현됐다고 표현하지 않는다. 누락으로 stale Image를 잘못 재사용하는
것보다 과도한 invalidation을 우선한다.

## 6.2 상태

```text
missing
creating
valid
stale
corrupt
unsupported
quarantined
```

## 6.3 상태 원칙

- `COMPLETE`와 검증된 Metadata가 모두 있어야 `valid`
- Staging 디렉터리는 `valid`로 노출하지 않음
- Lock 소유권이 불명확하면 자동 삭제 금지
- Digest 불일치는 `corrupt`
- 설정 Fingerprint 불일치는 `stale`
- Corrupt Image는 정상 경로에서 Quarantine
- 명시적 Image 실행에서 실패 시 Legacy로 자동 재실행 금지

---

# 7. Integrity Verification 최적화

현재 `Resolve`와 `Materialize` 전 전체 Integrity Hash가 중복될 가능성이 있다.

안전성을 약화하기 전에 단계별 시간을 측정한다.

## 7.1 필수 계측

```text
image_resolve_ms
image_metadata_verify_ms
image_full_hash_ms
library_materialize_ms
workspace_verify_ms
cleanup_ms
```

## 7.2 권장 검증 계층

### Image 생성 시

```text
전체 파일 Full Hash
→ Manifest 생성
→ COMPLETE 기록
→ Atomic Commit
```

### 정상 Warm Resolve 시

```text
COMPLETE 확인
→ Metadata와 Schema 확인
→ Manifest 자체 Hash 확인
→ 파일 수/논리 크기/선택 Metadata 확인
```

### Materialization 후

```text
작업 오류 확인
→ 파일 수와 논리 크기 확인
→ 선택적 Sample Hash
→ Materializer별 무결성 검사
```

### Full Audit 조건

- 비정상 종료 후
- 외부 수정 탐지
- 정기 Audit
- 명시적 `--verify-full`
- Sample 검증 실패

Full Hash 제거는 Benchmark 근거 없이 수행하지 않는다.

---

# 8. Storage와 파일시스템 정책

## 8.1 exFAT 관측 결과

Image 논리 크기 약 4.87GB가 실험한 exFAT 저장소에서 물리 약 16.60GB를 사용했다.

```text
allocation amplification ≈ 16.60 / 4.87 ≈ 3.41배
```

또한 외장 exFAT에서 Unity `Library`를 직접 실행했을 때 `MDB_BAD_RSLOT`과 Native Crash가 재현됐다.

이 결과를 모든 exFAT 환경으로 일반화하지 않는다. 현재 장비에서 확인된 사실로 기록한다.

현재 권장 정책:

```text
외장 exFAT
→ 정지된 Image 보관만 조건부 허용
→ 실제 할당 크기 증폭 경고

Unity 실행 Workspace
→ 검증된 내부 APFS 또는 지원 파일시스템 사용
```

## 8.2 Storage Probe

향후 Store Root 선택 시 다음을 측정한다.

- Filesystem 종류
- Allocation unit
- Logical/Allocated size
- 많은 작은 파일의 증폭률
- Atomic Rename
- File Lock
- Long Path
- Unity 직접 실행 가능 여부
- Clone API 지원 여부

예시:

```json
{
  "filesystem": "exFAT",
  "logical_bytes": 1073741824,
  "allocated_bytes": 3516504473,
  "allocation_amplification": 3.27,
  "unity_execution_supported": false,
  "recommendation": "image-storage-only"
}
```

## 8.3 지원 정책

- Store Root와 실행 Workspace Root는 달라도 된다.
- ReFS Block Clone을 사용할 경우 Base Image와 Destination은 같은 ReFS Volume이어야 한다.
- Store Root가 다른 Volume이면 Physical Copy 또는 명시적 실패가 필요하다.
- 자동으로 사용자의 기존 Volume을 Format하지 않는다.
- Logical Size와 Physical Size를 UI/JSON에서 구분한다.

---

# 9. Windows ReFS 전제조건

## 9.1 지원 후보

- Windows 11 24H2 이상
- Windows Server 2025 이상
- ReFS Dev Drive
- Source Image와 Destination이 동일 ReFS Volume

OS 버전 문자열이나 Filesystem 이름만으로 지원을 선언하지 않는다. 작은 실제 Clone Probe가 최종 Capability 판정이다.

## 9.2 Dev Drive 형태

1. 사용자가 준비한 ReFS Dev Drive/Partition
2. 동적 확장 VHDX 기반 ReFS Dev Drive

**VHDX**는 하나의 파일을 Windows에서 가상 디스크처럼 마운트하는 형식이다. 동적 확장 VHDX는 설정한 최대 용량 전체를 즉시 차지하지 않고 실제 데이터가 늘어날수록 파일 크기가 커진다.

## 9.3 Preflight

```json
{
  "os": "Windows 11 24H2",
  "filesystem": "ReFS",
  "source_volume_id": "...",
  "destination_volume_id": "...",
  "same_volume": true,
  "block_clone_probe": "passed",
  "host_free_bytes": 0,
  "workspace_volume_free_bytes": 0,
  "cluster_size": 4096
}
```

---

# 10. ReFS Block Clone Materializer 구현

## 10.1 구현 언어

1차 구현은 testplay-runner와 동일한 Go로 한다.

- `//go:build windows`
- `golang.org/x/sys/windows`
- `CreateFileW`
- `DeviceIoControl`
- `SetEndOfFile`
- 공식 Win32 API 우선
- 어셈블리어 불필요
- C/C++ DLL도 1차 Spike에서는 불필요

Go API로 안정적인 구현이 불가능하다는 측정 근거가 생긴 경우에만 별도 Native Helper 추출을 검토한다.

## 10.2 API 후보

우선:

```text
FSCTL_DUPLICATE_EXTENTS_TO_FILE_EX
```

필요 시:

```text
FSCTL_DUPLICATE_EXTENTS_TO_FILE
```

지원 여부는 실제 Probe 결과로 판정한다.

## 10.3 파일 Clone 알고리즘

각 Regular File에 대해:

```text
1. Source Handle Open
2. Destination 생성
3. Destination Logical Size를 Source와 동일하게 설정
4. Volume Cluster Size 확인
5. Cluster-aligned Prefix 계산
6. 4 GiB 미만 Chunk로 DeviceIoControl 반복
7. 마지막 비정렬 Tail은 Buffered Physical Copy
8. Timestamp와 Attribute 복원
9. File Size와 선택적 Sample Hash 검증
```

권장 Chunk는 1 GiB 또는 2 GiB로 시작하고 Benchmark로 결정한다. 한 번의 Clone Length는 4 GiB 미만으로 제한한다.

## 10.4 파일 유형 정책

| 유형 | 초기 정책 |
|---|---|
| Directory | 생성 후 순회 |
| Regular File | Block Clone |
| Zero-length | 빈 Destination 생성 |
| Symlink/Reparse Point | 안전하게 재현 가능하면 재현, 아니면 명시적 unsupported |
| Sparse File | Sparse/Integrity 설정 일치 후 Clone, 불가능하면 명시적 File Fallback 또는 실패 |
| ADS | 탐지 후 보고, 초기에는 unsupported 가능 |
| Locked File | 명확한 Lock Failure 반환 |

예상하지 못한 Reparse Point를 조용히 따라가 Source Root 밖을 복제하지 않는다.

## 10.5 Long Path

한글, 공백, 괄호와 260자 초과 Windows Path를 처리한다.

내부 Win32 Path는 필요한 경우 `\\?\` 형식을 사용하되 사용자 JSON에는 읽을 수 있는 Canonical Path를 반환한다.

## 10.6 Cancellation

- 디렉터리 순회와 파일·Chunk 경계에서 Context Cancellation 확인
- 취소 후 Incomplete Marker 유지
- Destination은 `valid`나 `ready`로 노출하지 않음
- Cleanup은 Retry-safe
- 부분 Clone 결과는 Quarantine 또는 안전 삭제

---

# 11. Atomic Tree Materialization

```text
workers/<slot>/project/Library.__tmp_<operation-id>
→ .incomplete marker
→ materialize tree
→ verify
→ 기존 Library quarantine 또는 제거
→ atomic rename/move
→ complete manifest
```

디렉터리 전체의 완전한 Transaction을 가정하지 않는다. Marker와 상태 전이를 통해 복구한다.

Manifest 예시:

```json
{
  "schema_version": 1,
  "workspace_backend": "image",
  "materializer": "refs-block-clone-v1",
  "image_key": "sha256...",
  "source_path": "X:\\testplay\\images\\...\\Library",
  "destination_path": "X:\\testplay\\workers\\00\\project\\Library",
  "logical_bytes": 5690831667,
  "physical_bytes_delta": 83886080,
  "file_count": 123456,
  "created_at": "...",
  "verification": "passed"
}
```

---

# 12. Path Sensitivity 연구

Unity `Library/Bee`에는 절대경로 정보가 포함될 수 있다. Base Image를 다른 Worker 경로로 Materialize하면 재컴파일 또는 부분 재임포트가 발생할 수 있다.

## 12.1 실험 A — Common Image

```text
Image created from X:\testplay\image-build\project
→ Worker 00: X:\testplay\workers\00\project
→ Worker 01: X:\testplay\workers\01\project
```

측정:

- Unity Startup
- Import Count/Time
- Script Compile Time
- Bee DAG Rewrite 여부
- 수정된 `Library` File Top-N
- Test 전후 Physical Delta

## 12.2 실험 B — Slot별 Baseline Image

각 Worker는 영구 고정 경로를 가진다.

```text
X:\testplay\workers\00\project
X:\testplay\workers\01\project
```

최초 한 번 해당 경로에서 Warm-up한 뒤 Slot별 Baseline Image를 만든다. 이후 같은 Active Path로 복구한다.

## 12.3 결정 기준

Common Image가 Slot별 Baseline 대비 다음 중 하나를 초과하면 Slot별 Baseline을 채택한다.

- 준비 후 추가 Compile/Import 10초 초과
- `Library` 변경 물리량 1 GiB 초과
- Test Parity 실패
- Bee/Asset DB 오류
- 반복 실행에서 Physical Image 수준으로 회귀

경로 문자열을 근거 없이 직접 Patch하지 않는다.

---

# 13. Worker Slot 설계

## 13.1 초기 동시성

- Phase 1: 1 Worker
- Phase 2: 2 Worker
- Phase 3: 4 Worker
- 최대값은 실측 후 확장

## 13.2 Slot 상태

```go
type SlotState string

const (
    SlotEmpty       SlotState = "empty"
    SlotPreparing   SlotState = "preparing"
    SlotReady       SlotState = "ready"
    SlotRunning     SlotState = "running"
    SlotResetting   SlotState = "resetting"
    SlotQuarantined SlotState = "quarantined"
)
```

## 13.3 Worktree 전략

초기 병렬 테스트는 고정 Slot 경로에 Detached Worktree를 구성한다.

```bash
git worktree add --detach X:\testplay\workers\00\project <commit>
```

- 같은 Branch Checkout 충돌 방지
- Slot별 경로 고정
- Run 종료 후 삭제 또는 Commit 전환 재사용은 Benchmark로 결정
- Git Metadata와 Image Lifecycle을 하나의 함수에 결합하지 않음
- Dirty Worktree 강제 삭제 금지

---

# 14. Test Sharding

CoW 1~4 Worker가 안정화된 뒤 구현한다.

**Sharding**은 많은 테스트를 여러 묶음으로 나눠 여러 Worker가 동시에 실행하게 하는 방식이다.

## 14.1 입력

- 완전한 Test 목록
- 최근 성공 Run의 Test별 Duration
- Category/Filter
- 격리 제약
- Serial Category
- Worker Capability

## 14.2 LPT 알고리즘

**LPT(Longest Processing Time First)**는 예상 실행시간이 긴 테스트부터 현재 총 예상시간이 가장 짧은 Shard에 배정하는 방식이다.

```text
테스트를 예상시간 내림차순 정렬
→ 현재 총시간이 가장 짧은 Shard에 배정
```

## 14.3 결과 병합

- 각 Worker 결과 독립 보존
- 최종 Exit Code는 기존 Contract와 호환
- 중복 Test Execution 금지
- Missing Test 탐지
- Worker Crash는 Infrastructure Failure로 분류
- 결과 병합 시 Path Remap 유지
- Stable Result Digest 생성
- Backend/Materializer별 Metrics 보존

## 14.4 목표

```text
300 tests
→ 4~8 shards
→ shard당 40~75 tests
→ Unity startup 비용을 shard 내부에서 상각
```

300개 Unity Process를 생성하지 않는다.

---

# 15. Resource 정책

시간과 물리 저장공간을 우선 최적화하되 OOM, Disk Full과 Unity License 실패를 막는다.

```yaml
parallel:
  workers: auto
  max_workers: 8
  minimum_free_memory_gb: 6
  minimum_workspace_volume_free_gb: 2

workspace:
  reject_physical_copy_when_budget_exceeded: true
  allow_silent_fallback: false
```

Physical Copy 예상 공간이 Budget을 넘으면 자동 실행하지 않는다.

---

# 16. 필수 계측

## 16.1 시간

- Capability Probe
- Image Resolve
- Metadata Verify
- Full Integrity Hash
- Image Create
- Worktree Create/Update
- Library Materialization
- Workspace Verify
- Unity Startup
- Script Compile
- Asset Import
- Test Execution
- Write-back
- Reset/Cleanup
- 전체 Wall-clock

Script Compile과 Asset Import가 Unity 로그에서 안정적으로 분리되지 않으면 `unknown/unavailable`로 기록하고 추측하지 않는다.

## 16.2 저장공간

- Image Logical Bytes
- Image Allocated Bytes
- Worker Logical Bytes
- Worker Allocated Bytes
- Host VHDX File Size 변화
- Workspace Volume Free Bytes 변화
- Test 전후 Worker Physical Delta
- Store Root Allocation Amplification

최소 두 가지 계측을 비교한다.

- `FILE_STANDARD_INFO.AllocationSize`
- `GetCompressedFileSize`
- Volume Free-space Delta

## 16.3 향후 Materializer 계측 결과 예시

다음은 기존 camelCase `workspace_metrics` 계약을 확장하는 후보이며 아직
구현되지 않았다.

```json
{
  "backend": "shadow",
  "workspace_metrics": {
    "workspaceBackend": "image",
    "materializer": "refs-block-clone",
    "imageKey": "...",
    "imageStatus": "valid",
    "resolveMs": 25,
    "fullHashMs": 0,
    "materializeMs": 842,
    "verifyMs": 112,
    "logicalBytes": 5690831667,
    "physicalBytesDelta": 81788928,
    "pathStrategy": "common-image"
  }
}
```

---

# 17. 테스트 계획

## 17.1 순수 Go Unit

- Image Key Determinism
- `packages-lock` Invalidation
- Build Target/Test Platform Invalidation
- Path Normalization
- Materializer Selection
- Chunk Boundary 계산
- 4 GiB 미만 보장
- Tail Copy 계산
- Cancellation State
- Manifest State Transition
- Backend Selection
- Silent Fallback 금지
- Metrics additive compatibility

## 17.2 Physical Image Regression

- Existing Image 10회 Reuse
- Base Image Digest 불변
- Source Input Hash 불변
- Hardlink 없음
- Lock Ownership
- Staging 미완성 노출 금지
- Corrupt Image Quarantine
- `--clear-cache`
- External Store Root
- 한글/공백 경로
- `--keep-workspace`

## 17.3 Windows ReFS Integration Fixture

- 0 byte
- 1 byte
- cluster-1 / cluster / cluster+1
- 1 GiB 이상 Sparse/Regular File
- 많은 작은 파일 10k+
- 한글/공백/긴 경로
- Read-only File
- Reparse Point
- Locked File
- Source/Destination 다른 Volume
- Clone 후 Source/Sibling Isolation
- 취소 후 Incomplete/Recovery
- 실제 Physical Delta

## 17.4 Unity Fixture

1. 작은 Unity Fixture
2. `minidungeon-ai-tooling-evidence`
3. GNF_

GNF_ 실행 전에 작은 Fixture에서 Lifecycle, Source Protection과 Parity를 통과해야 한다.

## 17.5 Backend Parity

동일 Commit과 Test Selection으로 비교:

```text
legacy
physical-image
refs-image
```

비교 항목:

- Exit Code
- Test 목록과 Outcome
- Normalized Error
- Timeout Type
- Compile Failure Classification
- Results XML 의미
- Summary JSON
- Stable Result Digest
- `new_failures`

## 17.6 오염과 복구

- Base Image Full Hash 또는 Deterministic Manifest 전후 비교
- Source Git Status/Hash 전후 비교
- Worker 0 수정 후 Worker 1 Sample Hash 비교
- Failed Run 후 재준비 성공
- 강제 Process Kill 후 Quarantine/Recovery
- Disk Full
- Locked File
- Stale Lock
- Incomplete Marker
- VHDX Remount 후 복구

## 17.7 Standalone CLI 독립성

Honey Bee 저장소, Extension, Runtime과 Package가 없는 깨끗한 환경에서:

- 사전 빌드 `testplay` 바이너리로 `version`, `check`, `list`
- Legacy 실행
- Bridge 실행
- Physical Image 실행
- ReFS 환경에서 Probe와 Image Materialization
- 일반 CLI Caller가 Project Path와 testplay 설정만으로 실행
- 결과 JSON이 Honey Bee 관련 필드 없이 해석 가능
- Honey Bee 미설치가 Warning이나 Capability Failure로 기록되지 않음

CI에는 최소 하나의 `standalone-windows` Job을 둔다.

---

# 18. Benchmark Matrix

## 18.1 Backend/Materializer

| 실행 방식 | 목적 |
|---|---|
| Bridge | 열린 Warm Editor 기준 |
| Legacy Shadow | 기존 Cache/Write-back 기준 |
| Image + Physical Copy | 현재 검증된 안전 기준선 |
| Image + ReFS Common Image | 핵심 CoW 후보 |
| Image + ReFS Slot Baseline | Path 안정 후보 |

## 18.2 Run Matrix

- Cold 1회
- Warm 10회
- 2 Worker Parallel 10회
- 4 Worker Parallel 10회
- 가능하면 30회 Dogfood

## 18.3 보고 표

| Backend | Materializer | Resolve | Hash | Materialize | Verify | Unity | Wall | Peak Physical | Post-run Delta | Parity |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| Legacy | cache-copy | | | | | | | | | |
| Image | physical-copy | | | | | | | | | |
| Image | refs-common | | | | | | | | | |
| Image | refs-slot | | | | | | | | | |

성능 개선이 없거나 회귀하면 숨기지 않는다.

---

# 19. 오류와 Fallback

## 19.1 명시적 Materializer

```text
Probe/Materialize 실패
→ 구조화된 오류
→ 실행 중단
→ 다른 Materializer 자동 replay 금지
```

## 19.2 `auto`

Alpha 안정화 후에만 허용한다.

```text
Bridge eligible
→ Bridge

else Image valid/creatable and ReFS Probe passed
→ Image + ReFS

else Image Physical Copy allowed and Disk Budget passed
→ Image + Physical Copy

else Legacy allowed and Budget passed
→ Legacy

else
→ 명시적 실패와 선택 가능한 대안
```

Fallback 발생 시 다음을 기록한다.

- Attempted Backend
- Attempted Materializer
- Selected Backend
- Selected Materializer
- Fallback Reason
- Estimated/Actual Bytes
- Estimated/Actual Time

## 19.3 오류 코드 후보

- `image-missing`
- `image-stale`
- `image-corrupt`
- `image-locked`
- `image-unsupported`
- `materializer-unsupported`
- `refs-unsupported`
- `different-volume`
- `clone-probe-failed`
- `destination-conflict`
- `file-locked`
- `verification-failed`
- `disk-budget-exceeded`
- `workspace-quarantined`
- `allocation-amplification-exceeded`

기존 Exit-code Contract와 충돌하지 않도록 Infrastructure Error Mapping을 별도로 설계한다.

---

# 20. 단계별 구현 계획

## T0 — Physical Image Baseline

상태: **완료 / PROMISING**

완료 항목:

- Opt-in Image Backend
- External Store Root
- Image Key/State
- Lock
- Staging/Validation/Commit
- Quarantine
- Physical Materialization
- Metrics
- Legacy/Image Parity
- GNF_ Cold/Warm Benchmark

다음 단계로 진행할 근거는 확보됐으나 기본 Backend 변경 조건은 충족하지 않았다.

## T1 — ImageStore와 LibraryMaterializer 책임 분리

- 현재 Physical Copy 동작 보존
- Interface 도입
- 결과에 Materializer ID 추가
- Image Lifecycle과 Materialization Error 분리
- 기존 CLI/JSON 호환 유지
- 추가 리팩터링 금지

## T2 — 계측 분해

- Resolve
- Metadata Verify
- Full Hash
- Materialize
- Verify
- Cleanup
- Unity Startup
- Compile/Import/Test 가능 범위 분리

중복 Full Hash 제거 여부는 이 단계 실측 후 결정한다.

## T3 — Windows ReFS 작은 Probe

Unity를 실행하지 않는다.

```text
1 MiB Fixture
→ Clone
→ Logical/Physical 측정
→ Source 수정 격리
→ Destination 수정 격리
→ 취소/Lock/다른 Volume 오류
```

## T4 — ReFS File/Tree Materializer

- Regular File Clone
- Chunk/Tail
- Attribute/Timestamp
- Directory Tree
- Marker/Manifest
- Long Path
- Integration Test

## T5 — Single Unity Worker

- 작은 Fixture
- Legacy/Physical Image/ReFS Image Parity
- Unity 실행 후 Physical Delta
- Common Image Path Invalidation
- 실패 복구

## T6 — GNF_ Benchmark와 Path Sensitivity

- Cold 1
- Warm 10
- Common Image
- Slot Baseline
- Compile/Import
- Source/Image Contamination
- SLO Verdict

## T7 — 2개, 이후 4개 Worker

- Slot Pool
- Independent Writable Libraries
- Parallel Run
- 총 Physical Delta
- Resource Guard
- Crash Recovery

## T8 — Test Sharding

- Duration History
- LPT Planner
- Merged Contract
- Missing/Duplicate Detection
- 200~300 Test Benchmark

## T9 — Shared Engine Extraction 검토

- Honey Bee에서도 소비 가능한 Neutral Helper Protocol
- testplay 내부 타입과 공개 계약 분리
- 별도 Package/Helper 필요성 결정
- testplay 독립 설치와 실행 유지

---

# 21. Stop Conditions

다음 조건에서는 복잡한 구현으로 무작정 진행하지 않고 원인을 보고한다.

- ReFS Clone 성공 후 Physical Delta가 Physical Copy와 유사
- Unity 실행 즉시 대부분의 `Library`가 다시 써져 1 GiB 상한을 지속 초과
- Common Image와 Slot Baseline 모두 Path Invalidation 비용이 Physical Image와 유사
- Source/Image/Sibling 오염
- Parity 실패
- 일반 사용자 권한에서 Win32 API가 안정적으로 동작하지 않음
- File Count가 많을 때 Clone 준비가 목표 대비 이점 없음
- ReFS가 실제 사용자 설치 환경에서 지나치게 높은 진입 장벽을 만듦

후보를 다음 순서로 검토한다.

1. ReFS Per-slot Retained Baseline
2. VHDX Differencing Disk
3. Path-stable Mount
4. ProjFS User-mode Overlay
5. NTFS Minifilter는 최후 수단

---

# 22. Honey Bee Extraction Readiness

## 22.1 현재 추출 후보

- Image Key Payload
- Metadata와 Integrity Verification
- Key-scoped Lock
- Atomic Staging/Commit
- Corruption Quarantine
- Materialization Contract
- Workspace Metrics Schema
- Capability Probe 결과 형식

## 22.2 아직 추출하면 안 되는 부분

- Bridge/Shadow/Process 선택 정책
- NUnit/Exit-code 처리
- Artifact/History/Status 계약
- Unity Builder 인자
- testplay Result Ownership
- Retention/GC 정책
- 플랫폼별 CoW 구현

## 22.3 추출 조건

다음을 모두 만족한 뒤 검토한다.

- ReFS Single Worker PROVEN 또는 최소 PROMISING
- Physical Image와 ReFS가 동일 Interface로 동작
- Honey Bee가 요구하는 기능과 testplay 내부 정책의 경계가 명확
- Standalone testplay 설치가 공통 Helper 없이도 가능하거나 Helper를 독립 탐지 가능
- Protocol Versioning과 Compatibility Test 존재

---

# 23. 개발 Agent 규칙

1. 기존 Bridge와 Legacy를 삭제하거나 기본 동작을 바꾸지 않는다.
2. Physical Image를 CoW라고 표현하지 않는다.
3. Image Backend를 기본값으로 전환하지 않는다.
4. Image Lifecycle과 Materializer 책임을 분리한다.
5. 먼저 작은 실제 ReFS Probe를 구현한다.
6. OS 버전이나 Filesystem 이름만으로 지원을 선언하지 않는다.
7. Logical Size와 Physical Size를 분리한다.
8. Source/Base Image를 Unity에 직접 열지 않는다.
9. Reparse Point를 무작정 따라가지 않는다.
10. 4 GiB 제한과 Cluster Alignment를 테스트로 고정한다.
11. GNF_ 전에 작은 Fixture 검증을 끝낸다.
12. Legacy/Physical Image Parity를 성능보다 우선한다.
13. 실패를 Silent Fallback이나 성공으로 포장하지 않는다.
14. 구현 보고에는 실제 명령, 환경, 수치와 실패 횟수를 포함한다.
15. ReFS 성공 전 Honey Bee와 공유 코드를 성급히 추출하지 않는다.
16. Honey Bee 저장소, Package 또는 Runtime을 testplay의 Build/Run Dependency로 추가하지 않는다.
17. Honey Bee 연동 입력은 일반 CLI 옵션 또는 공개 파일 프로토콜로 일반화한다.
18. `standalone-windows` 검증이 실패하면 완료로 보고하지 않는다.
19. 실행하지 않은 Benchmark를 실행했다고 기록하지 않는다.
20. exFAT 관측 결과를 모든 환경에 일반화하지 않는다.

---

# 24. 최종 보고 형식

## 1. Environment

- OS Build
- Volume과 Filesystem
- ReFS Dev Drive/VHDX
- Unity Version
- Hardware
- Store Root
- Workspace Root

## 2. Capability Evidence

- 실제 FSCTL Probe
- Same Volume
- Cluster Size
- Logical/Physical Delta

## 3. Implementation

- Image Store
- Materializer
- File/Tree Clone
- Marker/Manifest
- Slot Lifecycle

## 4. Correctness

- Source/Image/Sibling 격리
- Legacy/Physical/ReFS Parity
- Lock/Quarantine

## 5. Path Sensitivity

- Common Image
- Per-slot Baseline
- Compile/Import
- Bee Rewrite

## 6. Performance

- Cold/Warm/Parallel
- Resolve/Hash/Materialize/Verify
- Unity/Test
- Logical/Physical
- Peak/Post-run Delta

## 7. Failures

- Lock
- Cancellation
- Disk Full
- Crash Recovery
- Unsupported File
- Different Volume

## 8. Verdict

- `PROVEN`
- `PROMISING`
- `NOT YET VIABLE`

## 9. Honey Bee Extraction Readiness

- 공유 가능한 부분
- testplay에 남아야 할 부분
- Protocol Risk

---

# 25. 완료 정의

이 연구 기능은 다음을 모두 만족해야 ReFS Alpha 완료로 간주한다.

- Physical Image Baseline이 기존 테스트에서 계속 통과한다.
- Image Store와 Materializer 책임이 분리된다.
- 실제 ReFS Block Clone 사용을 Probe Evidence로 증명한다.
- GNF_ 기준 Library Materialization 5초 이내 또는 실패 상한 10초 이내다.
- Worktree + Library READY가 목표 15초, 실패 상한 30초 이내다.
- Worker 생성 직후 Physical Delta 250 MiB 이하이다.
- 일반 EditMode Test 후 Worker Delta 1 GiB 이하이다.
- Legacy, Physical Image와 ReFS 결과 Parity 100%다.
- Source, Base Image와 Sibling 오염 0건이다.
- 실패·중단·재부팅 후 Incomplete Workspace를 식별하고 복구할 수 있다.
- Logical/Physical Bytes와 Fallback 여부를 결과에서 확인할 수 있다.
- 4 Worker 준비 30초 이내, 총 추가 물리량 4 GiB 이하이다.
- Honey Bee가 없는 깨끗한 Windows 환경에서 독립 실행된다.
- testplay-runner 소스와 모듈 Graph에 Honey Bee Dependency가 없다.
- 공통 Helper가 분리되더라도 testplay 단독으로 Version Probe, 실행과 오류 해석이 가능하다.
- 기존 JSON/Exit-code/Replay-safety 계약은 Honey Bee 연동 여부에 따라 달라지지 않는다.
- 기본 Backend 변경은 별도 제품 결정과 추가 Dogfood 없이는 수행하지 않는다.

---

# 26. 현재 최종 판정

```text
Physical Image Backend: PROMISING
ReFS Block Clone Materializer: NOT IMPLEMENTED / READY FOR SPIKE
Default Backend Change: REJECTED FOR NOW
Draft PR Readiness: YES
```

현재 Physical Image는 다음을 증명했다.

- 결과 정확성 유지
- Source와 Base Image 오염 방지
- 반복 Warm Run에서 전체시간 32.1% 절감
- Workspace Lifecycle 63.5% 절감
- 추가 물리 Peak 41.7% 절감

동시에 다음 한계를 확인했다.

- Workspace 준비 110.2% 회귀
- Cold 65.3% 회귀
- Physical Copy 공간 필요
- exFAT Allocation 증폭
- 4 Worker/Sharding 미검증

따라서 다음 기술 질문은 명확하다.

> 검증된 Image Lifecycle을 유지하면서 Physical Copy Materialization을 ReFS Block Clone으로 교체했을 때, 준비시간과 실제 물리 추가량을 어느 수준까지 줄일 수 있는가?

---

# 27. 참고 자료

- Microsoft Dev Drive: https://learn.microsoft.com/en-us/windows/dev-drive/
- Microsoft ReFS Block Cloning: https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning
- `FSCTL_DUPLICATE_EXTENTS_TO_FILE_EX`: https://learn.microsoft.com/en-us/windows-hardware/drivers/ifs/fsctl-duplicate-extents-to-file-ex
- `FSCTL_DUPLICATE_EXTENTS_TO_FILE`: https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ni-winioctl-fsctl_duplicate_extents_to_file
- `FILE_STANDARD_INFO`: https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_standard_info
- `GetCompressedFileSize`: https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getcompressedfilesizew
- Git Worktree: https://git-scm.com/docs/git-worktree
- Current Bridge: `unity/com.testplay.bridge/README.md`
- Current Shadow: `internal/shadow/workspace.go`
- Current Image Backend: `internal/libraryimage`
- Current Backend Selection: `internal/runsvc/workspace_backend.go`
- Benchmark: `docs/benchmarks/library-image-baseline.md`
- Image Spike Notes: `docs/library-image-spike.md`
