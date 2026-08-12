# testplay-runner

**Unity의 망가진 테스트 러너를 AI 에이전트용 안정적인 계약 레이어로 감싸는 Go CLI — 명확한 exit code, JSON 출력, silent failure 없음.**

한국어 | [English](README.md)

---

Unity의 원시 CLI는 자동화에 적합하지 않습니다. 컴파일 실패에도 종료코드 0을 반환하고, 결과는 XML로만 출력되며, 진행 상황을 알 수 없고, 오류 유형이 모호합니다. `testplay`는 AI 에이전트와 CI 파이프라인을 위해 설계된 6개의 명령으로 이 모든 문제를 해결합니다.

## testplay는 누구를 위한 도구인가

testplay는 **계약 레이어(contract layer)이지, 속도 레이어가 아닙니다.** 두 종류의 사용자:

- **AI 에이전트와 CI 파이프라인** — 명확한 exit code, 구조화된 JSON, 폴링 가능한 진행 파일이 필요한 자동화 호출자. testplay는 이들을 위해 설계됐습니다.
- **인간 개발자의 일상 TDD** — Unity의 Test Runner 창을 그대로 쓰세요. testplay는 ms 단위 반복과 경쟁하지 않습니다. *자동화된* 경로를 신뢰 가능하게 만드는 게 역할입니다.

AI 에이전트가 Unity 테스트를 반복 실행한다면, testplay의 모든 일은 매 반복의 결과를 명확하게(legible) 만드는 것입니다. 개별 테스트 실행 속도는 testplay의 최적화 대상이 아닙니다 — 에이전트 루프의 병목은 모델 추론 시간이지, Unity 시작 시간이 아닙니다.

## 해결하는 문제

| 문제 | 해결책 |
|---|---|
| 컴파일 실패에도 종료코드 0 반환 | 컴파일 오류는 exit 2, 테스트 실패는 exit 3으로 명확히 구분 |
| XML 전용 출력 | 모든 stdout을 `schema_version` 포함 JSON으로 출력 |
| 실행 전 검증 없음 | `testplay check`로 Unity 실행 전 환경 사전 검증 |
| 진행 상황 불투명 | 실행 중 `testplay-status.json`을 원자적으로 업데이트 |
| 타임아웃 유형 모호 | JSON에 `timeout_type: compile / test / total` 명시; `compile_ms` + `test_ms` 설정 시 two-phase 실행으로 컴파일/테스트 타임아웃 분리 |
| 회귀 추적 불가 | `--compare-run`으로 `new_failures` 비교 |
| 플랫폼별 경로 차이 | 모든 응답에 절대경로 + 상대경로 동시 제공 |
| 실행 없이 테스트 탐색 불가 | `testplay list`로 알려진 어트리뷰트 정적 스캔 — 커스텀 어트리뷰트 누락 (Known Limitations 참조) |
| Unity 에디터가 프로젝트 잠금 보유 | 적격 테스트는 열린 에디터에서 실행(`backend: "bridge"`); 실행 전 거절은 `.testplay-shadow/`로 폴백할 수 있지만, 실행 가능성이 생긴 뒤의 불명확성은 재실행 없이 exit 9 |

## 설치

**사전 빌드 바이너리 (권장):**

