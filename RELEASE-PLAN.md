# 📈 testplay Release Plan & Version History

**현재 버전:** `v0.3.0-beta`
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

## 🟢 v0.2.0-beta (The Editor Unlock)
**테마:** 에디터 락(Lock) 우회 및 섀도우 워크스페이스 구축

- **목표:** 개발자의 작업 흐름을 끊지 않는 백그라운드 격리 실행 환경 확보
- **포함 기능:**
  - `Temp/UnityLockfile` 감지 시 `.testplay-shadow/` 워크스페이스 자동 생성
  - OS별 심링크/Junction 처리를 통한 `Packages/` 연결, `Library/` 영구 캐시 보존
  - 심링크·컨텍스트 취소·FileMode 보존·롤백 안전성을 갖춘 프로덕션 수준 강화
  - 결과 JSON의 경로 재매핑(Path Remapping) — 에이전트는 원본 경로만 확인
  - `--shadow` (강제 활성화) / `--reset-shadow` (캐시 재구축) 플래그 도입
- **릴리즈 게이트:** 에디터가 켜진 상태로 `testplay run`을 실행해도 원본 워크스페이스를 오염시키지 않고, 결과 JSON과 artifact가 원본 기준 경로로 매핑될 것

## 🟢 v0.3.0-beta (Current: The Multi-Instance Core)
**테마:** 시나리오 기반 다중 실행의 뼈대

- **목표:** 여러 개의 유니티 프로세스를 띄우고 결과를 합치는 1차 코어 확장
- **v0.2 P1 backlog 해소 (다중 실행의 전제 조건):**
  - **runID UUID 기반 교체** — 1초 단위 타임스탬프 → UUID/nanosecond; 동시 실행 시 결과 파일 충돌 방지 (현재 `Medium` Known Limitation)
  - **`--config` flag 도입** — CWD 의존 제거; 오케스트레이터가 각 인스턴스에 다른 config 경로를 직접 지정 가능
  - **Per-run shadow 격리** — run-ID 기반 독립 shadow 디렉터리(`.testplay-shadow-<run_id>/`); 병렬 `testplay run` 안전성 확보 (현재 `Medium` Known Limitation)
  - **Exit 8 구현** — SIGINT/SIGTERM → exit 8, timeout → exit 4로 명확 구분 (현재 두 경우 모두 exit 4)
- **포함 기능:**
  - `testplay run --scenario <file>` 인터페이스 최초 도입
  - Role 기반(Host/Client) 다중 섀도우 워크스페이스 동시 실행
  - 개별 `results.xml`과 `status`를 단일 시나리오 결과 JSON으로 1차 통합
- **릴리즈 게이트:** 시나리오 파일로 2개 이상의 인스턴스가 동시 실행되고, 합쳐진 JSON 결과가 일관된 구조로 출력될 것

## 🟣 v0.4.0-beta (The Orchestrator)
**테마:** 정교한 프로세스 동기화 및 에러 트래킹

- **목표:** 다중 인스턴스가 실제 네트워크 검증 도구로 기능하기 위한 타이밍 제어 확보
- **포함 기능:**
  - Host/Client 기동 순서(Startup Ordering) 및 Ready Gating 도입
  - IPC 또는 로그/포트 기반 ready signal 대기 메커니즘 추가
  - 다중 실행 환경에 맞춘 Exit Code 세분화
  - JSON 내 `exit_code_reason` 필드 추가를 통한 실패 원인 명확화
- **릴리즈 게이트:** Host 프로세스가 Ready 상태가 된 뒤 Client가 정해진 순서로 접속하고, 실패 시 원인이 구조화된 결과에 반영될 것

## 🟠 v0.5.0-beta (The AI Contract)
**테마:** 테스트 파싱 고도화 및 스키마 수렴

- **목표:** 멀티 인스턴스 코어 안정화 이후, AI 에이전트를 위한 출력 규약 강화
- **포함 기능:**
  - `[TestCase]`, `[Theory]`, `[TestCaseSource]` 등 파라미터화된 테스트의 단계적 지원
  - v1.0 정식 릴리즈 전 JSON 스키마 수렴 단계 진행
  - Breaking changes를 가능한 한 줄이기 위한 계약 정리
- **릴리즈 게이트:** 주요 NUnit 파라미터 테스트 결과가 개별 항목으로 구조화되어 JSON에 반영되고, 스키마 변경이 예측 가능한 수준으로 관리될 것

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
