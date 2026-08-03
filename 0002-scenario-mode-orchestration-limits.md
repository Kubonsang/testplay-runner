# Case Study 0002: Scenario Mode Orchestration Limits Discovered

> 2026-06-05 dogfooding에서 발견된 testplay scenario 모드의 실용성 결함

- **Date:** 2026-06-05
- **Project:** NGO 액션 RPG (private), `feat/ROOM-TRAVERSAL-PORT-01` 브랜치
- **Test target:** Phase F multi-client NGO synchronization 검증
- **testplay version:** v0.9.x

---

## 한 줄 요약

> **testplay v0.9의 scenario 모드는 IPC 버스 자체는 작동하지만, 사용자 워크플로우 측면의 오케스트레이션 결함(필터 전파 실패, phase 핸드셰이크 불일치, 고정 타임아웃)으로 인해 실제 NGO multi-client 검증에 사용 가능한 상태가 아니다.**

---

## 배경

`ROOM-TRAVERSAL-PORT-01` 태스크의 Phase F는 host+client 두 인스턴스를 통한 cross-instance NGO 동기화 검증이 목표였다. Phase A~E에서 단일 인스턴스 (shadow) 검증은 완료된 상태였다.

scenario 모드를 사용하기 위한 환경:

```json
// testplay.scenario.json
{
  "schema_version": "1",
  "instances": [
    { "role": "host",   "config": "testplay.json", "ready_phase": "running" },
    { "role": "client", "config": "testplay.json", "depends_on": "host" }
  ]
}
```

---

## 1. 인프라 관점: 부분적으로 작동 (Partial Pass)

### 작동한 부분

**단일 인스턴스 (shadow) 모드:**
```
testplay run --shadow --filter "ROOM_TRAVERSAL_PORT_Tests|TURN_CYCLE_Logic_Tests"
→ 13/13 PASS (exit 0)
```

NGO host 모드로 `ServerBuild_BindsRoomsToPhysicalTraversalAdapter` 등 권위 로직 검증 완료. 이전 Case Study 0001과 동일한 신뢰도의 결과.

**Cross-instance failure correlation:**
host의 exit 3과 client의 exit 4가 `orchestrator_errors` 필드에 정확히 보고됨. 이 부분은 v0.9 release notes에서 약속한 기능이 동작함을 확인.

### 작동하지 않은 부분

**scenario 모드 (host + client):**
```
testplay run --scenario testplay.scenario.json
→ host:   exit 3, total 199, passed 191, failed 8
→ client: exit 4 (시작 실패)
→ orchestrator_errors:
   instance "client" timed out waiting for "host" to reach phase "compiling" (30000ms)
```

2회 반복 시도 (warm shadow 포함) **모두 동일하게 재현**. transient 문제가 아닌 구조적 결함.

---

## 2. 발견된 세 가지 결함

### 결함 A: `--filter`가 scenario 인스턴스에 전파되지 않음

**증상:**
```bash
testplay run --scenario S --filter X
# 기대: host와 client 모두 필터 X로 실행
# 실제: host와 client 모두 전체 suite(199개) 실행, 필터 무시
```

**위험성:**
silent failure. 에러 메시지 없이 사용자 의도와 다른 동작. 199 테스트 × 2 인스턴스 = 398 실행이 끝난 후에야 발견.

**워크어라운드:**
별도 축소 config (`testplay.json` 별도 작성 + assembly/category 지정)를 만들어 scenario가 그것을 참조하게 함. 사용자 부담.

### 결함 B: Client가 host의 "compiling" phase를 30초 고정 대기

**증상:**
- scenario JSON에 `host.ready_phase: "running"`을 명시해도
- client는 "compiling" phase를 30초 동안 기다림
- 30초 내 신호 수신 실패 시 client exit 4로 시작조차 못 함

**대형 프로젝트에서의 문제:**
shadow workspace 준비 + Unity 컴파일이 30초를 초과하는 프로젝트에서는 client가 영원히 시작 못 함. 본 프로젝트(199 tests, 정규 Unity NGO 프로젝트)에서 100% 재현.

**시도한 우회 (실패):**
```json
{ "role": "client", "depends_on": "host", "depends_on_phase": "running" }
```
`depends_on_phase` 필드를 추가했으나 **silent하게 무시**됨. client는 여전히 "compiling"을 기다리다 타임아웃.

