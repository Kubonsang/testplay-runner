# 📈 testplay Release Plan & Version History

**현재 릴리스 버전:** `v0.11.0`
**목표:** 단순한 로컬 테스트 래퍼를 넘어, AI 에이전트에 최적화된 시나리오 기반 멀티 인스턴스 러너로 단계적으로 확장

> 이 문서는 확정 약속이 아니라, 베타 진행 상황에 따라 조정될 수 있는 릴리즈 계획을 정리한 것입니다.  
> 각 마일스톤의 릴리즈 게이트는 기능 존재 여부보다 재현성과 계약 일관성을 기준으로 판단합니다.

---

## 🧪 미릴리스 main — 플랫폼 네이티브 CoW Storage Helper

- 동일 schema 1 NDJSON `hello`/`acquire`/`release`/`shutdown` 계약
- Windows: 관리자 권한이 필요한 Differencing VHDX
- macOS: APFS `clonefileat(2)` 기반 디렉터리 Child, 관리자 권한 불필요
- Linux: GNU `cp --reflink=always` 기반 디렉터리 Child, 관리자 권한 불필요
- macOS/Linux는 CoW 미지원 파일시스템에서 물리 복사로 폴백하지 않고
  `cow-unavailable`로 실패
- **현재 경계:** storage helper와 자동 테스트까지만 구현. 공개 `testplay`
  CLI, Image backend, 기본 backend에는 아직 연결하지 않았으므로 v0.11.0
  릴리스 계약이나 버전 번호를 바꾸지 않는다.
- **검증 상태:** macOS 26.5.1 arm64 APFS 실제 Acquire/격리/Release PASS;
  Linux/Windows helper 교차 컴파일 PASS. Linux 실제 reflink 파일시스템과
  Unity Library 호환성은 아직 별도 하드웨어 게이트가 필요하다.

---

## ✅ v0.1.0-beta (Foundation)
**테마:** AI와 CI를 위한 결정론적 단일 러너의 뼈대

- **목표:** 단일 유니티 프로세스 실행에 대한 기초적인 상태 추적과 결과 계약 확보
- **포함 기능:**
  - Direct Batch Mode 실행
  - 기초 JSON contract 및 `status` / `event` artifact 기반 확보
  - Shell self-check 및 opt-in Unity smoke 테스트 경로 확보
- **릴리즈 게이트:** 단일 테스트 실행 시 JSON 출력과 주요 artifact가 일관되게 생성되고, 실패 시에도 계약이 깨지지 않을 것

## ✅ v0.2.0-beta (The Editor Unlock)
**테마:** 에디터 락(Lock) 우회 및 섀도우 워크스페이스 구축

- **목표:** 개발자의 작업 흐름을 끊지 않는 백그라운드 격리 실행 환경 확보
- **포함 기능:**
  - `Temp/UnityLockfile` 감지 시 `.testplay-shadow/` 워크스페이스 자동 생성
  - OS별 심링크/Junction 처리를 통한 `Packages/` 연결, `Library/` 영구 캐시 보존
  - 심링크·컨텍스트 취소·FileMode 보존·롤백 안전성을 갖춘 프로덕션 수준 강화
  - 결과 JSON의 경로 재매핑(Path Remapping) — 에이전트는 원본 경로만 확인
  - `--shadow` (강제 활성화) / `--reset-shadow` (캐시 재구축) 플래그 도입
- **릴리즈 게이트:** 에디터가 켜진 상태로 `testplay run`을 실행해도 원본 워크스페이스를 오염시키지 않고, 결과 JSON과 artifact가 원본 기준 경로로 매핑될 것

## ✅ v0.3.0-beta (The Multi-Instance Core)
**테마:** 시나리오 기반 다중 실행의 뼈대

- **목표:** 여러 개의 유니티 프로세스를 띄우고 결과를 합치는 1차 코어 확장
- **v0.2 P1 backlog 해소 (다중 실행의 전제 조건):**
  - **runID 교체** — 1초 단위 타임스탬프 → `YYYYMMDD-HHMMSS-xxxxxxxx` (crypto-random 4바이트 hex suffix); 동시 실행 시 결과 파일 충돌 방지 (현재 `Medium` Known Limitation)
  - **`--config` flag 도입** — CWD 의존 제거; 오케스트레이터가 각 인스턴스에 다른 config 경로를 직접 지정 가능
  - **Per-run shadow 격리** — run-ID 기반 독립 shadow 디렉터리(`.testplay-shadow-<run_id>/`); 병렬 `testplay run` 안전성 확보 (현재 `Medium` Known Limitation)
  - **Exit 8 구현** — SIGINT/SIGTERM → exit 8, timeout → exit 4로 명확 구분 (현재 두 경우 모두 exit 4)
- **포함 기능:**
  - `testplay run --scenario <file>` 인터페이스 최초 도입
  - Role 기반(Host/Client) 다중 섀도우 워크스페이스 동시 실행
  - 개별 `results.xml`과 `status`를 단일 시나리오 결과 JSON으로 1차 통합
- **릴리즈 게이트:** 시나리오 파일로 2개 이상의 인스턴스가 동시 실행되고, 합쳐진 JSON 결과가 일관된 구조로 출력될 것

## ✅ v0.4.0-beta (The Orchestrator)
**테마:** Host/Client 기동 순서 제어

