# 📈 testplay Release Plan & Version History

**현재 버전:** `v0.4.2-beta`
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

## 🟠 v0.5.0-beta (The AI Contract)
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

## 🟤 v0.6.0-RC (Release Candidate)
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
  - v0.6.0-RC 기반 버그 픽스 및 전체 안정성 보강
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

- **`testplay watch`**
  - 파일 변경 감지 및 섀도우 백그라운드 자동 재실행 기능
  - 코어 시나리오 실행 계약이 충분히 안정화된 뒤 도입 검토
- **추가 DX 기능**
  - Code Coverage 리포트 연동 등 부가 기능은 v1.0 이후 우선순위를 다시 검토
