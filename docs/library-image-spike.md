# Library Image 기반 Shadow Workspace 기술 검증

## 결론

이 Vertical Slice는 안전성과 결과 parity를 확인했다. 작은 fixture에서는
warm 전체 시간이 Legacy보다 0.4% 짧고 workspace 준비가 30% 길어 이점이
없었다. GNF_와 외장 persistent store를 사용한 대형 측정에서는 warm 전체
시간이 32.1%, prepare+write-back+cleanup lifecycle이 63.5% 짧았다.
그러나 좁은 workspace preparation은 전체 hash 검증 때문에 110.2%
길었다.

**최종 판정: `PROMISING`**

기본 backend는 변경하지 않는다. Image backend는 명시적 실험 옵션으로만
남긴다.

## 문제 정의

Unity Editor가 원본 프로젝트를 열고 있거나 `--shadow`가 지정되면
testplay-runner는 per-run Shadow Workspace를 만든다. 기존 warm cache는
Library의 cold import를 피하지만 매 실행마다 Library 전체를 workspace로
복사하고, 성공 후 다시 cache로 복사한다. 대규모 프로젝트에서는 이
physical I/O가 디스크, CPU, 메모리, 실행 시간을 함께 소비한다.

검증한 가설은 다음과 같다.

> 검증된 immutable Library Base Image를 재사용하면 기존 결과 계약과
> 원본 보호를 유지하면서 Shadow 준비 시간을 유의미하게 줄일 수 있다.

이번 구현은 COW, APFS clone, ReFS block clone, hardlink, 가상
파일시스템을 사용하지 않는다.

## 현재 Shadow 흐름

실제 호출 흐름은 다음과 같다.

1. `cmd/testplay/run.go:runRun`이 config, artifacts, status writer와
   `runsvc.Request`를 만든다.
2. `internal/runsvc/service.go:Service.Run`이 bridge eligibility를 먼저
   판정한다.
3. bridge가 선택되지 않았고 다음 중 하나가 참이면 Shadow를 사용한다.
   - `--shadow`
   - `--reset-shadow`
   - 원본 `Temp/UnityLockfile` 존재
4. `internal/shadow/workspace.go:Prepare`가 다음을 수행한다.
   - `Assets/`: 8-worker physical copy
   - `ProjectSettings/`: 8-worker physical copy
   - `Packages/`: Unix symlink 또는 Windows junction
   - `Library/`: 유효한 `.testplay/cache/Library` physical copy, 아니면 빈 디렉터리
   - `Temp/`: 제거
5. `internal/unity.Execute`가 Shadow path로 Unity를 실행한다.
6. results XML, stdout/stderr, summary, manifest는 원본의
   `.testplay/runs/<run_id>`에 수집된다.
7. `Workspace.RemapPaths`가 결과 속 Shadow 절대 경로를 원본 경로로
   되돌린다.
8. exit 0 또는 3이면 Shadow Library를 Legacy cache로 전체 write-back한다.
9. `Workspace.Cleanup`이 per-run Shadow를 삭제한다.

### Lock과 동시 실행

- `shadow.IsLocked`는 `Temp/UnityLockfile`의 존재만 검사한다. PID 또는
  stale 여부는 확인하지 않으므로 stale Unity lock은 불필요한 Shadow를
  선택할 수 있다.
- per-run Shadow 이름에 run ID가 포함되어 실행 workspace끼리는
  충돌하지 않는다.
- Legacy Library cache에는 생성 락이 없다.
- scenario mode는 공유 cache write-back을 건너뛴다.

### Cache 재사용과 무효화

Legacy cache key는 다음 두 파일만 해시한다.

- `ProjectSettings/ProjectVersion.txt`
- `Packages/manifest.json`

`packages-lock.json`, 나머지 ProjectSettings, build target은 포함하지
않는다. 상태는 bool valid/invalid만 제공하며 missing, stale, corrupt를
구분하지 않는다.

### Portable config와 프로젝트 경로

현재 `origin/main`의 `config.Validate`는 상대 `project_path`를 config
파일 디렉터리에 고정하고, 상대 `result_dir`을 project path에 고정한다.
Image key는 canonical absolute project identity도 포함하므로 프로젝트가
이동하면 기존 image가 무효화된다. 이는 Library 내부의 절대 경로 가능성을
고려한 의도적인 안전 우선 선택이다.