- **목표:** Host가 네트워크 리슨 상태가 된 후 Client를 기동시키는 순서 보장 — 다중 인스턴스가 실제 협력 테스트를 수행하기 위한 최소 전제 조건
- **P1 선행 조건 (Ready Gating의 전제):**
  1. **Per-instance status polling 계약 정의** — `--scenario` 모드에서 인스턴스별 상태 파일(`testplay-status-<role>.json`) 계약 확정 및 구현
- **신규 기능:**
  2. **Host ready gating** — 시나리오 파일에 `depends_on`/`ready_phase`/`ready_timeout_ms` 필드 추가; Host 인스턴스의 status가 지정 phase에 도달할 때까지 Client 기동을 지연
  3. **Scenario 실패 원인 구조화** — `orchestrator_errors` 구조화된 필드로 ready timeout·host crash 등 오케스트레이션 실패를 출력
- **릴리즈 게이트:** Host 인스턴스가 ready 신호를 내기 전에 Client 프로세스가 시작되지 않을 것; 순서 위반 또는 ready timeout 발생 시 실패 원인이 시나리오 결과 JSON에 반영될 것

## ✅ v0.4.1-beta (Hotfix: Circular Dep + Fast-Fail)
**테마:** 시나리오 안정성 보강

- **포함 수정:**
  - `depends_on` 순환 의존성 감지 — `Load()` 시 DFS로 순환 탐지, 교착 방지
  - Host crash 시 dependent 인스턴스 fast-fail — `ready_timeout_ms` 전체 대기 없이 즉시 실패 처리

## ✅ v0.4.2-beta (Library Warm Cache)
**테마:** 섀도우 워크스페이스 Library/ 캐시를 통한 cold-start 제거

- **목표:** 반복 실행 시 Unity reimport 대기 시간(2-5분/회) 제거
- **포함 기능:**
  - **병렬 파일 복사** — `copyDir`을 8-goroutine 워커 풀로 병렬화 (~3-5x 처리량)
  - **캐시 인프라** — `.testplay/cache/Library/` + SHA256 기반 무효화 키 (`ProjectVersion.txt` + `manifest.json`)
  - **캐시 라이프사이클** — 첫 실행 cold-start → 성공 시(exit 0/3) 캐시 저장 → 이후 실행 seed → 프로젝트 변경 시 무효화
  - **시나리오 안전성** — `--scenario` 모드에서 캐시 write-back 건너뛰기 (동시 쓰기 방지)
  - **`--clear-cache` 플래그** — 캐시 강제 삭제 후 cold-start
- **릴리즈 게이트:** 캐시 유효 시 shadow Library/ seed 복사로 대체되고, 캐시 키 불일치 시 자동 무효화; 시나리오 모드에서 동시 쓰기 충돌 없을 것

## ✅ v0.5.0-beta (The AI Contract)
**테마:** AI 에이전트를 위한 출력 규약 강화 — 파싱 고도화, 에러 컨텍스트, E2E 검증

- **목표:** 순수 Go 로직의 완성도를 실제 Unity 환경에서 증명하고, AI 에이전트가 실패 원인을 정확히 추론할 수 있는 출력 계약 확립
- **포함 기능:**

  ### 5-1. Unity E2E 검증 파이프라인
  - 더미 Unity 프로젝트(`testdata/unity-project/`)를 이용한 실제 Unity 실행 기반 통합 테스트
  - **opt-in 방식:** `UNITY_PATH` 환경변수가 설정된 환경에서만 E2E 테스트 실행 (`go test -tags e2e ./...`)
  - 검증 항목: (1) 캐시된 Library/로 cold-start 회피 확인 (2) 실제 NUnit XML 출력 파싱 정확성 (3) exit code 매핑 (4) shadow workspace 경로 재매핑
  - "안정화"라는 테마의 완결 조건: fake 기반 단위 테스트를 넘어 실제 엔진 출력으로 계약 검증

  ### 5-2. AI 에러 컨텍스트 강화 (Scenario)
  - Host crash 시 dependent 인스턴스의 에러 메시지에 **Host의 exit code와 실패 유형**을 포함
  - 현재: `dependency "host" exited before reaching phase "compiling"`
  - 목표: `dependency "host" failed with exit 2 (compile error) before reaching phase "compiling"`
  - 구현: `doneChannels`의 타입 변경 (`chan struct{}` → `chan int` 또는 별도 result 조회) 으로 exit code 전파
  - AI 에이전트 가치: client의 exit 4를 보고 "타임아웃" 방향으로 잘못 디버깅하는 것을 방지; 즉시 Host 쪽 원인으로 점프 가능

  ### 5-3. NUnit 파라미터화 테스트 파싱
  - `[TestCase]`, `[Theory]`, `[TestCaseSource]` 등 파라미터화된 테스트 지원
  - **구현 주의사항:** Unity Test Framework의 NUnit XML에서 `<test-suite type="ParameterizedMethod">` 노드 아래에 개별 `<test-case>`가 그룹핑됨 — 파서가 이 suite type을 인식하여 그룹핑 메타데이터를 JSON에 반영해야 함
  - `xmlTestSuite`에 `Type` 속성 파싱 추가 → JSON 출력에 파라미터화 여부 표현
  - 실제 Unity 출력 XML 픽스처(`testdata/parameterized.xml`) 확보 필수 (E2E 파이프라인과 연계)

  ### 5-4. JSON 스키마 수렴
  - v1.0 정식 릴리즈 전 스키마 변경을 최소화하기 위한 계약 정리
  - Breaking changes를 이 버전에서 일괄 처리하여 v0.6+ 이후 안정성 확보

