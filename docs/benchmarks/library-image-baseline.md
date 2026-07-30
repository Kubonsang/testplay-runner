# Library Image Spike Benchmark Baseline

## 목적

Legacy Shadow cache와 실험적 Image backend의 cold/warm 비용, 디스크,
메모리, 안정성, 결과 parity를 같은 Unity fixture에서 비교한다.

## 환경

| 항목 | 값 |
|---|---|
| 측정 일자 | 2026-07-30 |
| OS | macOS, arm64 |
| Unity | 6000.3.8f1 |
| testplay source | `origin/main` b0be57d 기반 spike |
| 동시성 | 1 |
| test platform | EditMode |
| 반복 | backend별 cold 1회 + warm 10회 |
| 테스트 | 5 passed, 0 failed |
| 원본 fixture | `testdata/unity-project` |
| 추가 부하 | 64 × 1MiB random `.bytes` Assets |
| source Assets | 64MiB |
| warm Legacy cache | 77MiB |
| warm Image store | 77MiB |
| 계측 | `workspace_metrics`, `/usr/bin/time -l` |

## GNF_ production-size benchmark

### 환경과 저장 경계

| 항목 | 값 |
|---|---|
| 원본 프로젝트 | GNF_ |
| source Assets / 기존 Library | 1.5GiB / 5.3GiB |
| 테스트 | `GNF.DungeonGen.Tests.MathUtilTests`, 17 passed |
| 반복 | backend별 cold 1회 + warm 10회 |
| Unity workspace | 내부 APFS |
| persistent Legacy cache / Image store | 외장 SSD exFAT |
| 외장 여유 공간 | 시작 시 383GiB |
| 동시성 | 1 |

외장 SSD에 프로젝트 전체를 복제해 Unity를 직접 실행한 최초 시도는
script compilation 완료 후 Unity ArtifactDB/LMDB에서
`MDB_BAD_RSLOT`이 발생하고 native crash하여 exit 6으로 실패했다. 해당
실패는 성공 결과에 포함하지 않았다. exFAT 위의 Unity `Library` 실행은
이 환경에서 지원할 수 없다고 판정했다.

대신 명시적 `--workspace-store-root`를 추가해 다음 경계를 사용했다.

```text
GNF_ source + builder/test Shadow  → 내부 APFS
Legacy cache / immutable Image    → 외장 exFAT
raw benchmark output              → 외장 exFAT
```

Unity는 외장 DB를 직접 열지 않는다. 외장 store는 정지된 Library 파일을
보관하고 내부 APFS Shadow와 physical copy만 수행한다. 이 방식은 APFS
Clone/COW를 사용하지 않는다.

`--workspace-store-root`는 기존 default를 변경하지 않는다. 절대경로,
존재하는 디렉터리, Unity 프로젝트와 조상/자손 관계가 아닌 경로만
허용한다. 프로젝트 canonical path hash로 store를 namespace한다. Image
`--clear-cache`는 Image store만 제거하며 Legacy cache를 지우지 않는다.

### GNF_ cold run

| 지표 | Legacy | Image | Image 변화 |
|---|---:|---:|---:|
| Wall time | 240.80s | 398.14s | +65.3% |
| Image creation | 0ms | 267,986ms | 신규 비용 |
| Workspace preparation | 3,859ms | 41,238ms | +968.6% |
| Library materialization | 0ms | 14,445ms | cold Legacy는 빈 Library |
| Test Unity execution | 198,312ms | 49,697ms | Image builder는 creation에 포함 |
| NUnit test duration | 23ms | 31ms | 동등 |
| Cleanup | 6,598ms | 7,668ms | +16.2% |
| Legacy cache write-back | 30,687ms | 0ms | 제거 |
| Observed peak physical | 22,962,720,768 | 23,171,891,200 | +0.9% |
| Retained physical | 16,595,156,992 | 16,598,171,648 | +0.02% |
| Workspace physical | 6,367,563,776 | 6,573,719,552 | +3.2% |
| Peak RSS | 3,103,457,280 | 3,105,210,368 | +0.06% |
| 결과 | 17/17 PASS | 17/17 PASS | parity |

Cold Image는 builder Unity, image copy/전체 검증, 별도 test Unity가 필요해
예상대로 느렸다.

### GNF_ warm run, 10회