### 기존 테스트가 보장하는 동작

- Shadow 선택과 실행 project path
- Assets/ProjectSettings copy와 Packages link
- 권한 및 symlink 보존
- copy 취소와 준비 실패 rollback
- per-run workspace 격리 및 cleanup
- Legacy Library seed/write-back/clear
- source path remap
- exit code와 JSON schema
- process/shadow/bridge 결과 계약

## 기존 병목

코드와 기존 측정 문서에서 확인된 병목은 다음과 같다.

- Assets와 ProjectSettings 전체 copy
- warm cache Library의 cache → workspace 전체 copy
- 성공 후 workspace → cache 전체 write-back
- Unity startup, package resolve, script compilation, asset import
- per-run workspace 삭제

기존 `docs/20_v0.9_measurement_note.md`는 151 EditMode test 프로젝트에서
cold 223초, warm 평균 30.4초를 기록했다. 기존 구현은 이 시간을 세부
단계로 나누지 않았다.

이번 slice는 다음을 분리 계측한다.

- project file copy
- Library materialization
- image creation
- Unity 전체 실행
- NUnit test duration 합계
- Unity residual startup/import/compile 시간
- Legacy cache write-back
- cleanup
- 추가 allocated bytes 또는 platform fallback logical bytes

`unityStartupMs`는 `Unity wall time - NUnit test duration`인 residual이다.
Unity가 구조화된 import/compile 경계를 제공하지 않으므로 script
compilation과 asset import는 아직 각각 분리하지 못한다.

## 구현한 설계

### CLI와 선택 정책

```bash
testplay run --workspace-backend=legacy
testplay run --workspace-backend=image
testplay run --workspace-backend=image --keep-workspace
testplay run --workspace-backend=image --workspace-store-root=/absolute/cache/root
```

- flag 미지정: 기존 bridge → shadow/process 선택을 그대로 유지한다.
- `legacy`: 기존 Shadow/cache backend를 명시적으로 강제한다.
- `image`: 새 Image Shadow backend를 명시적으로 강제한다.
- 명시적 image 실패: 오류를 반환하고 종료한다.
- silent legacy fallback: 없음.
- `fallbackUsed`: 항상 false인 구조화 필드로 공개한다.
- `--workspace-store-root`: persistent Legacy cache/Image store만
  project 밖의 명시적 절대경로로 이동한다. Unity Shadow는 기존 경로다.
- scenario mode: 이번 slice에서는 image, external store, keep-workspace를
  명시적으로 거부한다.

External store root는 존재하는 directory여야 하며 Unity project와
조상/자손 관계일 수 없다. canonical project path hash로 namespace해 다른
프로젝트 cache 충돌을 방지한다. Image `--clear-cache`는 Legacy cache를
삭제하지 않는다.

기존 top-level `backend` 값은 호환성을 위해 두 Shadow 구현 모두
`"shadow"`를 유지한다. 세부 선택은 additive `workspace_metrics`의
`workspaceBackend`로 구분한다.

### Image 저장 구조

```text
.testplay/library-images/
  images/
    <image-key>/
      Library/
      metadata.json
      COMPLETE
  locks/
    <image-key>.lock
  quarantine/
    <image-key>-corrupt-<timestamp>/
```

Image는 key별 immutable 디렉터리다. 이전 key의 image는 자동 삭제하지
않으며 새 key resolve에서 `stale` 근거로 사용한다.

### Image 생성과 materialization

1. Image key를 계산한다.
2. key lock을 `O_EXCL`로 획득한다.
3. lock 획득 후 다시 resolve한다.
4. missing/stale/corrupt이면 격리된 builder Shadow를 만든다.
5. builder는 Assets, Packages, ProjectSettings를 모두 physical copy한다.
6. Unity compile-only batch를 실행해 builder Library를 구성한다.
7. Library를 image sibling staging 디렉터리에 physical copy한다.
8. 전체 Library digest, file count, byte count를 metadata에 기록한다.
9. `COMPLETE`를 마지막에 기록하고 staging image를 검증한다.
10. 같은 filesystem에서 final key path로 atomic rename한다.
11. 별도 test Shadow를 만들고 Image Library를 physical copy한다.
12. Unity test를 실행한다.
13. test-mutated Library는 폐기하며 Base Image에는 write-back하지 않는다.

