# testplay-runner

**AI 에이전트를 위한 신뢰할 수 있는 Unity 테스트 실행기**

한국어 | [English](README.md)

---

Unity의 원시 CLI는 자동화에 적합하지 않습니다. 컴파일 실패에도 종료코드 0을 반환하고, 결과는 XML로만 출력되며, 진행 상황을 알 수 없고, 오류 유형이 모호합니다. `fastplay`는 AI 에이전트와 CI 파이프라인을 위해 설계된 5개의 명령으로 이 모든 문제를 해결합니다.

## 해결하는 문제

| 문제 | 해결책 |
|---|---|
| 컴파일 실패에도 종료코드 0 반환 | 컴파일 오류는 exit 2, 테스트 실패는 exit 3으로 명확히 구분 |
| XML 전용 출력 | 모든 stdout을 `schema_version` 포함 JSON으로 출력 |
| 실행 전 검증 없음 | `fastplay check`로 Unity 실행 전 환경 사전 검증 |
| 진행 상황 불투명 | 실행 중 `fastplay-status.json`을 원자적으로 업데이트 |
| 타임아웃 유형 모호 | JSON에 `timeout_type: compile / test / total` 명시; `compile_ms` + `test_ms` 설정 시 two-phase 실행으로 컴파일/테스트 타임아웃 분리 |
| 회귀 추적 불가 | `--compare-run`으로 `new_failures` 비교 |
| 플랫폼별 경로 차이 | 모든 응답에 절대경로 + 상대경로 동시 제공 |
| 실행 없이 테스트 탐색 불가 | `fastplay list`로 `[Test]`, `[UnityTest]` 어트리뷰트 정적 스캔 |

## 설치

```bash
git clone https://github.com/Kubonsang/testplay-runner.git
cd testplay-runner
go build -o fastplay ./cmd/fastplay
```

크로스 컴파일:

```bash
GOOS=windows GOARCH=amd64 go build -o fastplay.exe ./cmd/fastplay
```

## 설정