- **릴리즈 게이트:**
  1. 실제 Unity 환경에서 E2E 테스트가 통과하고, fake 기반 테스트와 결과가 일치할 것
  2. Host crash 시 dependent 인스턴스의 에러 메시지에 Host의 exit code가 포함될 것
  3. 파라미터화된 NUnit 테스트 결과가 개별 항목으로 구조화되어 JSON에 반영될 것
  4. 스키마 변경이 예측 가능한 수준으로 관리되고 공식 문서에 반영될 것

## ✅ v0.5.1-beta (Hardening)
**테마:** 보안 수정, 계약 누락 보완, 입력 검증 강화

- **목표:** v0.5.0에서 드러난 보안 취약점과 출력 계약 누락을 일괄 수정
- **포함 수정:**
  - **runID 경로 순회 차단** — `history.go`에서 runID를 파일 경로에 직접 사용; `^[0-9]{8}-[0-9]{6}-[0-9a-f]{8}$` 형식 검증 추가로 임의 파일 읽기/쓰기 방지
  - **결과 저장 실패 알림 및 Exit 9 도입** — `resultStore.Save()` 실패 시 JSON 출력의 `warnings` 필드에 포함하되, 테스트 결과(exit 0~8)와 러너 자체의 시스템 실패를 구분하기 위해 **exit 9 (runner system error)** 를 신설. 테스트는 통과했지만 결과 저장·아티팩트 기록 등 러너 인프라가 실패한 경우에 반환. AI 에이전트가 "코드를 고칠 것인가 vs 디스크를 비울 것인가"를 즉시 판단 가능
  - **`skipped` 카운트 JSON 노출** — `parser.Result.Skipped`은 이미 파싱되나 `testplay run` 출력에 미포함; 에이전트가 통과/실패/스킵을 정확히 구분 가능
  - **타임아웃 음수 검증** — `config.Validate()`에서 `total_ms`, `compile_ms`, `test_ms`의 음수값 거부; AI가 자동 생성한 설정에서 즉시 만료되는 context 방지
  - **`list` 스캐너 파라미터 테스트 감지** — 소스 스캔 정규식에 `[TestCase]`, `[TestCaseSource]`, `[Theory]` 추가; v0.5.0 파싱 지원과 정합성 확보
- **릴리즈 게이트:**
  1. 비정상 runID 입력 시 에러 반환, 정상 포맷만 파일 경로로 사용될 것
  2. 저장 실패 시 exit 9를 반환하고, stdout JSON에 `warnings` 필드가 포함될 것
  3. `skipped` 카운트가 출력 JSON에 반영될 것
  4. 음수 타임아웃 설정 시 exit 5 (config error)로 거부될 것
  5. `testplay list` 출력에 `[TestCase]` 테스트가 포함될 것

## ✅ v0.6.0-beta (The Network Ready) — shipped 2026-04-03
**테마:** 시나리오 데이터 주입 + 운용 안전성 보강 + AI 디버깅 가속

- **목표:** 네트워크 협력 테스트의 필수 전제인 인스턴스별 환경 설정 주입을 확보하고, 장기 운용 시 자원 누적 문제를 해결하며, AI 에이전트의 실패 분석 속도를 개선
- **포함 기능:**

  ### 6-1. Scenario Context Injection (환경 변수 주입)
  - 시나리오 파일의 인스턴스 스펙에 `env` 필드 추가 (예: `{ "PORT": "7777", "ROLE": "HOST" }`)
  - Layer 5(오케스트레이터)가 `env` 값을 읽어 Layer 1(Unity 실행기)의 `exec.Cmd.Env`에 주입
  - 네트워크 테스트(NGO/Mirror)에서 Host/Client가 서로 다른 포트·역할로 기동하기 위한 최소 전제
  - AI 에이전트 가치: 시나리오 파일만으로 멀티 인스턴스 환경 설정을 완전히 선언적으로 관리 가능
  - **책임 경계:** 환경 변수 주입은 러너(OS 프로세스) 레벨에서만 보장. Unity C# 코드에서 `System.Environment.GetEnvironmentVariable()`로 명시적으로 수신하는 것은 사용자의 책임. 러너는 OS 환경변수 세팅까지만 계약

  ### 6-2. Artifact Retention (아티팩트 자동 정리)
  - `.testplay/results/` 및 `.testplay/runs/`에 쌓이는 과거 실행 결과의 자동 정리
  - 보존 정책: 최근 N개 또는 D일 이내 보존 (설정 가능), 기본값은 최근 30개
  - AI 폭주 시 디스크가 무한히 쌓이는 문제 방지

  ### 6-3. Windows 프로세스 그룹 Kill
  - 현재 Unix에서만 구현된 프로세스 그룹 kill(`runner_unix.go`)을 Windows에도 확장
  - `Job Object` 기반으로 Unity 프로세스 트리 전체를 context 취소 시 종료
  - Windows CI 환경에서의 좀비 프로세스 방지

  ### 6-4. Failure Excerpt (실패 발췌)
  - NUnit XML의 `<message>` 태그 내용과 `<stack-trace>`의 첫 번째 사용자 코드 프레임(user-space code line)을 기계적으로 결합하여 `tests[].excerpt` 필드로 제공
  - 러너가 스택 트레이스를 "이해"하거나 지능적으로 필터링하지 않음 — OS·테스트 모드별 스택 포맷 차이에 취약한 정규식 파싱을 배제하고, 구조화된 XML 태그만 활용
  - AI 에이전트가 전체 로그를 읽지 않고도 assertion 메시지 + 소스 위치를 즉시 파악 가능

  ### 6-5. Phase 정확도 개선
  - `running` 페이즈가 Unity 종료 후에 기록되는 현재 동작을 개선
  - **접근 방식 제약:** Unity stdout 텍스트 파싱(문자열 매칭)으로 상태 머신을 제어하지 않음 — stdout은 버퍼링 지연이 심하고, Unity 버전별 로그 텍스트가 예고 없이 변경되며, 사용자 코드(`Debug.Log`)의 오탐 위험이 있어 계약 일관성을 보장할 수 없음
  - **후보 전략:** (A) Unity C# 헬퍼가 테스트 시작 시 마커 파일(`.testplay/runs/../running.marker`)을 생성하도록 유도 — 정확하지만 C# 의존성 발생, (B) Unity 프로세스 시작 시점을 `running`으로 간주하고 정확도를 포기 — 단순하지만 컴파일 페이즈와 구분 불가
  - 구현 시 두 전략의 트레이드오프를 평가 후 결정; 정확도를 위해 비계약 소스에 의존하는 것은 금지