원본 프로젝트의 `Library`는 위 경로에서 읽거나 복사하지 않는다. Cold
Image의 Library 데이터 흐름은 정확히 다음과 같다.

```text
copied Assets/Packages/ProjectSettings
→ builder Unity가 builder Library 생성
→ builder Library를 Base Image로 physical copy
→ Base Image를 test Shadow Library로 physical copy
```

따라서 cold Image에서는 Assets, Packages, ProjectSettings가 builder와
test Shadow에 각각 한 번, 총 두 번 physical copy된다. Library는 Unity가
builder에 한 번 생성하고, 이후 Base와 test Shadow로 두 번 physical
copy된다. Warm Image에서는 builder가 없으므로 프로젝트 파일 copy 1회와
Base → test Shadow Library copy 1회만 수행한다.

### 용량 생명주기

새 계측은 원본 프로젝트가 이미 차지한 용량을 제외하고 다음을 분리한다.

| 단계 | 논리/물리 계측 | 수명 |
|---|---|---|
| Base Image | `baseImageLogicalBytes`, `baseImagePhysicalBytes` | key가 유효한 동안 유지 |
| 전체 Image store | `imageStorePhysicalBytes` | stale/quarantine 포함, 명시적 clear/후속 GC 전까지 유지 |
| cold builder | `imageBuilderLogicalBytes`, `imageBuilderPhysicalBytes` | image atomic commit 직후 정리 |
| test Shadow | `workspaceLogicalBytes`, `workspacePhysicalBytes` | 실행 종료 cleanup까지 |
| 체크포인트 최대치 | `observedPeakAdditionalPhysicalBytes` | builder+store 또는 store+test Shadow 중 큰 값 |
| cleanup 결과 | `cleanupReclaimedPhysicalBytes`, `retainedPhysicalBytes` | 실행 workspace 회수와 실행 후 유지량 |

`observedPeakAdditionalPhysicalBytes`는 디렉터리 생명주기의 안전상 중요한
체크포인트에서 관측한 최대치다. 고주파 filesystem sampler가 아니므로
copy 도중의 메타데이터, Unity 임시 파일, Legacy의 파일 교체 순간 등으로
실제 순간 피크가 더 클 수 있다. 용량 사전 점검에는 이 값에 별도 안전
여유를 더해야 한다.

Image cold의 정상적인 겹침은 다음 두 상태 중 큰 값이다.

```text
생성 commit 직전/직후: 전체 Image store + builder Shadow
테스트 실행 중:       전체 Image store + test Shadow
cleanup 후:            전체 Image store만 유지
```

Warm에서는 builder가 0이며 `전체 Image store + test Shadow`만 transient
peak를 만든다. `--keep-workspace`이면 workspace가 회수되지 않으므로
`retainedPhysicalBytes`에 Image store와 workspace가 모두 포함된다.

Legacy cache write-back은 기존 cache와 완성된 임시 cache가 동시에 존재하는
지점을 별도로 측정하고 active workspace를 더한다. 이를 통해 단순
`workspace + 최종 cache`만 계산했을 때 누락되는 warm write-back peak를
드러낸다.

### Image Key

Schema v1 key 입력은 다음과 같다.

- image schema version
- `ProjectVersion.txt`에서 읽은 Unity Editor version
- configured Unity executable path hash
- `Packages/manifest.json` hash
- `Packages/packages-lock.json` hash 또는 명시적 `missing`
- 전체 `ProjectSettings/` tree hash
- host build target identity (`GOOS/GOARCH`)
- `ProjectSettings.asset`의 scripting backend 항목 또는 ProjectSettings hash coverage marker
- canonical absolute project identity hash

전체 payload를 canonical JSON으로 직렬화하고 SHA-256 digest를 만든다.
Assets는 key에 포함하지 않는다. Assets 변경은 materialized Library에서
Unity의 정상 변경 탐지/import 경로가 처리한다. Assets를 key에 포함하면
코드 수정마다 Base Image를 폐기해 warm reuse 목적이 사라진다.

### 상태와 무효화