**위험성:**
- ready_phase 핸드셰이크가 문서화된 동작과 일치하지 않음
- 사용자가 스키마에 없는 필드를 추가해도 경고 없음
- 30초 타임아웃이 옵션으로 노출되지 않음

### 결함 C: 사전 존재 실패가 scenario 결과를 오염시킴

**증상:**
scenario 모드는 인스턴스별로 전체 suite를 실행한다. 사용자 프로젝트의 FIX01/FIX02 8개 사전 실패가 main 브랜치에서도 동일하게 실패하지만, scenario 모드의 host는 이를 자기 결과로 포함하여 exit 3을 반환한다.

새로 추가한 ROOM_TRAVERSAL_PORT NGO 테스트는 모두 pass했지만 host의 최종 exit code는 3이다.

**위험성:**
scenario "성공"(양쪽 exit 0)을 판단하려면 사전 실패를 사용자가 직접 처리해야 함. CI 자동화에서 "scenario passed" 판정이 어려움.

---

## 3. 추가 발견: 의도하지 않은 함정들

### Config 경로 해석

scenario JSON 내 `config: "testplay.json"`은 **scenario 파일이 있는 디렉터리 기준**으로 해석된다. scenario 파일을 `/tmp` 등 프로젝트 밖에 두면 `testplay.json not found`로 실패한다.

→ 워크어라운드: scenario 파일을 반드시 프로젝트 루트에 둠. 문서에 명시 필요.

### Host 단일 인스턴스 빠른 실행은 정상

`ServerBuild_BindsRoomsToPhysicalTraversalAdapter` 테스트가 host에서 0.014~0.066s로 빠르다. unity-ngo-test-guardrails Rule 4의 "30ms 미만 의심" 신호 근처지만, 검증 대상이 RPC 왕복이 아닌 **서버측 graph build + bind (권위 로직, 동기적)**이므로 host에서 빠른 것이 정상.

→ Case Study 0001 후속 분석과 일관됨. unity-ngo-test-guardrails 스킬의 detection signal은 host-only 환경에서 false positive를 유발할 수 있음 (별도 스킬 개정 권장).

---

## 4. 담담한 결론

이 케이스를 있는 그대로 요약하면 다음과 같다.

> "testplay v0.9의 IPC 메시지 버스 자체는 작동하지만, scenario 모드의 오케스트레이션 계층(필터 전파, phase 핸드셰이크, 타임아웃)은 사용자 워크플로우에 맞춰져 있지 않다. 단일 host 인스턴스 + 합성 맵 NGO 테스트가 현재 신뢰 가능한 경로이며, 진짜 cross-instance multi-client 검증은 testplay 도구 자체의 개선 없이는 불가능하다."

도구는 절반만 작동한다. 인프라(IPC, 프로세스 관리)는 통과, 오케스트레이션(사용자 워크플로우)은 미통과.

---

## 5. testplay 프로젝트에 미치는 영향

### v1.0 release readiness 재평가 필요

이전 RELEASE-PLAN v3에서 v1.0을 "Unity 한정 계약 안정화"로 정의했다. 하지만 scenario 모드가 실용적으로 사용 불가능하다면, **"안정화된 계약"의 핵심 가치 제안 중 하나(NGO multi-client 검증 자동화)가 작동하지 않는 상태**다.

다음 중 하나를 선택해야 한다:

**옵션 A: v1.0 GA 전에 scenario 모드 결함 해결**
- T-6 (scenario 오케스트레이션 결함) 신규 추가
- v1.0 RC 단계에서 해결 → GA
- 일정 지연 발생 가능

**옵션 B: v1.0을 "single-instance only" 정체성으로 좁힘**
- scenario 모드를 v1.0에서 experimental로 격리
- v1.1에서 본격 해결
- v1.0 출시 일정 유지, 대신 scenario 약속 축소

**옵션 C: v1.0 일정을 유지하고 한계를 명시**
- README의 의도적 한계(P-?)로 scenario 모드 제약 명시
- 외부 사용자에게 "v1.x 동안 scenario는 host-only fallback 권장" 안내
- 가장 정직한 옵션이지만 핵심 가치 제안 약화

본인의 NGO 액션 RPG dogfooding에서 cross-instance 검증이 필요한 시점이 결정 기준이 된다.

### IPC 버스 안정성과 scenario 안정성의 분리