- **릴리즈 게이트:**
  1. ✅ 시나리오 파일의 `env` 필드가 Unity 프로세스의 환경변수로 주입되고, 인스턴스별로 다른 값이 적용될 것
  2. ✅ 보존 정책을 초과하는 과거 실행 결과가 자동으로 정리될 것
  3. ✅ Windows에서도 context 취소 시 Unity 자식 프로세스가 남지 않을 것
  4. ✅ 실패 테스트의 `excerpt` 필드에 NUnit `<message>` + 소스 위치가 포함될 것
  5. ✅ `running` 페이즈 전환이 비계약 소스(stdout 텍스트 매칭)에 의존하지 않을 것 — 전략 B 채택 (misleading phase 제거)

- **추가 하드닝 (릴리즈 후):**
  - `MergeEnv` Windows 대소문자 무시 (build-tagged `envKeysEqual`)
  - `Prune` run-ID 형식 필터링 + `keep <= 0` 방어 가드
  - `retention.max_runs` `*int` 포인터 (nil=기본 30, 0=비활성화)
  - `internal/runid` 패키지로 정규식 중복 제거
  - `taskkill` 실패 시 stderr 로그 + `os.ErrProcessDone` 반환

## ✅ v0.7.0-rc (Release Candidate) — shipped 2026-04-03
**테마:** 배포 파이프라인 및 초기 셋업

- **목표:** 정식 릴리즈를 앞두고 사용자 진입 장벽 낮추기 및 DX 향상
- **포함 기능:**
  - **`testplay init`** — testplay.json 생성 (Unity 경로 자동 탐색, 기본값 주입, `--force` 덮어쓰기)
  - **GoReleaser 연동** — `v*` 태그 푸시 시 darwin/linux/windows (amd64+arm64) 바이너리 자동 빌드·배포
  - **Cross-platform CI** — `go test ./...`를 ubuntu/macos/windows에서 push/PR마다 실행
  - **시나리오 모드 아티팩트 정리** — 멀티 인스턴스 실행 후에도 retention 정책에 따라 자동 정리; 같은 프로젝트를 공유하는 인스턴스는 중복 정리 방지
- **릴리즈 게이트:** ✅ 설치 경로가 단순해지고, 빈 프로젝트에서도 `init`과 기본 설정만으로 첫 실행 흐름을 재현할 수 있음

## ✅ v0.7.1 (Scenario race fix) — shipped 2026-04-03
**테마:** v0.7.0 스모크 검증 중 드러난 race 한 줄 픽스

- **목표:** v0.4.0-beta 이래 잠재해 있던 시나리오 fast-fail 경로의 race를 제거.
- **수정 내용:**
  - **`depReadyCh` re-check** — `depReadyCh` 와 `depDoneCh` 가 동시에 close되면 Go의 `select` 는 랜덤하게 한 쪽을 고름. 이전엔 `depDoneCh` 가 선택되면 즉시 fast-fail 했는데, 실제로는 dependency가 정상적으로 ready 신호를 친 직후 종료한 경우일 수도 있음. v0.7.1은 fast-fail 분기 진입 직전 `depReadyCh` 를 한 번 더 확인하는 inner-select 를 추가해 잘못된 실패 판정을 제거함. 코드: `internal/scenario/runner.go:120-126`.
- **릴리즈 게이트:** v0.7.0 스모크 시나리오 305 테스트 반복 실행에서 race 없음.

## ✅ v0.8.0 (The Honest Contract) — shipped 2026-04-23
**테마:** 문서화된 계약과 실제 동작의 일치