| 상태 | 판정 | 동작 |
|---|---|---|
| `valid` | marker, metadata, key, 전체 Library digest 일치 | 재사용 |
| `missing` | image가 하나도 없음 | 신규 생성 |
| `stale` | 요청 key는 없고 다른 key image가 있음 | 요청 key 신규 생성 |
| `corrupt` | marker/metadata/Library/digest 불일치 | quarantine 후 재생성 |
| `unsupported` | 지원하지 않는 image schema | 명시적 실패 |

잘못된 image를 조용히 재사용하지 않는다.

## 안전성

### Atomicity

- staging은 final image와 같은 `images/` 아래에 생성한다.
- metadata와 COMPLETE를 쓴 뒤 전체 검증한다.
- 마지막 commit만 atomic rename한다.
- 중간 실패 staging은 제거된다.
- 불완전한 final directory는 corrupt이며 valid로 노출되지 않는다.

### Locking

- lock 범위는 Unity builder 시작 전부터 image commit 후까지다.
- 같은 key의 competing creator는 builder를 실행하지 않고 명시적 lock
  conflict를 받는다.
- lock은 PID, random token, UTC 생성 시각을 기록한다.
- Unix에서는 30분 이상이고 PID가 존재하지 않는다고 확인된 lock만
  stale로 제거한다.
- 현재 PID의 lock과 살아 있는 PID의 lock은 강제 제거하지 않는다.
- Windows는 stdlib만으로 안전한 PID liveness를 증명할 수 없어 자동
  stale 제거를 하지 않는 보수적 정책이다.
- release는 token이 여전히 일치할 때만 lock을 제거한다.

### Cleanup

- builder 실패 시 builder와 staging image를 제거한다.
- test Shadow는 성공, test 실패, infra return에서 cleanup된다.
- `--keep-workspace`는 test Shadow만 보존한다.
- Base Image는 일반 Shadow cleanup 대상이 아니다.
- corrupt image는 quarantine에 보존한다.
- stale image GC는 이번 범위에 포함하지 않는다.

### Source protection

- Image builder와 test workspace는 Assets, Packages, ProjectSettings를
  physical copy한다.
- source Library는 읽거나 수정하지 않는다.
- Base Image는 Unity project path로 직접 열지 않는다.
- materialized Library는 hardlink 없는 physical copy이므로 workspace
  쓰기가 Base Image에 전파되지 않는다.

Legacy의 Packages link 동작은 호환성을 위해 변경하지 않았다. 실제 Unity
E2E에서 Legacy가 source `packages-lock.json`을 만들 수 있음을 다시
확인했으며, Image 실행은 Legacy 이후 source tree에 추가 변경을 만들지
않았다.

## 관측 가능성

run stdout, history JSON, summary JSON에 다음 additive object가 기록된다.

```json
{
  "workspace_metrics": {
    "workspaceBackend": "image",
    "imageStatus": "valid",
    "imageResolutionStatus": "valid",
    "imageKey": "4bb071...",
    "imageCreationMs": 0,
    "workspacePreparationMs": 208,
    "fileCopyMs": 29,
    "libraryMaterializationMs": 100,
    "unityStartupMs": 6583,
    "unityExecutionMs": 6589,
    "testExecutionMs": 6,
    "cleanupMs": 134,
    "cacheWriteBackMs": 0,
    "baseImageLogicalBytes": 80740352,
    "baseImagePhysicalBytes": 80744448,
    "imageStorePhysicalBytes": 80752640,
    "imageBuilderLogicalBytes": 0,
    "imageBuilderPhysicalBytes": 0,
    "workspaceLogicalBytes": 148000000,
    "workspacePhysicalBytes": 148200000,
    "observedPeakAdditionalPhysicalBytes": 228952640,
    "retainedPhysicalBytes": 80752640,
    "cleanupReclaimedPhysicalBytes": 148200000,
    "physicalBytesAdded": 228944448,
    "fallbackUsed": false,
    "workspaceKept": false
  }
}
```

사람용 한 줄 workspace 요약은 stderr에만 기록된다. stdout의 단일 JSON
계약은 유지된다.

`physicalBytesAdded`는 초기 Spike 결과와의 호환성을 위한 기존 aggregate
필드다. 생명주기 판단에는 의미가 명확한 Base/Store/Workspace/Peak/
Retained/Reclaimed 필드를 사용한다.

## 검증

### 자동 테스트