v0.9에서 약속한 것:
- IPC 버스 atomic append → 작동
- Cross-instance failure correlation → 작동
- events.ndjson IPC 통합 → 작동

**약속하지 않은 것** (사용자가 합리적으로 기대했을 수 있는 것):
- `--filter` + scenario 조합 동작
- ready_phase 핸드셰이크 정확성
- 타임아웃 옵션 노출
- 사전 실패 격리

이 갭이 발견된 것은 정상적인 dogfooding의 결과다.

---

## 6. 무엇을 배웠는가

### 1. "기능 출시"와 "기능 사용 가능"은 다르다

v0.9에서 IPC 버스를 출시했고 기술적으로 작동한다. 하지만 사용자 워크플로우의 빈틈 (`--filter` 전파, phase 핸드셰이크) 때문에 실제로는 쓸 수 없다. 다음 릴리즈부터는 **"기능 출시 = 한 가지 dogfooding 워크플로우에서 처음부터 끝까지 사용 가능"** 으로 게이트를 강화해야 한다.

### 2. Silent failure는 가장 비싼 실패다

`--filter` 무시, `depends_on_phase` 무시 — 모두 에러 없이 다른 동작을 한다. 이런 silent failure는 사용자가 30분 또는 그 이상을 낭비한 후에 발견된다. v1.0 계약 동결 전에 모든 무시되는 옵션을 **명시적 에러**로 전환해야 한다.

### 3. dogfooding 한 사이클은 단위 테스트 100개보다 가치 있다

이 한 번의 ROOM-TRAVERSAL-PORT-01 검증 세션이 testplay scenario 모드의 세 가지 결함을 동시에 노출했다. 단위 테스트는 이 결함들을 잡지 못한다. **사용자 워크플로우 결함은 사용자 워크플로우로만 발견된다.**

---

## 7. 다음 액션

### testplay 측 (필수)

1. **`--filter`의 scenario 인스턴스 전파 구현**
   - 또는 명시적으로 "scenario 모드에서 `--filter` 미지원" 에러 반환

2. **client 대기 phase 동적화**
   - `host.ready_phase`와 `client.depends_on_phase`를 명시적으로 매핑
   - scenario JSON 스키마에 검증 추가 (알 수 없는 필드 사용 시 에러)

3. **scenario 타임아웃 옵션 노출**
   - `client.ready_timeout_ms` 같은 명시적 필드
   - 기본값 30s 유지하되 사용자가 조정 가능

4. **사전 실패 격리 옵션 검토**
   - `scenario.expected_baseline_failures` 같은 필드로 사전 실패 허용
   - 또는 명확한 문서로 "scenario는 전체 suite green 상태에서만 의미 있음" 안내

### 본 프로젝트 측 (대기)

- 위 4가지 중 최소 (1)과 (2)가 해결되기 전까지 Phase F의 cross-instance 검증은 보류
- 단일 host 인스턴스 + 합성 맵 NGO 테스트가 신뢰 경로로 유지
- `_deferEntryEnabled` 활성화는 수동 2인 플레이테스트로 진행

---

## Raw 데이터

### 단일 인스턴스 성공 run
```
testplay run --shadow --filter "ROOM_TRAVERSAL_PORT_Tests|TURN_CYCLE_Logic_Tests"
→ exit 0, 13/13 passed
```

### scenario 실패 run
```
testplay run --scenario testplay.scenario.json
→ host:   exit 3, 199 total, 191 passed, 8 failed (사전 실패)
→ client: exit 4 (시작 실패)
→ orchestrator_errors:
   "instance \"client\" timed out waiting for \"host\" to reach phase \"compiling\" (30000ms)"
```

세부 run JSON은 본 프로젝트의 private 저장소에 보관.

---

## References

- Case Study 0001 — AI Agent Autonomous Debug Loop (성공 사례, 비교 자료)
- ADR 0002 — Polling + Atomic Append (IPC 버스 설계 결정)
- ADR 0003 — NGO Harness 분리 (NGO 검증의 책임 분리)
- v0.9.0 Release Notes — Network Primitives
- RELEASE-PLAN v3 Section 7 — v1.3 IPC Reliability (관련 향후 계획)

이 케이스 스터디는 RELEASE-PLAN v3에 새로운 미완 한계 **T-6: scenario 모드 오케스트레이션 결함** 신규 등록을 정당화한다.