프로젝트 루트에 `fastplay.json`을 생성합니다:

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
  "result_dir": ".fastplay/results"
}
```

`unity_path`를 생략하면 `UNITY_PATH` 환경변수로 폴백합니다.
`project_path`를 생략하면 `fastplay.json`이 위치한 디렉터리가 기본값이 됩니다.
`test_platform`은 `"edit_mode"` (기본값) 또는 `"play_mode"`를 허용합니다. Unity CLI에 `-testPlatform EditMode|PlayMode`로 전달됩니다.
`result_dir`는 `fastplay result`가 읽는 실행 이력 JSON 저장 위치를 제어합니다.
반면 run별 아티팩트(`results.xml`, `summary.json`, `manifest.json`, `stdout.log`,
`stderr.log`, `events.ndjson`)는 항상
`<project_path>/.fastplay/runs/<run_id>/` 아래에 저장됩니다.

**타임아웃 설정:**
- `total_ms` (기본값 300000): 전체 실행의 외부 안전망 데드라인.
- `compile_ms` + `test_ms`: **반드시 둘 다 함께 설정해야** two-phase 실행이 활성화됨 — Unity가 컴파일만 먼저 실행(`compile_ms` 데드라인), 이후 테스트 실행(`test_ms` 데드라인). 단계별 타임아웃이면 `timeout_type: "compile"` 또는 `"test"`가 나오고, 바깥 `total_ms`가 먼저 만료되면 `"total"`이 나올 수 있습니다. 하나만 설정하면 validation error.
- 둘 다 설정하지 않으면 single-phase 실행 (컴파일+테스트를 Unity 한 번 호출로 처리, `total_ms` 기준).

> **참고:** PlayMode 네트워크 하네스와 NGO 오케스트레이션은 아직 미지원입니다.

## 명령어

### `fastplay version`

현재 fastplay 버전을 JSON으로 출력합니다.

```bash
fastplay version
```

```json
{
  "schema_version": "1",
  "version": "v0.1.0-beta"
}
```

---

### `fastplay check`

Unity 경로, 프로젝트 경로, 설정 파일을 사전 검증합니다. 가장 먼저 실행하세요.

```bash
fastplay check
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
  "hint": "set UNITY_PATH or add unity_path to fastplay.json"
}
```

종료코드 0 = 준비됨. 종료코드 1 = 의존성 누락 (`hint` 필드 참조). 종료코드 5 = 설정 파일 오류.

---

### `fastplay list`

Unity를 실행하지 않고 `*.cs` 파일에서 `[Test]`, `[UnityTest]` 어트리뷰트를 정적으로 스캔합니다. 목록이 불완전할 수 있습니다 (`[TestCase]`, `[Theory]` 등은 미탐지).

```bash
fastplay list
```

```json
{
  "schema_version": "1",
  "tests": ["MyTests.PlayerTests.TestJump", "MyTests.PlayerTests.TestRun"]
}
```

---

### `fastplay run`

설정된 `test_platform` (`edit_mode` 또는 `play_mode`)으로 Unity 테스트를 실행합니다. 진행 상황은 `fastplay-status.json`에 스트리밍됩니다.

```bash
fastplay run
fastplay run --filter TestJump
fastplay run --category Smoke
fastplay run --compare-run 20250301-102200
```

**전체 통과 (exit 0):**

```json
{
  "schema_version": "1",
  "run_id": "20250325-143000",
  "exit_code": 0,
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
  "run_id": "20250325-143000",
  "total": 10,
  "passed": 9,
  "failed": 1,
  "skipped": 0,
  "tests": [
    {
      "name": "MyTests.PlayerTests.TestJump",
      "result": "Failed",
      "message": "Expected 1 but was 0",
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
  "run_id": "20250325-143000",
  "exit_code": 2,
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

---

### `fastplay result`

저장된 실행 이력을 조회합니다. Unity를 재실행하지 않습니다.

```bash
fastplay result
fastplay result --last 3
```

```json
{
  "schema_version": "1",
  "runs": [
    {"run_id": "20250325-143000", "exit_code": 0, "total": 10, "passed": 10, "failed": 0},
    {"run_id": "20250324-091500", "exit_code": 3, "total": 10, "passed": 9, "failed": 1}
  ]
}
```

## 종료코드

| 코드 | 의미 | 에이전트 조치 |
|---|---|---|
| 0 | 모든 테스트 통과 | 진행 |
| 1 | Unity / 프로젝트 경로 없음 | 환경 수정, `hint` 필드 참조 |
| 2 | 컴파일 실패 | 소스 수정, `errors[].absolute_path` + `line` 참조 |
| 3 | 테스트 실패 | 테스트 수정, `tests[].absolute_path` + `line` 참조 |
| 4 | 타임아웃 또는 시그널 중단 | JSON 결과의 `timeout_type` 확인 — 아래 표 참조 |
| 5 | 설정 오류 | `fastplay.json` 수정 또는 생성 |
| 6 | 빌드 실패 (미구현) | Unity 라이선스 / 빌드 타겟 확인 |
| 7 | 권한 오류 (미구현) | 경로 권한 수정 |

### Exit 4 — timeout_type 값

| `timeout_type` | status의 `phase` | 원인 |
|---|---|---|
| `"compile"` | `timeout_compile` | 컴파일 단계가 `compile_ms` 데드라인 초과 |
| `"test"` | `timeout_test` | 테스트 단계가 `test_ms` 데드라인 초과 |
| `"total"` | `timeout_total` | 외부 `total_ms` 데드라인 만료 (어느 단계에서든 발생) |
| *(없음)* | `interrupted` | SIGINT / SIGTERM — 코드 변경 없이 재시도 |

컴파일 단계 타임아웃 JSON 예시:

```json
{
  "schema_version": "1",
  "exit_code": 4,
  "timeout_type": "compile",
  "tests": [],
  "errors": []
}
```

## 진행 상황 모니터링

`fastplay run` 실행 중 `fastplay-status.json`을 폴링하면 실시간 진행 상황을 확인할 수 있습니다:

```json
{
  "schema_version": "1",
  "phase": "running",
  "run_id": "20250325-143000",
  "total": 10,
  "passed": 3,
  "failed": 0,
  "updated_at": "2025-03-25T14:30:05Z",
  "started_at": "2025-03-25T14:29:58Z",
  "last_heartbeat_at": "2025-03-25T14:30:03Z",
  "artifact_root": "/Users/user/MyProject/.fastplay/runs/20250325-143000",
  "pid": 12345
}
```

페이즈 진행: `compiling → running → done`
실패 페이즈: `timeout_compile`, `timeout_test`, `timeout_total`, `interrupted`

## 권장 에이전트 흐름

```
1. fastplay check            # 환경 검증
2. fastplay list             # 테스트 이름 탐색
3. fastplay run              # 실행 (fastplay-status.json 폴링으로 진행 추적)
4. fastplay result --last 3  # 실행 이력 검토
```

## 개발

```bash
# 레이스 감지 포함 전체 테스트
go test -race ./...

# 통합 테스트
go test -tags=integration ./cmd/fastplay/...

# 현재 플랫폼 빌드
go build ./cmd/fastplay
```

## Unity Smoke 검증

`fixtures/smoke-project/`에 실제 Unity 설치 환경에서 `fastplay run`의 end-to-end 동작을 검증하는 최소 Unity 프로젝트가 포함되어 있습니다. EditMode 테스트 1개와 PlayMode(`[UnityTest]`) 테스트 1개로 구성됩니다.

**로컬 실행:**

```bash
# 사전 조건: Unity 설치, UNITY_PATH 설정
export UNITY_PATH=/Applications/Unity/Hub/Editor/2022.3.0f1/Unity.app/Contents/MacOS/Unity
./scripts/smoke.sh
```

스크립트 동작:
1. EditMode → PlayMode 순으로 각 플랫폼에 맞는 `fastplay.json`을 생성
2. `fastplay check` + `fastplay run` 실행
3. 각 run의 아티팩트 디렉터리(`.fastplay/runs/<run_id>/`)에 아래 6개 파일이 모두 존재하는지 확인:
   - `results.xml`, `summary.json`, `manifest.json`, `stdout.log`, `stderr.log`, `events.ndjson`
4. 프로젝트 루트의 `fastplay-status.json`(run 디렉터리 바깥의 스냅샷) 존재 확인

**CI (opt-in):**

```bash
gh workflow run smoke.yml
```

`.github/workflows/smoke.yml` 참조. Unity가 설치된 self-hosted runner와 `UNITY_PATH` 환경변수가 필요합니다.

실제 프로젝트에 재사용할 수 있는 패턴은
[`docs/playmode-smoke-example.md`](docs/playmode-smoke-example.md)를 참고하세요.
fixture를 코드로 생성하는 scene-free PlayMode smoke 테스트를 `fastplay run`
기준으로 정리해뒀습니다.

## 라이선스

Apache 2.0 — [LICENSE](LICENSE) 참조.
서드파티 고지 — [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES) 참조.