[GitHub Releases](https://github.com/Kubonsang/testplay-runner/releases)에서 다운로드 — darwin/linux (amd64/arm64), windows (amd64).

**소스에서 빌드:**

```bash
git clone https://github.com/Kubonsang/testplay-runner.git
cd testplay-runner
go build -o testplay ./cmd/testplay
```

크로스 컴파일:

```bash
GOOS=windows GOARCH=amd64 go build -o testplay.exe ./cmd/testplay
```

## 설정

`testplay init`으로 `testplay.json`을 생성합니다:

```bash
testplay init --unity-path /path/to/Unity
```

또는 프로젝트 루트에 직접 생성합니다:

```json
{
  "schema_version": "1",
  "unity_path": "/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity",
  "project_path": "/path/to/your/UnityProject",
  "test_platform": "edit_mode",
  "timeout": {
    "total_ms": 300000,
    "compile_ms": 60000,
    "test_ms": 240000
  },
  "result_dir": ".testplay/results",
  "retention": {
    "max_runs": 30
  }
}
```

`unity_path`를 생략하면 `UNITY_PATH` 환경변수로 폴백합니다.
`project_path`를 생략하면 `testplay.json`이 위치한 디렉터리가 기본값이 됩니다. 상대 경로 `project_path`는 프로세스 cwd가 아닌 config 파일의 디렉터리 기준으로 해석됩니다.
`test_platform`은 `"edit_mode"` (기본값) 또는 `"play_mode"`를 허용합니다. Unity CLI에 `-testPlatform EditMode|PlayMode`로 전달됩니다.
`result_dir`는 `testplay result`가 읽는 실행 이력 JSON 저장 위치를 제어합니다. 상대 경로(기본값 `.testplay/results` 포함)는 `project_path` 기준으로 해석되어 history와 아티팩트가 항상 같은 곳에 위치합니다.
반면 run별 아티팩트(`results.xml`, `summary.json`, `manifest.json`, `stdout.log`,
`stderr.log`, `events.ndjson`)는 항상
`<project_path>/.testplay/runs/<run_id>/` 아래에 저장됩니다.
`retention.max_runs`는 오래된 run 결과/아티팩트의 자동 정리를 제어합니다 (기본값 30). `0`으로 설정하면 정리를 비활성화합니다.
`testplay.json`의 알 수 없는(오타) 키는 조용히 무시되지 않고 거부됩니다(exit 5, 에러가 해당 키를 명시). `schema_version`은 `"1"`이어야 합니다.

**타임아웃 설정:**
- `total_ms` (기본값 300000): 전체 실행의 외부 안전망 데드라인.
- `compile_ms` + `test_ms`: **반드시 둘 다 함께 설정해야** two-phase 실행이 활성화됨 — Unity가 컴파일만 먼저 실행(`compile_ms` 데드라인), 이후 테스트 실행(`test_ms` 데드라인). 단계별 타임아웃이면 `timeout_type: "compile"` 또는 `"test"`가 나오고, 바깥 `total_ms`가 먼저 만료되면 `"total"`이 나올 수 있습니다. 하나만 설정하면 validation error.
- 둘 다 설정하지 않으면 single-phase 실행 (컴파일+테스트를 Unity 한 번 호출로 처리, `total_ms` 기준).

**브릿지 설정 (v0.10.0):** 선택적 top-level `"bridge": { "enabled": false }` 블록으로 웜 에디터 브릿지를 완전히 비활성화할 수 있습니다(생략 시 기본값: 활성). `compile_ms`와 `test_ms`를 둘 다 설정(two-phase)하면 해당 실행도 cold 경로를 강제합니다. [웜 에디터 브릿지](#웜-에디터-브릿지) 참조.

> **참고:** PlayMode 네트워크 하네스와 NGO 오케스트레이션은 아직 미지원입니다.

## 명령어

### `testplay version`

현재 testplay 버전을 JSON으로 출력합니다.

```bash
testplay version
```

```json
{
  "schema_version": "1",
  "version": "v0.13.0-rc.1"
}
```

---

### `testplay init`

`testplay.json` 설정 파일을 합리적인 기본값으로 생성합니다. 새 프로젝트를 시작할 때 한 번 실행합니다.

```bash
testplay init --unity-path /path/to/Unity
testplay init --test-platform play_mode
testplay init --force  # 기존 testplay.json 덮어쓰기
```

```json
{
  "created": "testplay.json",
  "unity_path": "/path/to/Unity",
  "project_path": "/current/directory"
}
```

Unity 경로 해석 우선순위: `--unity-path` 플래그 > `UNITY_PATH` 환경변수 > 빈 값 (경고 포함).
`testplay.json`이 이미 있으면 exit 5 (`--force`로 덮어쓰기 가능). `--test-platform`이 유효하지 않으면 exit 5.

---

### `testplay check`

Unity 경로, 프로젝트 경로, 설정 파일을 사전 검증합니다. 가장 먼저 실행하세요.

```bash
testplay check
```

```json
{
  "schema_version": "1",
  "ready": true
}
```

실패 시:

```json
{
  "schema_version": "1",
  "ready": false,
  "hint": "set UNITY_PATH or add unity_path to testplay.json"
}
```

종료코드 0 = 준비됨. 종료코드 1 = 의존성 누락 (`hint` 필드 참조). 종료코드 5 = 설정 파일 오류.

---

### `testplay list`

Unity를 실행하지 않고 `*.cs` 파일에서 `[Test]`, `[UnityTest]`, `[TestCase]`, `[TestCaseSource]`, `[Theory]` 어트리뷰트를 정적으로 스캔합니다.

**이 결과는 완전한 테스트 목록이 아닌 최선의 추정값입니다.** 커스텀 테스트 어트리뷰트(`[NetworkTest]`, `[IntegrationTest]`, 프로젝트 전용 기반 클래스 등)는 조용히 누락됩니다. 출력 결과만으로는 무엇이 빠졌는지 알 수 없습니다.

실용 지침:
- 이미 존재하는 걸 아는 테스트의 `--filter` 후보 생성에 사용하세요.
- 전체 커버리지가 중요한 경우 `--filter` 없이 `testplay run`을 실행하세요. Unity가 직접 모든 테스트를 탐색합니다.
- `list`에 없는 테스트가 실제로 존재하고 실행될 수 있습니다.

```bash
testplay list
```

성공적인 `testplay run`(exit 0 또는 3) 이후에는 전체 테스트 목록이 캐시됩니다. 이후 `list` 호출은 그 캐시에서 `complete: true`로 반환합니다:

```json
{
  "schema_version": "1",
  "complete": true,
  "source": "run_cache",
  "cached_run_id": "20250325-143000-a3f8b2c1",
  "tests": ["MyTests.PlayerTests.TestJump", "MyTests.PlayerTests.TestRun"]
}
```

첫 성공 실행 전에는 `list`가 정적 스캔으로 폴백합니다:

```json
{
  "schema_version": "1",
  "complete": false,
  "source": "static_scan",
  "tests": ["MyTests.PlayerTests.TestJump", "MyTests.PlayerTests.TestRun"]
}
```

---

### `testplay run`

설정된 `test_platform` (`edit_mode` 또는 `play_mode`)으로 Unity 테스트를 실행합니다. 진행 상황은 `testplay-status.json`에 스트리밍됩니다.

```bash
testplay run
testplay run --filter TestJump
testplay run --category Smoke
testplay run --compare-run 20250301-102200-a3f8b2c1
testplay run --config path/to/testplay.json
testplay run --shadow              # 에디터 락 없이 강제로 섀도우 워크스페이스 사용
testplay run --clear-cache         # 캐시된 Library 제거 후 섀도우 워크스페이스 생성
testplay run --bridge              # 웜 에디터 브릿지 선호 (단, Pristine Gate는 통과해야 함)
testplay run --no-bridge           # cold shadow/process 경로 강제
testplay run --scenario scenario.json  # 멀티 인스턴스 동시 실행
```

`--scenario`와 함께 사용하면 `--filter`, `--category`, `--shadow`, 캐시 플래그가 모든 시나리오 인스턴스에 전파됩니다(시나리오 모드는 항상 cold 실행). 전역 `--compare-run` 플래그는 시나리오 모드에서 거부됩니다(exit 5) — baseline은 인스턴스별 저장소이므로, 시나리오 파일의 각 인스턴스에 해당 role 자신의 이전 `run_id`를 `"compare_run"` 필드로 지정하세요.

**`backend` 필드 (v0.10.0):** 모든 `run` 결과에는 어떤 엔진이 결과를 만들었는지 알려주는 `backend` 필드(`"process"`, `"shadow"`, `"bridge"`)가 **항상** 포함됩니다([웜 에디터 브릿지](#웜-에디터-브릿지) 참조).

**전체 통과 (exit 0):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "exit_code": 0,
  "backend": "process",
  "total": 2,
  "passed": 2,
  "failed": 0,
  "skipped": 0,
  "tests": [
    {
      "name": "MyTests.PlayerTests.TestJump",
      "result": "Passed",
      "duration_s": 0.006
    },
    {
      "name": "MyTests.PlayerTests.TestRun",
      "result": "Passed",
      "duration_s": 0.004
    }
  ],
  "new_failures": null
}
```

**테스트 실패 (exit 3):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "exit_code": 3,
  "backend": "process",
  "total": 10,
  "passed": 9,
  "failed": 1,
  "skipped": 0,
  "tests": [
    {
      "name": "MyTests.PlayerTests.TestJump",
      "result": "Failed",
      "message": "Expected 1 but was 0",
      "excerpt": "Expected 1 but was 0 (at PlayerTests.cs:42)",
      "file": "Assets/Tests/PlayerTests.cs",
      "absolute_path": "/path/to/UnityProject/Assets/Tests/PlayerTests.cs",
      "line": 42
    }
  ],
  "new_failures": null
}
```

**컴파일 실패 (exit 2):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "exit_code": 2,
  "backend": "process",
  "total": 0,
  "passed": 0,
  "failed": 0,
  "skipped": 0,
  "tests": [],
  "errors": [
    {
      "file": "Assets/Scripts/Player.cs",
      "absolute_path": "/path/to/UnityProject/Assets/Scripts/Player.cs",
      "line": 17,
      "message": "CS0103: The name 'speed' does not exist in the current context"
    }
  ],
  "new_failures": null
}
```

**빌드/라이선스 실패 (exit 6):**

Unity 배치 실행이 NUnit XML도 안 만들고 C# 컴파일 에러도 없이 종료한 경우 — 보통 라이선스 활성화 실패 또는 빌드 모듈 미설치. 소스가 아닌 Unity 환경(라이선스, 플랫폼 모듈 설치)을 고치세요.

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000-a3f8b2c1",
  "exit_code": 6,
  "backend": "process",
  "tests": [],
  "errors": []
}
```

**섀도우 워크스페이스 권한 오류 (exit 7):**

per-run `.testplay-shadow-<run_id>/` 워크스페이스 준비가 파일시스템 권한 오류로 실패한 경우 (예: 프로젝트 디렉토리 쓰기 불가, `.testplay/` 가 다른 사용자 소유). 경로/소유권을 고치세요.

```json
{
  "schema_version": "1",
  "exit_code": 7,
  "error": "runsvc: prepare shadow workspace: ... permission denied"
}
```

**시나리오 모드 (`--scenario`) — 집계 출력:**

모든 인스턴스를 동시 실행하고 결과를 하나의 JSON으로 합쳐 출력합니다. v0.9 신규 필드: `scenario_run_id` (top-level), `instances[].ipc_messages`, `instances[].ipc_summary`. v0.9.1부터 시나리오 JSON은 strict validation을 사용하므로 알 수 없는 필드는 조용히 무시되지 않고 오류가 됩니다. v0.10부터 인스턴스별 `backend` 필드가 추가됩니다(시나리오는 항상 cold이므로 `"shadow"` 또는 `"process"`).

```json
{
  "schema_version": "1",
  "scenario_run_id": "20260424-130000-a3f8b2c1",
  "exit_code": 0,
  "instances": [
    {
      "role": "host",
      "run_id": "20260424-130000-h1234567",
      "exit_code": 0,
      "backend": "shadow",
      "total": 5, "passed": 5, "failed": 0, "skipped": 0,
      "tests": [],
      "errors": [],
      "new_failures": null,
      "ipc_messages": [
        {"seq": 1, "ts": "2026-04-24T13:00:05Z", "from": "host", "to": "*", "kind": "ready", "payload": {"port": 7777}},
        {"seq": 3, "ts": "2026-04-24T13:00:08Z", "from": "client", "to": "host", "kind": "connected"}
      ],
      "ipc_summary": {
        "sent_count": 1,
        "received_count": 1,
        "last_sent":     {"seq": 1, "to": "*", "kind": "ready"},
        "last_received": {"seq": 3, "from": "client", "kind": "connected"}
      }
    },
    {
      "role": "client",
      "run_id": "20260424-130000-c7654321",
      "exit_code": 0,
      "backend": "shadow",
      "total": 3, "passed": 3, "failed": 0, "skipped": 0,
      "tests": [],
      "errors": [],
      "new_failures": null,
      "ipc_messages": [...],
      "ipc_summary": {...}
    }
  ]
}
```

최소 시나리오 파일:

```json
{
  "schema_version": "1",
  "instances": [
    {"role": "host", "config": "testplay.json", "ready_phase": "compiling"},
    {"role": "client", "config": "testplay.json", "depends_on": "host", "ready_timeout_ms": 120000}
  ]
}
```

`config` 경로는 절대 경로가 아니면 시나리오 파일 위치 기준으로 해석됩니다. `depends_on_phase`는 대기 중인 인스턴스가 의존 대상에게 요구하는 phase입니다. 생략하면 의존 대상의 `ready_phase`, 그마저 없으면 기본값 `"compiling"`을 사용합니다. 유효한 phase는 `"compiling"`, `"running"`, `"done"`입니다. `"running"` 대기는 의존 대상 config의 two-phase 실행(`compile_ms` + `test_ms`)이 켜져 있어야 합니다 — single-phase 실행은 이 phase를 절대 emit하지 않으므로, ready timeout을 태우는 대신 시나리오 로드 시점에 거부됩니다(exit 5). 각 인스턴스에 `"compare_run"`(해당 role 자신의 이전 run_id)을 지정하면 인스턴스별 `new_failures` 회귀 비교가 수행됩니다.

의존성 오케스트레이션이 실패하면 top-level `orchestrator_errors` 배열이 추가됩니다. v0.9부터 각 항목은 대기 인스턴스가 실패한 의존성으로부터 마지막으로 본 IPC 메시지로 보강됩니다:

```json
{
  "schema_version": "1",
  "scenario_run_id": "20260424-130000-a3f8b2c1",
  "exit_code": 4,
  "instances": [...],
  "orchestrator_errors": [
    "instance \"client\": dependency \"host\" exited with exit 2 (compile error) before reaching phase \"compiling\". \"client\" last received from \"host\": seq=1 kind=\"boot\""
  ]
}
```

---

### `testplay result`

저장된 실행 이력을 조회합니다. Unity를 재실행하지 않습니다.

```bash
testplay result
testplay result --last 3
```

```json
{
  "schema_version": "1",
  "runs": [
    {"run_id": "20250325-143000-a3f8b2c1", "exit_code": 0, "total": 10, "passed": 10, "failed": 0},
    {"run_id": "20250324-091500-b7d2e4f0", "exit_code": 3, "total": 10, "passed": 9, "failed": 1}
  ]
}
```

## 웜 에디터 브릿지

에디터가 열려 있으면 아래의 cold 경로는 프로젝트를 섀도우 워크스페이스로 복사하고 배치 모드를 cold-start 해야 합니다 — 복사(디스크)와 도메인 리로드+재임포트(시간) 비용을 모두 지불합니다. **웜 에디터 브릿지**는 둘 다 제거합니다: opt-in C# 에디터 패키지(`unity/com.testplay.bridge`)가 *이미 열린 에디터에서* `TestRunnerApi`로 EditMode 테스트를 실행하고 cold 실행과 동일한 NUnit `results.xml`을 작성합니다. **투명한 backend**(동일한 exit code, 동일한 JSON)이며, 어떤 엔진이 실행됐는지 `backend` 필드로 공개합니다.

**3-tier 자동 선택** (기본값은 정확성 우선):

```
1. bridge   — 살아있고 호환되는 idle 브릿지가 있고 Pristine Gate를 통과할 때
2. shadow   — 아니면 에디터가 프로젝트 락(Temp/UnityLockfile)을 보유할 때
3. process  — 아니면 실제 프로젝트에 대해 새 배치 모드 프로세스
```

실행이 시작되기 전에 브릿지 적격성 검사가 실패하면 자동으로 cold 경로로 폴백합니다. 브릿지 실행이 시작됐을 가능성이 생긴 뒤 소유권이나 완료를 증명할 수 없으면 exit 9로 끝내고 cold로 재실행하지 않습니다. `--no-bridge`(또는 `"bridge": { "enabled": false }`)는 완전히 금지하고, `--bridge`는 선호하되 Pristine Gate는 여전히 존중합니다. `--shadow`/`--reset-shadow`/`--clear-cache`, two-phase 설정(`compile_ms`+`test_ms`), 시나리오 모드는 모두 cold로 실행됩니다.

**설치 + opt-in.** in-repo UPM 패키지 `unity/com.testplay.bridge`를 프로젝트 `Packages/manifest.json`에 추가한 뒤 opt-in 합니다(그 전에는 휴면 상태이며 배치 모드에서는 절대 실행되지 않음):

- 에디터 실행 시 `TESTPLAY_BRIDGE_ENABLE=1` 환경변수, **또는**
- 빈 `<project>/.testplay/bridge/ENABLE` sentinel 파일.

**v0.11 업그레이드:** CLI와 `com.testplay.bridge` 패키지를 반드시 함께
업데이트하세요. Protocol 2는 요청을 하나의 에디터 세션과 소유한 Test
Framework run에 결속하며, v0.10의 protocol 1 패키지와 의도적으로 호환되지
않습니다. 버전이 맞지 않으면 추정하지 않고 거부하며 cold 폴백을 사용할 수
있습니다.

**Pristine Gate (정확성).** warm 결과는 warm 도메인이 테스트 대상 코드에 대해 fresh cold 도메인과 동등할 때만 반환됩니다. 브릿지는 Play Mode이거나 PlayMode 요청이면 거부(→ cold)하고, 컴파일/임포트가 안정될 때까지 대기한 뒤(컴파일 에러는 cold와 동일하게 `exit 2` + `errors[]`로 보고), 결과를 바꾸지 않는 상태(예: 미저장 씬)는 `warnings`로 공개합니다 — 에디터를 자동 저장하지 않습니다.

**런타임 파일**은 `<project>/.testplay/bridge/` 아래에 위치합니다:
`handshake.json`(세션에 결속된 liveness 하트비트), `requests/`, `responses/`,
영속 request tombstone, `runs/<run_id>/{status.ndjson, compile-errors.json}`.
브릿지는 `results.xml`을 기존 `.testplay/runs/<run_id>/`에 작성하므로 기존
파싱 파이프라인이 그대로 재사용됩니다.

**범위 (v0.11.0):** protocol 2 warm 브릿지는 Unity 6(6000.3+)의 EditMode
전용입니다. PlayMode-warm과 시나리오/네트워크 warm 오케스트레이션은
deferred이며 당분간 cold로 실행됩니다. exit code는 0–10이고 6-command
인터페이스는 변경 없습니다. 브릿지 run이 시작됐을 수 있지만 완료를
증명할 수 없으면 exit 9로 끝내며 cold로 자동 재실행하지 않습니다.
[`unity/com.testplay.bridge/README.md`](unity/com.testplay.bridge/README.md) 참조.

## 섀도우 워크스페이스

Unity 에디터가 프로젝트를 열고 있으면 `Temp/UnityLockfile`이 존재하며, Unity 배치 모드가 동일한 프로젝트 디렉터리에서 실행될 수 없습니다. 사용 가능한 웜 브릿지가 없으면 `testplay run`은 이를 자동으로 감지하고 프로젝트 루트 내 `.testplay-shadow-<run_id>/`에 per-run 섀도우 워크스페이스를 생성합니다:

| 디렉터리 | 전략 |
|---|---|
| `Assets/` | 매 실행마다 새로 복사 |
| `ProjectSettings/` | 매 실행마다 새로 복사 |
| `Packages/` | 심링크(Windows는 Junction) |
| `Library/` | `.testplay/cache/Library/`에서 seed; 캐시 없으면 cold-start |
| `Temp/` | 매 실행 전 삭제; Unity가 새로 생성 |

각 실행은 고유한 격리된 섀도우 디렉터리를 사용하므로 병렬 `testplay run` 호출이 안전합니다. 실행 종료 후 `ws.Cleanup()`으로 자동 삭제됩니다.

**Library 웜 캐시:** 첫 번째 성공적인 실행이 `.testplay/cache/Library/`를 생성합니다. 이후 섀도우 실행은 이 캐시에서 `Library/`를 seed하여 cold-start 재임포트를 방지합니다. `ProjectVersion.txt` 또는 `Packages/manifest.json`이 변경되면 캐시가 무효화됩니다. `--clear-cache`로 강제 cold-start가 가능합니다.

**섀도우 모드는 에이전트에게 투명합니다.** JSON 출력의 모든 `absolute_path` 필드는 원본 프로젝트 경로로 재매핑됩니다 — 에이전트는 섀도우 경로를 볼 수 없습니다.

**플래그:**
- `--shadow` — 에디터가 열려 있지 않아도 강제로 섀도우 워크스페이스를 사용 (섀도우 동작 테스트에 유용)
- `--reset-shadow` — `--shadow`와 동일 (per-run 격리로 매 실행이 이미 새로 시작됨; API 호환성을 위해 유지)
- `--clear-cache` — `.testplay/cache/` 제거 후 섀도우 워크스페이스 생성, Unity 강제 재임포트
- `--workspace-backend=legacy|image|vhdx-diff|auto` — 기존 backend 또는 opt-in Windows differencing VHDX provider 선택
- `--workspace-store-root=<absolute-path>` — 설치된 broker store를 선택(`vhdx-diff`에서는 등록 경로와 정확히 일치해야 함)
- `--keep-workspace` — 디버깅을 위해 per-run Shadow 디렉터리를 보존

Image backend는 실험 기능이며 기본으로 선택되지 않습니다. 명시적
`image` 실패는 오류로 보고되고 Legacy로 조용히 폴백하지 않습니다.
[기술 검증 보고서](docs/library-image-spike.md)와
[벤치마크](docs/benchmarks/library-image-baseline.md)를 참고하십시오. 현재
판정은 `PROMISING`입니다.

### Differencing VHDX workspace provider (실험적)

Windows 11에서 compatibility key마다 immutable NTFS parent VHDX 하나를
보관하고, 실행마다 writable differencing child를 격리 workspace의
`Library`에 mount합니다. 관리자는 broker를 한 번만 설치하고 이후 사용자와
AI agent는 비관리자 권한으로 실행합니다.

```powershell
testplay storage install
testplay storage status --json
testplay run --workspace-backend vhdx-diff
```

`auto`의 legacy fallback은 broker hello/capacity admission 이전에만 허용되며
parent/child 작업이 시작된 뒤에는 fallback하지 않습니다. 기본 quota는 실제
allocated bytes 32 GiB, host-free floor는 20 GiB, 신규 child reserve는 2 GiB입니다.
fixture와 GNF_ 1/2/4 worker, 강제 종료, broker restart, Windows reboot,
quota/LRU 및 retained workspace native gate가 통과했습니다. 그래도 기본
backend로 승격하지 않고 명시적 experimental opt-in으로만 제공합니다.
Managed ReFS 구현과 기존 evidence는 별도 experimental/legacy backend로
보존됩니다. 자세한 계약은
[provider 문서](docs/differencing-vhdx-workspace-provider.md)를 참고하십시오.

설치, AI agent 사용, retained workspace, rollback, 검증 절차는
[Differencing VHDX quickstart](docs/vhdx-diff-quickstart.md)를 따르십시오. RC Windows
바이너리는 Authenticode 서명이 없어 SmartScreen 경고가 나타날 수 있으므로
관리자 설치를 승인하기 전 공개된 SHA-256과 GitHub build-provenance
attestation을 모두 검증하십시오.

v0.12.0 릴리스 아티팩트에는 공개 `testplay run` 백엔드가 아닌 별도
`testplay-storage-helper` 아카이브가 실험적 통합 primitive로 포함됩니다. schema 1 NDJSON
`hello`/`acquire`/`release`/`shutdown` 계약은 그대로 유지하면서 Windows는
Differencing VHDX, macOS는 APFS `clonefileat(2)`, Linux는 필수 reflink
provider를 선택합니다. macOS/Linux는 물리 복사로 조용히 폴백하지
않습니다. Unix Child 삭제 전에는 Lease token, 전용 marker, filesystem
device/inode를 검증하고 덮어쓰기 없는 quarantine rename 뒤 다시
검증합니다. `.testplay-storage-owner` marker는 복제된 Unity Library 내부에
존재합니다. 기존의 빈 Mount 디렉터리는 기록한 permission bit로 새로
생성할 뿐이며, 원래 디렉터리 객체나 모든 metadata를 복원한다고
주장하지 않습니다. [Windows provider](docs/windows-vhdx-storage-helper.md),
[macOS/Linux provider](docs/unix-cow-storage-helper.md),
[v0.12.0 한글 릴리즈 노트](docs/29_v0.12.0_release_notes.ko.md)를 참고하십시오.
기존 schema-1 Helper 계약은 변경 없이 별도로 유지됩니다. 새 versioned
broker protocol은 명시적 `vhdx-diff`/`auto` workspace 선택에만 연결되며,
production 기본 backend는 그대로이고 물리 복사 fallback은 추가하지 않습니다.

**`.gitignore`는 최초 사용 시 자동으로 패치**되어 `.testplay-shadow-*/`가 제외됩니다.

## 시나리오 IPC 버스

`testplay run --scenario`로 여러 인스턴스가 동시 실행될 때, 각 인스턴스는 `TESTPLAY_IPC_BUS` 환경변수를 받습니다 — 값은 공유 NDJSON 파일의 절대 경로입니다. 어떤 언어든 그 파일에 메시지를 append하고 폴링으로 읽을 수 있습니다. `depends_on` ready 신호 외 임의 조정 (예: "client 접속 완료", "server가 플레이어 입장 감지", "host가 데미지 이벤트 수신") 에 사용하세요.

**메시지 형식** (한 줄당 JSON 하나):

```json
{"seq": 1, "ts": "2026-04-24T13:00:05Z", "from": "host", "to": "*", "kind": "ready", "payload": {"port": 7777}}
```

- `from` — 자기 역할
- `to` — 수신 역할, 또는 브로드캐스트는 `"*"`
- `kind` — 애플리케이션 정의 이벤트 이름
- `payload` — 선택; 메시지는 짧게 (atomic-append 보장은 ~4 KB 이하)
- `seq` — 자기 단조 카운터; (from, seq) 쌍으로 유일성 확보

**C# 최소 예제 (host 측):**

```csharp
var bus = System.Environment.GetEnvironmentVariable("TESTPLAY_IPC_BUS");
if (!string.IsNullOrEmpty(bus)) {
    var line = "{\"seq\":1,\"ts\":\"" + DateTime.UtcNow.ToString("o") + "\",\"from\":\"host\",\"to\":\"*\",\"kind\":\"ready\"}";
    System.IO.File.AppendAllText(bus, line + "\n");
}
```

**testplay가 자동으로 해주는 것:**

- 인스턴스별 폴링 reader가 자기 앞으로 온 메시지(브로드캐스트 포함) 수집
- 시나리오 출력에 `instances[].ipc_messages` (전체 리스트) + `instances[].ipc_summary` (카운트 + last_sent / last_received) 노출
- 각 인스턴스의 `events.ndjson`에 `ipc_send` / `ipc_recv`가 Unity 페이즈 이벤트와 섞여 단일 forensic 타임라인 형성
- 의존성이 ready 도달 전에 종료되면 `orchestrator_errors`에 마지막으로 본 메시지 정보 추가
- 버스 디렉토리(`.testplay/ipc/<scenario_run_id>/`)는 `retention.max_runs` 정책 자동 적용; `.gitignore` 자동 패치

**v0.9 비목표:** 실시간 push(SSE/websocket), 양방향 RPC, 프레임워크별 헬퍼(NGO/Mirror — v1.0 예정), single-mode(`--scenario` 없는 `testplay run`)에서의 IPC.

## 종료코드

| 코드 | 의미 | 에이전트 조치 |
|---|---|---|
| 0 | 모든 테스트 통과 | 진행 |
| 1 | Unity / 프로젝트 경로 없음 | 환경 수정, `hint` 필드 참조 |
| 2 | 컴파일 실패 | 소스 수정, `errors[].absolute_path` + `line` 참조 |
| 3 | 테스트 실패 | 테스트 수정, `tests[].absolute_path` + `line` 참조 |
| 4 | 타임아웃 | JSON 결과의 `timeout_type` 확인 — 아래 표 참조 |
| 5 | 설정/사용법 오류 | `testplay.json` 수정(알 수 없는/오타 키는 거부됨) 또는 CLI 호출 수정(알 수 없는 플래그·커맨드, 불필요한 인자) |
| 6 | 빌드 / Unity invocation 실패 | Unity 라이선스, 빌드 모듈, 에디터 로그, 패키지 import, shadow cold import 확인 |
| 7 | 권한 오류 (섀도우 워크스페이스) | 프로젝트 디렉토리 권한 수정 |
| 8 | 시그널 중단 | SIGINT/SIGTERM 수신 — 코드 변경 없이 재시도 |
| 9 | 러너 시스템 오류 | 결과/아티팩트 저장 실패 — 디스크 용량/권한 확인, `warnings` 필드 참조 |
| 10 | `--filter`/`--category`에 매칭된 테스트 없음 | 아무것도 실행되지 않음 — 필터를 고치거나 `testplay list`로 후보를 갱신 |

### Exit 4 — timeout_type 값

| `timeout_type` | status의 `phase` | 원인 |
|---|---|---|
| `"compile"` | `timeout_compile` | 컴파일 단계가 `compile_ms` 데드라인 초과 |
| `"test"` | `timeout_test` | 테스트 단계가 `test_ms` 데드라인 초과 |
| `"total"` | `timeout_total` | 외부 `total_ms` 데드라인 만료 (어느 단계에서든 발생) |

컴파일 단계 타임아웃 JSON 예시:

```json
{
  "schema_version": "1",
  "exit_code": 4,
  "backend": "process",
  "timeout_type": "compile",
  "tests": [],
  "errors": []
}
```

two-phase 모드의 compile invocation에서 exit 6이 반환되고 `errors[].message`에
C# compile error가 없다고 표시되면, 이를 소스 컴파일 실패로 보지 마세요.
shadow 모드에서는 cold workspace import 또는 패키지/asmdef refresh 중 테스트 phase에
도달하기 전에 이런 결과가 날 수 있습니다. `compile_ms`를 늘리거나 shadow cache를
워밍하고, 필요하면 non-shadow 실행으로 환경 비용과 실제 테스트 실패를 분리하세요.

## 진행 상황 모니터링

**폴링이 유일한 수단입니다.** 푸시 알림, 웹훅, SSE 엔드포인트는 없습니다. 에이전트는 일정 간격으로 `testplay-status.json`을 읽어야 합니다. `seq` 필드(매 Write마다 증가)로 파일이 마지막 읽기 이후 변경됐는지 감지할 수 있습니다 — `updated_at` 파싱 없이도 변경 여부를 판단 가능합니다.

`testplay run` 실행 중 `testplay-status.json`을 폴링하면 진행 상황을 확인할 수 있습니다:

```json
{
  "schema_version": "1",
  "seq": 7,
  "phase": "running",
  "run_id": "20250325-143000-a3f8b2c1",
  "total": 10,
  "passed": 3,
  "failed": 0,
  "updated_at": "2025-03-25T14:30:05Z",
  "started_at": "2025-03-25T14:29:58Z",
  "last_heartbeat_at": "2025-03-25T14:30:03Z",
  "artifact_root": "/Users/user/MyProject/.testplay/runs/20250325-143000-a3f8b2c1",
  "pid": 12345
}
```

페이즈 진행 (single-phase): `compiling → done`
페이즈 진행 (two-phase): `compiling → running → done`
실패 페이즈: `timeout_compile`, `timeout_test`, `timeout_total`, `interrupted`

## 권장 에이전트 흐름

```
0. testplay init             # testplay.json 생성 (최초 1회)
1. testplay check            # 환경 검증
2. testplay list             # 테스트 이름 탐색
3. testplay run              # 실행 (testplay-status.json 폴링으로 진행 추적)
4. testplay result --last 3  # 실행 이력 검토
```

## 개발

```bash
# 레이스 감지 포함 전체 테스트
go test -race ./...

# 통합 테스트
go test -tags=integration ./cmd/testplay/...

# 현재 플랫폼 빌드
go build ./cmd/testplay
```

## Unity Smoke 검증

`fixtures/smoke-project/`에 실제 Unity 설치 환경에서 `testplay run`의 end-to-end 동작을 검증하는 최소 Unity 프로젝트가 포함되어 있습니다. EditMode 테스트 1개와 PlayMode(`[UnityTest]`) 테스트 1개로 구성됩니다.

**로컬 실행:**

```bash
# 사전 조건: Unity 설치, UNITY_PATH 설정
export UNITY_PATH=/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity
./scripts/smoke.sh
```

스크립트 동작:
1. EditMode → PlayMode 순으로 각 플랫폼에 맞는 `testplay.json`을 생성
2. `testplay check` + `testplay run` 실행
3. 각 run의 아티팩트 디렉터리(`.testplay/runs/<run_id>/`)에 아래 6개 파일이 모두 존재하는지 확인:
   - `results.xml`, `summary.json`, `manifest.json`, `stdout.log`, `stderr.log`, `events.ndjson`
4. 프로젝트 루트의 `testplay-status.json`(run 디렉터리 바깥의 스냅샷) 존재 확인
5. `--shadow` 플래그를 사용한 섀도우 모드 스모크 단계 실행 — 섀도우 워크스페이스 생성 및 예상 서브디렉터리 확인

**CI (opt-in):**

```bash
gh workflow run smoke.yml
```

`.github/workflows/smoke.yml` 참조. Unity가 설치된 self-hosted runner와 `UNITY_PATH` 환경변수가 필요합니다.

실제 프로젝트에 재사용할 수 있는 패턴은
[`docs/05_v0.2.0_playmode_smoke_example.md`](docs/05_v0.2.0_playmode_smoke_example.md)를 참고하세요.
fixture를 코드로 생성하는 scene-free PlayMode smoke 테스트를 `testplay run`
기준으로 정리해뒀습니다.

## Known Limitations

현재 존재하는 한계를 솔직하게 기록합니다. 각 항목에는 개선 방향이 있습니다.

**`testplay list` 정적 스캔은 불완전할 수 있습니다.**
정적 스캐너는 `[Test]`, `[UnityTest]`, `[TestCase]`, `[TestCaseSource]`, `[Theory]`만 감지합니다. 커스텀 어트리뷰트나 추상 기반 클래스 패턴을 사용하는 테스트는 보이지 않습니다. 출력 JSON에 `complete`와 `source` 필드가 포함되어 있어 에이전트가 목록의 완전성 여부를 알 수 있습니다. 첫 번째 `testplay run`이 완료(exit 0 또는 3)된 후에는 `.testplay/cache/list.json`에 실행 캐시가 작성되며, 이후 `testplay list` 호출은 `complete: true, source: "run_cache"`로 실제 전체 목록을 반환합니다.

**진행 상황 모니터링은 폴링만 가능합니다.**
`testplay-status.json`이 실행 중 상태를 확인하는 유일한 채널입니다. SSE, 웹소켓, 네임드 파이프 없음. `seq` 필드(매 Write마다 증가)로 마지막 읽기 이후 파일이 변경됐는지 감지할 수 있습니다. 개선 방향: PlayMode 네트워크 테스트 도입 시 선택적 SSE 엔드포인트.

## 라이선스

Apache 2.0 — [LICENSE](LICENSE) 참조.
서드파티 고지 — [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES) 참조.
