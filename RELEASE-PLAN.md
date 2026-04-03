# 📈 testplay Release Plan & Version History

**현재 버전:** `v0.6.0-beta`
**목표:** 단순한 로컬 테스트 래퍼를 넘어, AI 에이전트에 최적화된 시나리오 기반 멀티 인스턴스 러너로 단계적으로 확장

> 이 문서는 확정 약속이 아니라, 베타 진행 상황에 따라 조정될 수 있는 릴리즈 계획을 정리한 것입니다.  
> 각 마일스톤의 릴리즈 게이트는 기능 존재 여부보다 재현성과 계약 일관성을 기준으로 판단합니다.

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

## 🟤 v0.7.0-RC (Release Candidate)
**테마:** 배포 파이프라인 및 초기 셋업

- **목표:** 정식 릴리즈를 앞두고 사용자 진입 장벽 낮추기 및 DX 향상
- **포함 기능:**
  - `testplay init` 명령어 도입
  - GoReleaser 연동을 통한 Windows/macOS/Linux Pre-built 바이너리 배포 체계 구축
  - 공식 문서(README, Docs) 전면 개편
- **릴리즈 게이트:** 설치 경로가 단순해지고, 빈 프로젝트에서도 `init`과 기본 설정만으로 첫 실행 흐름을 재현할 수 있을 것

## 🚀 v1.0.0 (Official Release)
**테마:** Scenario-Driven Host/Client 멀티 인스턴스 러너

- **목표:** AI 바이브 코딩 환경에서 코옵 게임 로직 검증을 1급 시나리오로 다룰 수 있는 기반 확보
- **포함 기능:**
  - v0.7.0-RC 기반 버그 픽스 및 전체 안정성 보강
  - Scenario-driven Host/Client 멀티 인스턴스 실행 흐름 정리
  - 시나리오 결과 통합, 실패 원인 구조화, 기본적인 orchestration 계약 확립
- **릴리즈 게이트:** 실제 프로젝트 환경에서 반복 실행 가능한 시나리오 테스트가 안정적으로 재현되고, 결과 해석이 일관되게 가능할 것

---

## 🚫 아직 약속하지 않는 것 (Out of Scope for v1.0)

- **실패 진단 시 스크린샷 캡처**
  - 시스템 자원 최적화(`-nographics`) 원칙과 충돌하므로 v1.0 범위에서 제외
  - 구조화된 텍스트와 스택 트레이스 진단에 우선 집중
- **특정 네트워크 하네스(NGO, Mirror) 전용 내장 기능**
  - 코어는 프레임워크 종속 기능보다 시나리오 실행과 오케스트레이션 계층에 집중
  - 프레임워크 비종속적인 실행 코어를 우선 지향

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