- **목표:** README에 솔직하게 기록한 세 가지 한계를 실제로 해소. 에이전트가 `list` 결과를 신뢰하지 못하고, exit code를 오독하며, 진행 상태를 stale하게 읽는 문제를 제거한다.
- **포함 기능:**

  ### 8-1. `testplay list` — run cache 기반 완전한 목록
  - `testplay run` 완료 후(exit 0 또는 3), NUnit XML에서 추출한 전체 테스트 목록을 `.testplay/list-cache.json`에 저장
  - `testplay list`는 cache가 있으면 `complete: true, source: "run_cache"`로 반환; 없으면 `complete: false, source: "static_scan"`으로 정적 스캔 결과 반환
  - 에이전트는 `complete` 필드 하나로 목록 신뢰도를 즉시 판단 가능
  - 출력 스키마 변경: `{"schema_version":"1", "complete": true, "source": "run_cache", "cached_run_id": "...", "tests": [...]}`
  - 시나리오 모드에서는 Library 캐시와 동일한 이유로 cache write-back 건너뜀

  ### 8-2. Exit code 6/7 — 예약에서 실제 반환으로
  - **Exit 6 구현:** Unity 종료 후 XML 없음 + 컴파일 에러 없음 경로에서 stderr를 패턴 매칭하여 라이선스 오류·빌드 타겟 미설치를 exit 2와 구분. 현재 이 케이스는 exit 2(컴파일 실패)로 잘못 반환되어 에이전트가 소스를 고치려 시도함
  - **Exit 7 구현:** shadow.Prepare, 결과 디렉토리 생성 등 Unity 실행 이전 파일 시스템 작업에서 `os.ErrPermission`이 확인되면 exit 7 반환. 현재는 exit 1 또는 exit 9로 혼입됨
  - README와 CLAUDE.md의 exit code 표에서 "reserved, never returned" 표기 제거

  ### 8-3. `testplay-status.json` — `seq` 필드 추가
  - `status.Writer`가 내부적으로 단조 증가하는 시퀀스 번호를 관리하고, 매 Write 시 `seq` 필드를 자동 주입
  - 에이전트는 이전 `seq`와 현재 `seq`를 비교해 stale 읽기를 즉시 감지 가능; `updated_at` 파싱 없이도 "아직 변화 없음"을 판단할 수 있음
  - 출력 스키마 변경: `{"schema_version":"1", "seq": 7, "phase": "running", ...}`
  - 기존 폴링 에이전트와 완전 하위 호환 (seq 무시하면 동일하게 동작)

- **릴리즈 게이트:**
  1. `testplay run` 후 `testplay list`가 `complete: true, source: "run_cache"`를 반환할 것
  2. Unity 라이선스 오류 시 exit 6, Unity 실행 이전 권한 오류 시 exit 7이 반환될 것
  3. `testplay-status.json`의 모든 쓰기에 `seq`가 포함되고, 호출마다 단조 증가할 것
  4. 위 세 변경이 기존 에이전트 동작을 깨지 않을 것 (기존 필드 유지, 새 필드만 추가)

> **Note:** SSE 기반 실시간 push는 별도 설계가 필요하며, PlayMode 네트워크 테스트 도입(v1.0 이후) 시점에 함께 설계한다.

## ✅ v0.9.0 (Network Primitives) — shipped 2026-04-24
**테마:** 프레임워크 무관 인스턴스 간 통신 + 크로스 인스턴스 실패 상관관계

- **목표:** v0.6 시나리오 모드의 단방향 ready 시그널을 양방향 메시지 버스로 확장. NGO/Mirror 어떤 프레임워크를 쓰든 사용자가 직접 host↔client 신호를 주고받을 수 있게 한다.
- **포함 기능:**

  ### 9-1. IPC 메시지 버스 (scenario-scoped)
  - 시나리오 실행 시 `.testplay/ipc/<scenario_run_id>/bus.ndjson` 생성
  - 각 인스턴스에 환경변수 `TESTPLAY_IPC_BUS=<absolute_path>` 자동 주입
  - 메시지 형식 (NDJSON): `{"seq": int, "ts": iso8601, "from": role, "to": role|"*", "kind": string, "payload": any}`
  - Atomic append (`os.OpenFile(O_APPEND|O_WRONLY)`) — 다중 writer 안전
  - 사용자 Unity 테스트 코드는 이 파일에 직접 줄 추가 (Go 의존성 없음, 어떤 언어에서도 사용 가능)

  ### 9-2. Cross-instance failure correlation
  - 시나리오 출력 스키마에 인스턴스별 `ipc_messages` 필드 추가
  - 어느 인스턴스가 실패했을 때, 그 인스턴스가 마지막으로 송수신한 메시지의 `seq`/`kind`를 출력에 포함
  - 예: `client` 실패 시 → "client received host's seq=3 (kind: ready), then sent seq=4 (kind: connected), then failed at test"

  ### 9-3. `events.ndjson` IPC 통합
  - 각 인스턴스의 `events.ndjson`에 송수신한 IPC 메시지를 `event_kind: "ipc_send"` / `"ipc_recv"`로 기록
  - 디버깅 시 한 인스턴스의 타임라인에서 IPC 트래픽까지 한 번에 추적 가능