- deterministic key
- Unity version, manifest, packages-lock, ProjectSettings invalidation
- valid/missing/stale/corrupt/unsupported
- incomplete image rejection
- corrupt quarantine and recreate
- live/stale lock과 builder-wide conflict
- failed staging cleanup
- Base/materialized write isolation
- copied Packages source isolation
- 첫 생성과 두 번째 reuse
- package change invalidation
- corrupt image safe recreation
- Unity test failure 후 source/Base 불변
- Legacy/Image stable result parity
- keep-workspace와 normal cleanup
- CLI flag validation과 additive JSON
- Windows cross-compile
- real Unity Legacy/Image parity

### 실제 Unity parity

```bash
UNITY_PATH=/Applications/Unity/Hub/Editor/6000.3.8f1/Unity.app/Contents/MacOS/Unity \
GOCACHE=/tmp/testplay-go-cache \
go test -tags e2e ./e2e -run '^TestE2E_LibraryImageParity$' -v -count=1
```

결과: PASS, 27.46초. exit code, counts, test 이름/결과, compile errors가
동일했다.

반복 벤치마크의 22회 결과도 모두 exit 0이고 backend 내부 안정성 및
cross-backend parity가 일치했다. 상세 수치는
`docs/benchmarks/library-image-baseline.md`에 있다.

## 알려진 한계와 위험

- GNF_ warm 전체 wall은 개선됐지만 좁은 workspace preparation은
  개선되지 않았다.
- 매 resolve와 materialize 전 전체 integrity hash를 계산해 Image path가
  Legacy보다 추가 I/O를 수행한다.
- Physical copy라 Library 크기만큼 workspace 디스크가 계속 필요하다.
- cold image는 builder와 test Unity를 각각 실행하므로 더 느리고 더 많은
  임시 디스크를 사용한다.
- Unity startup residual을 script compilation과 asset import로 분해하지
  못한다.
- build target은 현재 runner가 명시적 `-buildTarget`을 전달하지 않아 host
  identity로 표현한다.
- old-key image와 quarantine GC가 없다.
- scenario/parallel image 실행은 지원하지 않는다.
- Windows stale lock은 자동 회수하지 않는다.
- 외장 exFAT에서 Unity Library를 직접 실행하면 LMDB/native crash가
  발생했다. 외장 store에는 정지된 cache/image만 두고 Unity Shadow는
  내부 APFS에서 실행했다.
- external exFAT allocation unit이 논리 약 4.87GB Image를 물리 약
  16.60GB로 증폭했다.
- `--clear-cache`의 Legacy 동시성 한계는 기존 동작 그대로다.

## Honey Bee 공통 모듈 추출 준비도

지금 추출해도 비교적 안정적인 책임은 다음과 같다.

- canonical Image Key payload
- immutable image metadata와 verification
- key-scoped creation lock
- atomic staging/commit
- corruption quarantine
- physical materialization contract
- workspace metrics schema

아직 공통 Workspace Engine으로 추출하면 안 되는 부분은 다음과 같다.

- testplay의 bridge/shadow/process 선택 정책
- NUnit result/exit-code 분류
- testplay artifact/status/history 계약
- Unity compile-only builder 인자와 timeout 분류
- scenario concurrency와 cache GC
- 플랫폼별 clone/COW 최적화

추출 전 필요한 조건:

1. native production filesystem의 1GB 이상 Library에서 동일 parity와
   실패 복구 재검증
2. integrity verify와 copy를 한 pass로 결합한 최적화
3. image retention/GC 정책
4. Windows 실제 Unity와 stale-lock 운영 검증
5. build target과 scripting backend의 명시적 runner 입력
6. 최소 두 소비자(testplay-runner와 Honey Bee)의 동일 요구가 확인될 것

## 후속 제안

성능 문제를 해결하지 못한 채 프레임워크를 확대하지 않는다. 다음 spike는
새 backend 추가가 아니라 다음 한 가지에 한정하는 것이 적절하다.

> 검증과 physical materialization을 한 pass로 결합하고, destination의
> 동일 파일을 건너뛰는 incremental copy가 parity를 유지하면서 warm
> preparation을 줄이는가?

APFS clone, ReFS clone, hardlink, COW는 이 결과와 별도의 플랫폼별 안전성
spike 없이는 도입하지 않는다.