| 지표 | Legacy 평균 | Image 평균 | Image 변화 |
|---|---:|---:|---:|
| Wall time | 175.774s | 119.404s | **-32.1%** |
| Workspace preparation | 18,400.2ms | 38,668.9ms | **+110.2%** |
| Project file copy | 4,548.8ms | 5,113.2ms | +12.4% |
| Library materialization | 13,847.3ms | 11,926.8ms | -13.9% |
| Unity execution | 49,128.4ms | 48,624.4ms | -1.0% |
| Unity startup residual | 49,117.6ms | 48,609.0ms | -1.0% |
| NUnit test duration | 10.8ms | 15.4ms | 동등 |
| Cleanup | 8,076.9ms | 7,000.2ms | -13.3% |
| Cache write-back | 98,489.1ms | 0ms | -100% |
| Prepare + write-back + cleanup | 124,966.2ms | 45,669.1ms | **-63.5%** |
| Observed peak physical | 39,769,628,262 | 23,171,408,691 | **-41.7%** |
| Retained physical | 16,598,591,078 | 16,598,171,648 | -0.003% |
| Workspace physical | 6,573,573,325 | 6,573,237,043 | -0.005% |
| Peak RSS 평균 | 2,284,255,642 | 2,314,218,701 | +1.3% |
| 실패 | 0/10 | 0/10 | 동일 |

| 지표 | Legacy p50 / min / max | Image p50 / min / max |
|---|---:|---:|
| Wall time | 175.31 / 168.69 / 186.08s | 119.97 / 114.28 / 124.77s |
| Workspace preparation | 17,154 / 16,361 / 24,979ms | 37,992 / 36,900 / 42,417ms |
| Library materialization | 12,189 / 11,895 / 20,686ms | 11,305.5 / 10,687 / 15,613ms |
| Unity execution | 47,726.5 / 45,545 / 55,933ms | 47,306.5 / 45,868 / 53,438ms |
| Cleanup | 7,952 / 7,660 / 9,041ms | 6,990 / 6,762 / 7,351ms |
| Cache write-back | 97,834 / 94,121 / 104,846ms | 0 / 0 / 0ms |

Image의 warm wall 개선은 Unity 실행 성능 변화가 아니라 Legacy의 전체
cache write-back 제거에서 왔다. 반대로 Image는 `Resolve`와
`Materialize` 전 Base 전체 hash를 중복 수행해 좁은 preparation 지표가
110.2% 회귀했다. 최초 `Resolve` hash는 `workspacePreparationMs` 시작
전이므로 wall에는 포함되지만 preparation에는 포함되지 않는다.

### GNF_ parity와 안전성

| 검증 | 결과 |
|---|---|
| cold + warm 비교 실행 | 22/22 exit 0 |
| Legacy 내부 stable result | 11/11 동일 |
| Image 내부 stable result | 11/11 동일 |
| Legacy ↔ Image stable digest | 22개 모두 `142c188f...e48e` |
| 테스트 | 매회 17 passed, 0 failed |
| Image warm reuse | 10/10 `valid`, creation 0ms |
| 원본 Assets/Packages/ProjectSettings | 실행 전후 hash `acaa9007...b8ae` 동일 |
| Base Image write isolation | 매 warm 전체 digest verification PASS |
| 잔여 Shadow / image lock | 없음 |

외장 exFAT의 큰 allocation unit 때문에 Image 논리 크기
4,869,866,181 bytes가 물리 16,597,778,432 bytes를 사용했다. 이는
Image format 자체의 논리 팽창이 아니라 benchmark store filesystem의
물리 증폭이다.

## Small fixture benchmark

### 절차

Legacy와 Image용 fixture를 각각 별도 temp directory에 복사했다. 각
backend에서 다음 순서로 실행했다.

```bash
testplay run --workspace-backend=<legacy|image> --clear-cache

# 같은 fixture와 cache/image로 10회
testplay run --workspace-backend=<legacy|image>
```

모든 실행은 `/usr/bin/time -l`로 감싸 wall time과 maximum resident set
size를 수집했다. JSON 결과는 run ID, timing, temp path를 제외하고 다음을
비교했다.

- exit code
- total/passed/failed/skipped
- 실행된 test 이름
- test별 result
- compile errors

### Cold run

| 지표 | Legacy | Image | Image 변화 |
|---|---:|---:|---:|
| Wall time | 12.36s | 14.95s | +21.0% |
| Image creation | 0ms | 7,362ms | 신규 비용 |
| Workspace preparation | 43ms | 429ms | +898% |
| Library materialization | 0ms | 272ms | cold Legacy는 빈 Library |
| Unity test execution wall | 11,959ms | 6,702ms | builder는 imageCreation에 포함 |
| NUnit test duration | 7ms | 6ms | 동등 |
| Cleanup | 123ms | 136ms | +10.6% |
| Legacy cache write-back | 123ms | 0ms | 제거 |
| Physical bytes added (기존 aggregate) | 148,037,632 | 228,560,896 | +54.4% |
| Peak RSS | 555,810,816 | 556,040,192 | +0.04% |
| 실패 | 0/1 | 0/1 | 동일 |

Image cold run은 Base Image를 먼저 만들고 별도 test workspace에
materialize하므로 예상대로 느리고 디스크를 더 사용했다.

이 baseline은 세부 공간 계측을 추가하기 전에 수집했으므로 위
`Physical bytes added`는 당시 Base와 workspace 관측값을 합친 호환용
aggregate이며 실제 transient peak나 cleanup 후 retained 값이 아니다.
후속 GNF_ 실행에서는 다음을 별도 표로 기록한다.