- **명시적 비목표 (v1.0 이후로 미룸):**
  - NGO/Mirror 프레임워크 헬퍼는 v0.9 범위 아님 (v1.0)
  - 실시간 push (SSE/websocket) 아님 — 폴링 + atomic append만
  - 인스턴스 간 RPC 아님 — 단방향 메시지 (응답이 필요하면 사용자가 두 메시지로 구성)

- **릴리즈 게이트:**
  1. 시나리오 실행 시 모든 인스턴스에 `TESTPLAY_IPC_BUS` 환경변수가 절대 경로로 주입될 것
  2. 한 인스턴스에서 NDJSON 줄 append → 다른 인스턴스가 폴링으로 읽기 가능
  3. 시나리오 출력에 `instances[].ipc_messages` 필드가 송수신 마지막 메시지를 담고 있을 것
  4. host crash 시 `orchestrator_errors`에 "client saw host's last message: seq=N kind=K"가 포함될 것

## ✅ v0.9.1 (Scenario Orchestration Hotfix) — shipped 2026-06-05
**테마:** dogfooding에서 발견된 scenario 모드의 사용자 워크플로우 결함 해소

- **배경:** NGO 액션 RPG Phase F dogfooding에서 IPC 버스는 동작했지만, scenario orchestration 계층이 실제 multi-client 검증 워크플로우를 막는 문제가 발견됨.
- **포함 수정:**
  - `testplay run --scenario ... --filter X` / `--category X`가 모든 인스턴스의 Unity 실행 인자로 전파됨
  - `depends_on_phase` 필드를 정식 지원하여 dependent 인스턴스가 의존 대상에게 요구하는 phase를 명시 가능
  - `ready_phase`와 `depends_on_phase`의 의미를 분리하고, timeout/fast-fail 에러 메시지가 실제 대기 phase를 보고하도록 수정
  - scenario JSON을 strict validation으로 변경하여 알 수 없는 필드와 phase 충돌을 즉시 exit 5(config error)로 거부
  - `ready_timeout_ms` 문서화 강화: 기본 30초는 유지하되 대형 Unity 프로젝트는 scenario JSON에서 명시적으로 늘릴 수 있음
- **릴리즈 게이트:**
  1. scenario 모드에서 필터/카테고리가 모든 인스턴스에 동일하게 적용될 것
  2. `depends_on_phase: "running"`이 조용히 무시되지 않고 실제 대기 phase와 에러 메시지에 반영될 것
  3. 알 수 없는 scenario 필드는 silent failure가 아니라 validation error일 것
  4. v0.9 IPC bus와 failure correlation 계약이 회귀하지 않을 것

## ✅ v0.9.2 (Shadow Diagnostic Hotfix) — shipped 2026-06-05
**테마:** shadow cold import와 C# compile failure의 진단 분리

- **배경:** GNF_ 프로젝트 dogfooding에서 shadow workspace 초기 package/import 비용과 Unity compile invocation 실패가 C# compile failure처럼 보이는 문제가 발견됨.
- **포함 수정:**
  - two-phase compile invocation이 non-zero exit을 반환했지만 stderr에서 C# compile error가 발견되지 않으면 exit 2가 아니라 exit 6으로 분류
  - 진단 메시지에 "no C# compile errors"와 Unity/editor logs, package import, shadow cold import 확인 경로를 명시
  - 기존 `timeout_type: "compile"` 계약은 유지: phase deadline이 실제로 초과된 경우는 계속 exit 4
  - README exit code 표를 업데이트하여 exit 6을 license/build target뿐 아니라 Unity invocation/import failure까지 포함하도록 확장
- **릴리즈 게이트:**
  1. 실제 C# compile errors가 있는 경우만 exit 2로 남을 것
  2. C# compile errors 없는 compile invocation 실패는 exit 6 + 명확한 환경/Unity 진단 메시지를 반환할 것
  3. shadow cold import 비용 때문에 테스트 실패로 오독하지 않도록 README에 대응 경로가 문서화될 것

## ✅ v0.10.0 (The Warm-Editor Bridge) — shipped 2026-06-24
**테마:** 에디터가 열려 있을 때의 두 가지 물리적 병목 — shadow workspace 바이트 복사(용량)와 cold domain reload(시간) — 를, 에디터를 *우회*하지 않고 *통과*해서 제거한다. 계약은 그대로 유지되는 투명한 backend.

