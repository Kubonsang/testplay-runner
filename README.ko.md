# testplay-runner

**AI 에이전트를 위한 신뢰할 수 있는 Unity 테스트 실행기**

한국어 | [English](README.md)

---

Unity의 원시 CLI는 자동화에 적합하지 않습니다. 컴파일 실패에도 종료코드 0을 반환하고, 결과는 XML로만 출력되며, 진행 상황을 알 수 없고, 오류 유형이 모호합니다. `fastplay`는 AI 에이전트와 CI 파이프라인을 위해 설계된 4개의 명령으로 이 모든 문제를 해결합니다.

## 해결하는 문제

| 문제 | 해결책 |
|---|---|
| 컴파일 실패에도 종료코드 0 반환 | 컴파일 오류는 exit 2, 테스트 실패는 exit 3으로 명확히 구분 |
| XML 전용 출력 | 모든 stdout을 `schema_version` 포함 JSON으로 출력 |
| 실행 전 검증 없음 | `fastplay check`로 Unity 실행 전 환경 사전 검증 |
| 진행 상황 불투명 | 실행 중 `fastplay-status.json`을 원자적으로 업데이트 |
| 타임아웃 유형 모호 | JSON에 `timeout_type: compile / test / total` 명시 |
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
    "total_ms": 300000
  },
  "result_dir": ".fastplay/results"
}
```

`unity_path`를 생략하면 `UNITY_PATH` 환경변수로 폴백합니다.
`project_path`를 생략하면 `fastplay.json`이 위치한 디렉터리가 기본값이 됩니다.
`test_platform`은 `"edit_mode"` (기본값) 또는 `"play_mode"`를 허용합니다. Unity CLI에 `-testPlatform EditMode|PlayMode`로 전달됩니다.

> **참고:** `compile_ms`와 `test_ms`는 설정 파일에서 허용되지만 현재 런타임에서 아무런 효과가 없습니다 (향후 페이즈 인식 구현을 위해 예약된 필드). PlayMode 네트워크 하네스와 NGO 오케스트레이션은 아직 미지원입니다.

## 명령어

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
  "total": 10,
  "passed": 10,
  "failed": 0,
  "tests": [],
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
  "errors": [
    {
      "file": "Assets/Scripts/Player.cs",
      "absolute_path": "/path/to/UnityProject/Assets/Scripts/Player.cs",
      "line": 17,
      "message": "CS0103: The name 'speed' does not exist in the current context"
    }
  ]
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
| 4 | 타임아웃 또는 시그널 중단 | `timeout_type: "total"` 확인; 시그널 중단 시 `fastplay-status.json` phase가 `interrupted`로 표시되며 동일하게 exit 4 반환 |
| 5 | 설정 오류 | `fastplay.json` 수정 또는 생성 |
| 6 | 빌드 실패 (미구현) | Unity 라이선스 / 빌드 타겟 확인 |
| 7 | 권한 오류 (미구현) | 경로 권한 수정 |

## 진행 상황 모니터링

`fastplay run` 실행 중 `fastplay-status.json`을 폴링하면 실시간 진행 상황을 확인할 수 있습니다:

```json
{
  "schema_version": "1",
  "phase": "running",
  "run_id": "20250325-143000",
  "current_test": "MyTests.PlayerTests.TestJump",
  "total": 10,
  "passed": 3,
  "failed": 0,
  "updated_at": "2025-03-25T14:30:05Z"
}
```

페이즈 진행: `waiting → compiling → running → done`
실패 페이즈: `timeout_total`, `interrupted`

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

## 라이선스

Apache 2.0 — [LICENSE](LICENSE) 참조.
서드파티 고지 — [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES) 참조.