- Base Image 논리 크기와 물리 크기
- 전체 Image store 물리 크기
- builder Shadow 논리/물리 크기
- test Shadow 논리/물리 크기
- cold 체크포인트 최대 추가 물리량
- cleanup 후 유지 물리량과 회수 물리량
- warm 1회 체크포인트 최대 추가 물리량

### Warm run, 10회

평균을 기본값으로 사용하고 p50/min/max를 함께 기록한다.

| 지표 | Legacy 평균 | Image 평균 | Image 변화 |
|---|---:|---:|---:|
| Wall time | 7.138s | 7.106s | -0.4% |
| Workspace preparation | 162.1ms | 211.2ms | **+30.3%** |
| Project file copy | 29.7ms | 31.8ms | +7.1% |
| Library materialization | 131.8ms | 100.7ms | -23.6% |
| Unity execution wall | 6,592.4ms | 6,614.4ms | +0.3% |
| Unity startup/import/compile residual | 6,586.2ms | 6,608.3ms | +0.3% |
| NUnit test duration | 6.2ms | 6.1ms | 동등 |
| Cleanup | 128.9ms | 134.6ms | +4.4% |
| Cache write-back | 233.4ms | 0ms | -100% |
| Physical bytes added (기존 aggregate) | 148,501,299 | 148,236,288 | -0.2% |
| Peak RSS 평균 | 477,275,750 | 477,442,867 | +0.04% |
| Peak RSS 최대 | 478,117,888 | 480,362,496 | +0.5% |
| 실패 | 0/10 | 0/10 | 동일 |

### 분산

| 지표 | Legacy p50 / min / max | Image p50 / min / max |
|---|---:|---:|
| Wall time | 7.11 / 7.06 / 7.38s | 7.07 / 6.97 / 7.49s |
| Workspace preparation | 157.5 / 123 / 228ms | 208 / 196 / 229ms |
| Library materialization | 129 / 96 / 201ms | 100 / 89 / 118ms |
| Unity execution | 6,574.5 / 6,519 / 6,809ms | 6,589 / 6,492 / 6,953ms |
| Cleanup | 130.5 / 119 / 135ms | 134 / 130 / 139ms |

### Parity와 반복 안정성

| 검증 | 결과 |
|---|---|
| Legacy 내부 11회 stable result | PASS |
| Image 내부 11회 stable result | PASS |
| Legacy ↔ Image result parity | PASS |
| 전체 exit code | 22/22 exit 0 |
| real-Unity dedicated E2E parity | PASS, 27.46s |
| source protection during Image run | PASS |
| Base Image write isolation | PASS |

### 해석

Library 파일 copy 자체는 Image가 23.6% 짧았지만 전체 workspace
preparation은 30.3% 길었다. `Store.Resolve`와 `Materialize`가 안전성을
위해 전체 Library integrity를 각각 확인하며, 이 hash I/O가
`libraryMaterializationMs` 밖의 preparation 시간에 포함되기 때문이다.

Legacy의 233ms write-back을 제거했지만 Unity startup이 약 6.6초를
차지해 전체 warm wall time 개선은 32ms, 0.4%에 그쳤다. 측정 노이즈
범위이므로 유의미한 개선으로 볼 수 없다.

권장 목표인 warm preparation 50% 단축과 비교하면:

```text
목표:  -50% 이상
실측:  +30.3% 회귀
```

## 종합 판정

`PROMISING`

- 안전성: 검증됨
- 결과 parity: 검증됨
- small fixture 반복 안정성: 22회에서 검증됨
- GNF_ 반복 안정성: 비교 실행 22회에서 검증됨
- GNF_ warm wall: 32.1% 개선
- GNF_ 전체 workspace lifecycle: 63.5% 개선
- 좁은 warm preparation 목표: 미달, 110.2% 회귀
- native APFS persistent store 대표성: 미검증

기본 Backend를 바꿀 근거는 아직 부족하다. 다음 단계는 integrity verify와
materialization을 한 pass로 결합해 preparation 회귀를 제거한 뒤, native
APFS 또는 Windows production filesystem에서 다시 측정하는 것이다.

## 후속 Phase Metric Schema

기존 수치는 다시 계산하지 않았다. 후속 실행부터 additive
`workspace_metrics`의 `materializer`, `imageResolveMs`,
`imageMetadataVerifyMs`, `imageFullHashMs`, `libraryMaterializeMs`,
`workspaceVerifyMs`, 기존 `cleanupMs`를 함께 수집한다. 기존
`libraryMaterializationMs`는 호환 aggregate로 유지된다.

Warm valid Image의 현재 안전 경로는 Base Image 전체 Hash를 Resolve와
materialize 직전 Verify에서 각각 한 번 수행한다. 새 계측은 이 비용을
드러내며 Hash 자체를 제거하거나 약화하지 않는다.