- **배경:** dogfooding에서 에디터가 열려 있으면(`Temp/UnityLockfile`) 매 실행마다 `Assets/`+`ProjectSettings/`+캐시된 `Library/`를 통째로 복사(`io.Copy`, reflink 없음)하고 batchmode를 cold start 한다. 자동 호출자(agent/CI)는 사람이 쓰는 Test Runner window를 쓸 수 없으므로 따뜻한 경로 자체가 없었다.
- **정체성 정합성 (Identity anchor 인용):** "make it faster in the editor → Test Runner window의 몫"은 *사람의* GUI를 전제로 한다. 브릿지는 자동 호출자에게 동일한 따뜻한 경로를, **변경 없는 JSON/exit-code 계약 뒤의 투명한 backend로만** 제공한다. warm/cold가 *결과*를 바꿀 수 있는 순간에는 항상 cold로 강등(Pristine Gate). 속도·용량 이득은 부수효과지 약속된 기능이 아니다.
- **포함 기능:**
  ### 10-1. 3-tier auto backend (`bridge → shadow → process`)
  - `runsvc.Service.Run`의 기존 shadow/process 블록 *앞에* 브릿지 분기 추가 (Backend 인터페이스 도입 없음 — 실행 엔진은 cold/warm 둘 뿐).
  - `--bridge`(선호, 단 Pristine Gate는 통과해야 함) / `--no-bridge`(금지) / config `bridge.enabled:false`. two-phase·scenario는 항상 cold.
  ### 10-2. `internal/bridge` Go 클라이언트
  - `Probe` 6-점 handshake 게이트(protocol version, project-path 일치, Unity-version 일치, liveness/staleness, idle 상태) + `.testplay/bridge/` 하 파일 기반 NDJSON `Client`(atomic request/response, status-stream tail, `.cancel`).
  ### 10-3. `unity.ExecuteBridge`
  - `unity.Execute`의 형제. 동일한 `RunResult`/exit code 반환. `parseResults`(브릿지가 쓴 `results.xml`) + 추출한 `classifyNoResults`(compile-errors sidecar→exit 2 / build-failed→exit 6) + `handleContextErr`(timeout→4 / signal→8) 재사용 → cold와 byte-identical 분류.
  ### 10-4. `unity/com.testplay.bridge` (C# UPM, greenfield, EditMode 한정)
  - opt-in `[InitializeOnLoad]`(`TESTPLAY_BRIDGE_ENABLE` / `ENABLE` sentinel, batchmode에서는 절대 비활성), `TestRunnerApi` 드라이버가 cold와 동일한 NUnit `results.xml` 작성, `CompilationPipeline`→`CSxxxx:` 프리픽스 compile-errors sidecar, `O_APPEND` progress stream, **Pristine Gate**(Play Mode 거부, compile-settle 대기, dirty-scene 공개).
  ### 10-5. 공개(disclosure)
  - `backend` 필드(`process`|`shadow`|`bridge`) 항상 존재(Output Design Rule #13). 비-pristine 상태는 `warnings`로 공개(결과를 바꾸는 차이는 공개가 아니라 cold fallback). `testplay-status.json` 스키마 불변.
- **릴리즈 게이트 (사전 검증 완료):**
  1. **Cold/bridge parity** — `e2e/bridge_parity_test.go`: 동일 fixture에서 `tests[]`·exit code·`errors[]`(절대경로 제외) 동일.
  2. **`ITestResultAdaptor.ToXml()` 충실도** — parameterized + 실패 케이스가 `parser.Parse`를 통해 cold `-testResults`와 동일 결과.
  3. **Compile-error parity** — `CSxxxx` 에러가 warm sidecar→`errors[]`로 cold stderr scrape와 동일(경로 제외).
  4. **TestRunnerApi 취소** — 긴 EditMode run 중단 시 에디터가 wedge되지 않고 다음 run이 신뢰 가능.
  5. **Windows atomic 파일** — `.testplay/bridge/`에서 1000회 req/resp 루프에 partial read/sharing violation 없음.
  - **검증 결과 (Unity 6000.3.8f1 + 2022.3.62f3 LTS):** 스파이크 1–5 + #32(LTS) 모두 통과 — byte-identical parity(parameterized+failing), warm sidecar 정확(cold는 Unity 6에서 under-report → issue #31), SIGINT→exit 8 + 에디터 복구, CI windows-latest atomic 0 torn, 2022.3에서 ToXml(#7) 정상. 증거: `docs/25`. (선택: 2021.3 LTS 추가 검증.)
- **명시적 비목표 (이후 버전):** PlayMode-warm, scenario/network warm orchestration, bridge-side hard cancellation.
- **버전 규칙 준수:** 새 태그 `v0.10.0` 발행 완료 (2026-06-24, GoReleaser 크로스플랫폼 바이너리 배포). 원격에 푸시된 태그는 절대 덮어쓰지 않는다.

## ✅ v0.11.0 (The Honest Contract Hardening) — shipped 2026-07-14
**테마:** AI agent에게 틀린 성공이나 중복 실행을 보여 주는 silent-wrong 경로를 fail-closed 계약으로 전환한다.

- **CLI·config 정직성:** cold 실행의 timeout/signal이 exit 4/8로 복구되고,
  필터가 0개 테스트를 선택하면 exit 10, Unity 실행 실패는 exit 1 + hint,
  잘못된 CLI 사용과 알 수 없는 config key는 exit 5를 반환한다. config는
  하나의 strict JSON 값만 허용한다.
- **목록·경로 정직성:** list cache schema 2가 전체 inventory 여부와
  test platform을 함께 기록한다. filtered run은 cache를 오염시키지 않고,
  `project_path`/`result_dir`는 config와 project 기준으로 고정된다.
- **scenario 격리:** 인스턴스별 `compare_run`, filesystem identity 기반
  동일-project 판정, Windows junction/case alias 격리, phase validation,
  exit 10이 exit 1–9를 가리지 않는 집계 규칙을 제공한다.
- **Bridge protocol 2:** request를 `bridge_session_id`와 Test Framework run
  GUID에 결속한다. foreign run은 결과로 받을 수 없고, `RunFinished` 뒤
  cleanup까지 authoritative inactive를 확인한 다음에만 terminal response를
  공개한다.
- **No replay:** 실행 전임이 증명된 `not_started`만 cold fallback한다.
  `possibly_started`, 손상·누락된 완료 XML, domain reload/transport ambiguity,
  terminal publish 실패는 하나의 exit 9로 끝나며 자동 재실행하지 않는다.
- **배포 안전성:** OS matrix는 fail-fast를 끄고 Linux/Windows에서 Go 1.22.12
  compatibility floor를 유지한다. macOS와 release build는 Go 1.26.4를 사용해
  macOS 26이 요구하는 Mach-O `LC_UUID`를 포함한다. 수동 Release Preflight가
  5개 archive, checksums, CLI version, Darwin UUID를 태그 전에 검사한다.
- **릴리즈 게이트:** Go test/vet/build/E2E compile, shell self-check 9/9,
  Unity package tests 11/11, warm sequential/foreign/domain-reload/editor-restart/
  pre-existing-broken-compile spikes, 실제 GNF_ bridge 1/1·3/3·49/49 및
  no-match exit 10. 상세 증거: `docs/27_v0.11.0_validation.md`.
- **업그레이드 규칙:** CLI와 `com.testplay.bridge`를 함께 v0.11.0으로 올린다.
  protocol 1과 2는 의도적으로 호환되지 않는다. 원격 태그는 절대 이동하지
  않으며 문제 발생 시 v0.11.1을 발행한다.
- **지원 범위:** protocol 2 warm 브릿지는 per-run GUID 상태·취소 API가 있는
  Unity 6(6000.3+)만 지원한다. Unity 2022.3에는 패키지 설치를 허용하지 않는다.

## 🚀 v1.0.0 (NGO Harness)
**테마:** v0.9 primitives 위에 NGO(Netcode for GameObjects) 전용 sugar

- **목표:** 사용자가 NGO 프로젝트에서 boilerplate 없이 host/client 테스트를 작성할 수 있게 한다. 본인 프로젝트(map/Assets)에서 dogfooding.
- **포함 기능:**
  - NGO `NetworkManager` 부트스트랩 헬퍼 (Unity C# 패키지로 배포, optional install)
  - `[NetworkTest]` 어트리뷰트 (host/client 역할별 테스트 분기)
  - `await Network.WaitForClientConnected()` 등 헬퍼 — 내부적으로 v0.9 IPC 버스 사용
  - NGO 버전 매트릭스 검증 (Netcode 1.x / 2.x)
- **릴리즈 게이트:** 본인 NGO 프로젝트에서 멀티플레이어 동기화 테스트가 testplay scenario로 안정적으로 재현될 것 (dogfooding)
- **명시적 비목표 (v1.x 이후로 미룸):**
  - Mirror, Photon, FishNet 등 다른 프레임워크 sugar
  - 스크린샷 기반 진단
  - SSE 실시간 push

---

## 🚫 아직 약속하지 않는 것 (Out of Scope for v1.0)

- **실패 진단 시 스크린샷 캡처**
  - 시스템 자원 최적화(`-nographics`) 원칙과 충돌하므로 v1.0 범위에서 제외
  - 구조화된 텍스트와 스택 트레이스 진단에 우선 집중
- **NGO 외 네트워크 하네스(Mirror, Photon, FishNet) 전용 내장 기능**
  - v0.9 IPC primitives는 프레임워크 무관, v1.0 NGO sugar는 본인 dogfooding 우선
  - 추가 프레임워크는 v1.x에서 사용자 수요 확인 후 검토
- **에디터 attach 모드 (Test Runner API 직접 호출)**
  - 정체성 락(contract layer ≠ speed layer) 위배 위험
  - shadow warm-cache 시간 실측 결과가 임계값을 넘을 때만 v1.x에서 재검토

## 🔮 Post-v1.0 (장기 목표)

- **Flaky Suspicion Contract (불안정성 재시도 계약)**
  - 비결정적 테스트 실패(exit 3) 자동 재시도 및 `"flaky_suspicion": true` JSON 힌트 제공
  - `--compare-run` 기반 회귀 판별과의 연계 설계 필요
  - 재시도로 인한 실행 시간 증가 트레이드오프 검토 필요 — 별도 설계 문서(RFC) 작성 후 착수
- **Execution Timeline Artifact (실행 타임라인 세밀화)**
  - 현재 events.ndjson에 기록 중인 페이즈 전환 이벤트를 Unity 내부(테스트 시작/종료, 어셈블리 로드 등)까지 확장
  - Unity 내부 이벤트 후킹을 위한 Unity C# 패키지 개발 필요 — Go 러너 범위를 넘어서므로 코어 안정화 이후 확장팩 형태로 진행
- **State Snapshot Diffing (스냅샷 테스트)**
  - 게임 상태 직렬화 → 기대값 비교 프레임워크
  - C# 헬퍼 라이브러리 필요 — "TestPlay Unity SDK" 패키지로 분리 제공
- **`testplay watch`**
  - 파일 변경 감지 및 섀도우 백그라운드 자동 재실행 기능
  - 코어 시나리오 실행 계약이 충분히 안정화된 뒤 도입 검토
- **추가 DX 기능**
  - Code Coverage 리포트 연동 등 부가 기능은 v1.0 이후 우선순위를 다시 검토
