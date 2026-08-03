# 26. 완성도 & GitHub 스타 성장 브레인스토밍

> 2026-07-08 세션에서 정리한 기능 추가/보완 아이디어 전체 목록.
> 이 문서는 **브레인스토밍 기록**이며 확정 로드맵이 아니다. 각 항목은 착수 전 별도 스펙/설계를 거친다.

- **Date:** 2026-07-08
- **기준 상태:** `fix/v0.11-hardening` 브랜치 — 2026-07 pre-v1.0 감사(132건)의 strict blocker 대부분 커밋 완료 (ctx.Err exit 4/8, strict config decode, phase 검증, per-instance `compare_run`, exit 10, stdout 순수성, bridge criticals)
- **리포:** github.com/Kubonsang/testplay-runner — PUBLIC, 스타 1개
- **관련 문서:** Case Study 0002 (scenario 오케스트레이션 한계), pre-v1.0 감사 리포트 (2026-07-07), docs/25 (bridge 검증)

---

## 0. 핵심 프레임: 완성도와 스타는 다른 레버다

**스타는 마케팅의 결과다.** 세 가지에서 나온다:

1. **발견되는 순간** — 검색 유입, Marketplace/awesome 리스트 등재, 런치 포스트
2. **5분 안의 첫 성공** — 설치 → init → run이 마찰 없이 통과하는 경로
3. **공유하고 싶은 "wow" 하나** — README 최상단에서 30초 안에 전달되는 차별점

**완성도는 그 유입이 이탈하지 않게 하는 바닥이다.** 유입이 아무리 많아도 Case Study 0002 같은 silent failure를 첫 사용에서 만나면 스타 대신 이슈(혹은 조용한 이탈)가 남는다.

**전략적 함의:** 스타는 꾸준히 오지 않고 **순간**에 온다. v1.0 태깅 + 런치 콘텐츠 + 생태계 등재를 한 시점에 겹쳐서 터뜨리는 것이 단일 최대 레버다.

**포지셔닝 진단:** "Unity × AI 에이전트" 교차점은 2026년 현재 좁지만 빠르게 크는 시장이고, 경쟁이 사실상 없다. game-ci가 Unity CI(빌드/실행)를 점유하지만 **"테스트 결과 계약"** 자리는 비어 있다. testplay의 identity anchor(계약 계층)가 곧 포지셔닝 문구다.

---

## A. 생태계 연결 — 스타 견인력 최상

### A-1. MCP 서버 모드 (`testplay mcp`) — **강등 (2026-07-09 재평가)**

- **무엇:** `check` / `list` / `run` / `result`를 MCP(Model Context Protocol) 툴로 노출하는 서브커맨드. Claude Code, Cursor 등 MCP 클라이언트가 셸 우회 없이 직접 호출.
- **강등 사유 (당초 "최상" → "선택적 발견 채널"):**
  1. **bridge와 무관, CLI 계약과 중복.** MCP는 실행 백엔드(bridge)가 아니라 이미 완결된 *에이전트 인터페이스*(stdout JSON + exit code)를 두 번째 표면으로 복제하는 것이다. bridge가 붙었다고 MCP가 필요해지지도, 불필요해지지도 않는다 — 둘은 다른 층위.
  2. **exit code가 어색해진다.** testplay의 왕관 보석인 11개 exit code는 MCP tool result에 Unix exit code 개념이 없어 `exit_code` 필드로 강등된다 → 에이전트가 그 필드로 분기 → 지금 `$?`로 하는 것과 동일. 잘해야 본전.
  3. **CI는 MCP를 못 쓴다.** 타깃 절반(파이프라인)에 무효. 반면 A-2(GitHub Action)는 그 절반을 정확히 커버 → CI-side 레버가 MCP를 대체한다.
  4. **12k 파도를 못 탄다.** unity-mcp(12,198★)는 *에디터 전체 제어* MCP라서 나온 숫자다. "테스트 돌리고 결과 받기"라는 좁은 단일 목적 MCP는 그 궤적을 물려받지 못한다 — 당초 시장 논리의 약한 고리.
  5. **정체성 비용.** "하나의 정직한 계약"이 정체성인데 MCP는 lockstep 유지할 표면을 하나 더 만든다.
- **유일하게 남는 가치:** awesome-mcp-servers 등재라는 *발견 채널* 하나뿐. 그것도 위 4번 때문에 당초 기대보다 약하다.
- **하게 된다면:** 별도 구현 금지 — CLI 위 얇은 어댑터(`runsvc` 직접 호출, 단일 진실은 CLI 스키마)로만. 목적은 순수 발견 채널임을 눈 뜨고 인정하고, v1.0 이후 선택 항목으로.
- **규모:** M / **스타 영향:** 하중(발견 채널 한정) / **완성도 영향:** 마이너스 가능(표면 증가) / **우선순위:** v1.0 이후 선택

### A-2. GitHub Action 공식화 (`testplay-action`) ★ — **승격 (A-1 강등의 반대편, CI-side 최우선 레버)**

- **무엇:** Marketplace에 등재되는 공식 액션. testplay 바이너리 설치 + `run` 실행 + 결과(exit code + JSON)를 GitHub 네이티브 표면으로 번역.
- **왜 (MCP를 대체하는 CI-side 레버):** MCP가 인터랙티브 전용이라 CI 절반에 무효인 반면, Action은 *CI 네이티브*라 그 절반을 정확히 커버한다. 그리고 MCP와 달리 **두 번째 계약을 만들지 않는다** — 바이너리를 그대로 실행하고 그 출력을 번역만 하므로 단일 진실(CLI)이 유지된다. Marketplace 등재는 awesome-mcp보다 넓은 청중(모든 CI 사용자)을 향한 발견 채널.
- **핵심 원리 — exit code는 라우팅, JSON은 수리:**
  - Raw Unity CLI는 컴파일 실패에도 exit 0 → CI 게이트가 무용지물. testplay의 구분되는 exit code가 CI job 실패에 *타입*을 붙인다(빨간 X의 의미가 컴파일이냐 테스트냐 타임아웃이냐). ← 이것이 "exit code만 보면 된다"의 정당한 절반.
  - 하지만 *고치려면* JSON(`errors[].absolute_path:line`, `excerpt`)이 필요하다. Action의 진짜 가치는 이 JSON을 GitHub 표면으로 옮기는 것: `::error file=...,line=...`로 **PR diff 인라인 어노테이션**, job summary 테이블, JSON 아티팩트 업로드.
- **수혜 패턴 — CI를 지켜보는 에이전트:** push → CI가 `testplay run` → exit code로 게이트, 어노테이션/아티팩트로 수리 타깃 확보. autofix 봇/PR 수정 에이전트에게 딱 맞는 신호 구조(exit code = 트리거, JSON = 페이로드).
- **구현 스케치:** composite action (별도 리포 `testplay-action`). 입력: 버전, config 경로, filter, compare-run. 단계: 릴리스 바이너리 다운로드 → run → exit code 분기 → 어노테이션 출력 + job summary + `actions/upload-artifact`로 결과 JSON.
- **긴장/리스크:** Unity 라이선스/설치는 액션 범위 밖 — game-ci 이미지와의 **조합 사용 문서**가 필수 (경쟁이 아니라 보완 포지션). 또한 CLI가 이미 CI에서 `run: testplay run`으로 동작하므로, Action은 필수가 아니라 *유통·가시성 레이어*임을 명확히(v1.0 blocker 아님, 하지만 런치 모먼트에 강하게 짝지음).
- **규모:** M / **스타 영향:** 상 (CI 청중 + Marketplace 발견) / **완성도 영향:** 하 (계약 무손상, 얇은 래퍼)

### A-3. 에이전트 스킬 번들 (`testplay init --agent claude|cursor`)

- **무엇:** `init`에 `--agent` 플래그 추가 → CLAUDE.md 스니펫 / Cursor rules 파일에 6-command 플로우(check→list→run→result, exit code 분기표)를 생성.
- **왜:** "이 파일 하나 넣으면 에이전트가 testplay를 쓸 줄 안다"는 zero-code 온보딩. CLAUDE.md의 "Agent Recommended Usage Flow" 섹션이 이미 완성된 콘텐츠 — 포장만 하면 된다.
- **구현 스케치:** 템플릿 embed(`//go:embed`) + 대상 파일 존재 시 append(마커 주석으로 중복 방지). 실패해도 init 자체는 성공(경고만).
- **규모:** S / **스타 영향:** 중상 / **완성도 영향:** 하

### A-4. JSON Schema 파일 공개 (`schema/*.schema.json`)

- **무엇:** run 출력, scenario 출력, `testplay.json`, scenario JSON의 JSON Schema를 리포에 커밋하고 릴리스에 포함.
- **왜:** 에이전트/툴 제작자가 타입 생성·검증에 사용. "계약 계층" 정체성의 **기계 검증 가능한 물증**. `testplay.json`용 스키마는 에디터 자동완성($schema 참조)이라는 부수 효과도 있음.
- **구현 스케치:** Go struct → 스키마 생성(invopop/jsonschema 등) + CI에서 golden 출력과 스키마 검증 라운드트립 테스트 → 스키마 drift를 CI가 잡음.
- **규모:** S~M / **스타 영향:** 중 / **완성도 영향:** 중상 (계약 회귀 방지 장치 겸용)

### A-5. JUnit XML 내보내기 (`--junit <file>`)

- **무엇:** run 결과를 JUnit XML 파일로 병행 출력하는 플래그. stdout은 여전히 JSON only(파일 출력이므로 계약 무손상).
- **왜:** GitHub/GitLab/Jenkins/Buildkite가 **네이티브로 소화하는 포맷**. NUnit→JUnit 변환은 이미 파싱된 Go struct에서 재직렬화하는 수준. CI 사용자 스펙트럼이 즉시 넓어진다.
- **구현 스케치:** `parser` 패키지에 JUnit 직렬화 추가. testsuite=assembly, testcase=test, failure message=excerpt. 저장 실패는 exit 9 규칙 준수.
- **규모:** S / **스타 영향:** 중상 / **완성도 영향:** 하

### A-6. SARIF 내보내기

- **무엇:** compile error(`errors[]`)를 SARIF로 출력 → GitHub code scanning 탭에 표시.
- **왜:** 니치하지만 "컴파일 에러가 Security 탭처럼 정식 UI에 뜬다"는 차별점. A-2와 조합 시 효과.
- **규모:** S / **스타 영향:** 하중 / **완성도 영향:** 하 / **우선순위 낮음** — A-2의 어노테이션으로 대부분 커버됨

---

## B. 배포/온보딩 마찰 제거 — 스타 전환율

### B-1. Homebrew tap + Scoop/winget

- **무엇:** `brew install kubonsang/tap/testplay`, `scoop install testplay`.
- **왜:** 설치 마찰은 이탈 1순위. GoReleaser가 이미 있으므로 `brews:`/`scoops:` 섹션 추가 몇 줄 + tap 리포 생성.
- **주의:** Release Rules 준수 — tap 갱신도 태그 불변 원칙 위에서.
- **규모:** S / **스타 영향:** 중상

### B-2. Unity Hub 자동 탐지

- **무엇:** `unity_path` 미지정 시 `testplay init`(및 `check`)이 Unity Hub 표준 설치 경로(macOS `/Applications/Unity/Hub/Editor/*`, Windows `C:\Program Files\Unity\Hub\Editor\*`, Linux `~/Unity/Hub/Editor/*`)를 스캔하고, 프로젝트의 `ProjectSettings/ProjectVersion.txt`와 **일치하는 버전**을 자동 선택.
- **왜:** 첫 사용 최대 마찰("Unity 경로를 어디서 찾지?")이 사라진다. 일치 버전이 없으면 발견된 버전 목록을 exit 1 `hint`에 담아 자가 복구 가능하게.
- **구현 스케치:** `internal/unity` 경로 탐색 확장. 우선순위: 명시 config > `UNITY_PATH` env > Hub 스캔(버전 매칭) — 기존 동작은 불변(추가 fallback만).
- **규모:** M / **스타 영향:** 상 / **완성도 영향:** 중

### B-3. `examples/` 샘플 Unity 프로젝트

- **무엇:** 최소 Unity 프로젝트(EditMode 테스트 몇 개 + 의도된 실패 1개 + 컴파일 에러 주석 처리 예제) + testplay.json + 따라하기 문서.
- **왜:** clone → init → run **5분 성공 경로**의 물리적 구현. E2E 테스트 픽스처와 겸용하면 유지비 상쇄.
- **주의:** Unity 버전 고정 문제 — LTS 하나로 고정하고 README에 명시. Library/는 절대 커밋 금지.
- **규모:** M / **스타 영향:** 중상 / **완성도 영향:** 중 (E2E 픽스처 겸용 시)

### B-4. `testplay doctor` (또는 `check --verbose`)

- **무엇:** `check`보다 깊은 진단 — Unity 라이선스 상태, stale `Temp/UnityLockfile` 감지(mtime + PID 검증), 디스크 여유, bridge 핸드셰이크 건강(protocol version/staleness), 캐시 상태.
- **왜:** exit 1/6/7을 **사전에** 예방. 이슈 리포트 품질도 올라감("doctor 출력 첨부해주세요").
- **설계 결정 필요:** 새 커맨드 vs check 확장. 6-command 표면은 계약이므로 **`check --verbose`가 안전** (새 커맨드는 계약 확장 부담).
- **규모:** M / **스타 영향:** 중 / **완성도 영향:** 중상

### B-5. `testplay explain <exit-code>`

- **무엇:** `testplay explain 4` → 해당 exit code의 의미, 봐야 할 필드, 권장 에이전트 액션을 JSON으로 반환.
- **왜:** 에이전트가 문서 없이 자가 복구하는 마이크로 기능. 데모/트윗 소재로도 좋음.
- **긴장:** 커맨드 표면 확장 — exit code 표는 이미 JSON 출력 내 `hint`와 문서에 있으므로 **우선순위 낮음**. 하되 A-3 스킬 번들에 표를 넣는 것으로 대부분 대체 가능.
- **규모:** S / **스타 영향:** 하중

---

## C. README/마케팅 자산 — 스타는 마케팅의 결과

### C-1. 워밍 브리지 데모 GIF ★

- **무엇:** Unity Editor가 열린 화면 옆 터미널에서 `testplay run` → `"backend": "bridge"` JSON이 즉시 나오는 GIF. README 최상단 배치.
- **왜:** **아무도 없는 기능**이다. "에디터가 프로젝트를 잠그고 있어도 테스트가 돈다"는 Unity 자동화의 고전적 통증을 30초에 반전시키는 wow 자산.
- **규모:** S (vhs/asciinema + 화면 녹화) / **스타 영향:** 최상

### C-2. 에이전트 루프 asciinema

- **무엇:** 컴파일 에러 → exit 2 + `errors[].absolute_path:line` → 에이전트가 고침 → exit 0. 프로젝트의 존재 이유를 시연.
- **왜:** "AI 에이전트를 위한"이라는 주장의 시각적 증명. C-1과 함께 README 투톱.
- **규모:** S / **스타 영향:** 상

### C-3. 비교표 페이지 (docs 또는 README 섹션)

- **무엇:** vs Unity raw CLI / game-ci unity-test-runner / 직접 XML 파싱. 각 행: exit code 신뢰성, JSON 출력, 진행 관측, 에디터 열림 대응, 회귀 비교.
- **왜:** "unity test runner exit code", "unity batchmode test results" 같은 **검색 유입 자석**. 정직하게 쓸 것 — game-ci는 빌드/캐싱에서 우월하며 보완 관계임을 명시(신뢰 신호).
- **규모:** S / **스타 영향:** 중상

### C-4. 런치 콘텐츠

- **무엇:** v1.0 시점에 "Unity's test runner lies to your CI"류 기술 블로그 포스트 → HN, r/Unity3D, Unity Discussions, 한국은 유니티 커뮤니티/개발자 커뮤니티에 동시 게시.
- **왜:** 스타의 **순간**을 만드는 방아쇠. 소재는 이미 있다 — 8가지 문제 표, dogfooding 케이스 스터디(0001 성공 + 0002 실패 기록 공개는 드문 진정성 신호), 감사 132건을 고친 여정 자체.
- **주의:** 런치는 B-1(설치), C-1(GIF), B-3(examples)이 준비된 **후**에. 유입이 이탈하면 기회는 한 번뿐.
- **규모:** M / **스타 영향:** 최상 (증폭기)

### C-5. awesome 리스트 PR

- **무엇:** awesome-unity, awesome-claude-code, awesome-ai-agents, awesome-mcp-servers(A-1 이후) 등재 PR.
- **왜:** 지속 유입 채널. 각 리스트의 기여 가이드 준수 필요.
- **규모:** S / **스타 영향:** 중

### C-6. 뱃지 정비

- **무엇:** Unity 버전 매트릭스 뱃지(2022.3 LTS ✅ / 6000.x ✅ — docs/25가 이미 증거), CI 상태, Go Report Card, 최신 릴리스, 라이선스.
- **왜:** 신뢰 신호의 저비용 축적. 특히 "실제 Unity 두 버전에서 검증됨"은 이 도구 카테고리에서 희소한 주장.
- **규모:** S / **스타 영향:** 하중

### C-7. 커뮤니티 준비 (CONTRIBUTING.md + 이슈 템플릿 + good-first-issue)

- **무엇:** 기여 가이드, 버그 리포트 템플릿(doctor/check 출력 첨부 요구), good-first-issue 라벨링.
- **왜:** 스타가 붙기 시작하면 이슈/PR이 온다. 준비 안 된 리포는 그 에너지를 흘린다.
- **규모:** S / **스타 영향:** 하 (장기 복리)

---

## D. 완성도 — v1.0 나머지 봉우리

### D-1. scenario `--filter` 전파 ★ (Case Study 0002 결함 A)

- **무엇:** `testplay run --scenario S --filter X`에서 filter를 모든 인스턴스에 전파하거나, 최소한 **exit 5로 명시 거부**. 현재의 silent 무시가 최악의 상태.
- **왜:** v0.11 hardening 커밋 로그에서 아직 확인되지 않는 **유일한 0002 필수 항목**. "silent failure는 가장 비싼 실패다"(0002 교훈 2).
- **설계 결정 필요:** 전파 시 인스턴스별 필터 오버라이드(`instances[].filter`)와의 우선순위 정의. 1단계로 명시 거부(exit 5) → 2단계로 전파 구현이 안전한 순서.
- **규모:** S(거부) ~ M(전파) / **완성도 영향:** 최상 / **v1.0 blocker**

### D-2. real-subprocess scenario E2E

- **무엇:** 감사 실행 경로 (2)번 — 실제 서브프로세스로 host+client scenario를 도는 E2E (`//go:build e2e`).
- **왜:** 0002가 증명했듯 오케스트레이션 결함 클래스는 유닛 테스트로 못 잡는다. "기능 출시 = 한 가지 dogfooding 워크플로우에서 처음부터 끝까지 사용 가능" 게이트의 자동화판.
- **규모:** M / **완성도 영향:** 최상 / **v1.0 blocker**

### D-3. `testplay validate-scenario <file>` (또는 `check --scenario <file>`)

- **무엇:** 실행 없이 scenario JSON의 스키마·의존 그래프(사이클)·config 파일 존재·phase 정합성·env 키 형식을 검증.
- **왜:** 0002에서 30분+ 낭비했던 경험의 직접 해독제. "398 테스트 실행이 끝난 후에야 발견"을 "실행 전 1초에 발견"으로.
- **설계 결정 필요:** 커맨드 표면 — 6-command 계약 보호를 위해 `check --scenario <file>` 확장이 우선 후보.
- **규모:** S~M (Load()의 검증 로직이 이미 대부분 존재 — 노출만) / **완성도 영향:** 상

### D-4. scenario 집계 status 파일

- **무엇:** per-role 파일(`testplay-status-<role>.json`)에 더해 시나리오 전체 뷰(`testplay-scenario-status.json`) — 각 인스턴스의 현재 phase/seq + orchestrator 상태.
- **왜:** Known Limitations에 등재된 항목. 에이전트가 N개 파일 폴링 대신 1개 폴링. 파일 수가 늘어나는 것이 아니라 **폴링 로직이 사라지는** 것이 가치.
- **규모:** M / **완성도 영향:** 중

### D-5. CONTRACT.md — 계약 동결 문서

- **무엇:** exit code 표 + JSON 필드 계약(조건부 필드 규칙 13개) + status phase 전이도를 단일 버전 문서로. v1.0에서 "이 문서가 SemVer의 대상"이라고 선언.
- **왜:** "The Honest Contract"의 형식적 완성. A-4(JSON Schema)와 상호 참조하면 문서-코드 drift도 CI로 방지.
- **규모:** S / **완성도 영향:** 상 (선언적 가치)

---

## E. 러너로서의 깊이 — 에이전트 가치 증분

### E-1. `result --flaky` — 플레이키 감지

- **무엇:** `.testplay/results/` run history에서 테스트별 pass/fail 플립 이력을 분석 — 최근 N run에서 결과가 바뀐 테스트 목록 + 플립 횟수.
- **왜:** 에이전트에게 "이 실패는 네 코드 잘못이 아닐 수 있다"는 신호는 매우 값지다(불필요한 수정 루프 방지). **데이터는 이미 쌓이고 있다** — 파싱/집계만 추가.
- **규모:** M / **에이전트 가치:** 상

### E-2. 테스트별 duration 이력 + slowest 리포트

- **무엇:** 같은 run history 데이터로 테스트별 소요 시간 추이, `result --slowest` 상위 N.
- **왜:** E-1과 데이터 소스 공유. CI 시간 최적화의 근거 자료.
- **규모:** S (E-1 위에) / **가치:** 중

### E-3. `--retry-flaky N`

- **무엇:** 실패 테스트를 N회까지 재실행하되, JSON에 `retried: true` + 시도별 결과를 **정직하게 공개**.
- **왜:** CI 현실론. 은폐가 아니라 공개된 재시도는 계약 정신과 양립한다.
- **긴장:** identity anchor 재확인 필요 — "재시도로 green 만들기"는 정직성 훼손 경계선에 있다. **공개 필드가 타협 불가 조건.** E-1이 먼저(감지 없는 재시도는 은폐).
- **규모:** M / **가치:** 중 / **우선순위:** E-1 이후

### E-4. Unity Code Coverage 패스스루

- **무엇:** config에 `coverage: true` → `-enableCodeCoverage` + 관련 인자 전달, 커버리지 요약(라인 %)을 run JSON에 포함.
- **왜:** 에이전트가 "테스트를 추가했는데 커버리지가 올랐는가"를 닫힌 루프로 확인 가능.
- **리스크:** Unity Code Coverage 패키지 존재 여부 사전 확인 필요(없으면 exit 5 또는 warning). 출력 포맷의 Unity 버전별 차이 검증 필요.
- **규모:** M / **가치:** 중상

### E-5. `result --diff <run1> <run2>`

- **무엇:** new_failures를 넘어 fixed / still-failing / new-failure / new-pass 4분류 비교.
- **왜:** `--compare-run`의 일반화. 에이전트의 "내 수정이 무엇을 바꿨나" 질문에 완전한 답.
- **규모:** S~M / **가치:** 중상

---

## F. 큰 스윙 — v1.x 방향성

### F-1. PlayMode-warm 브리지

- **무엇:** v0.10에서 명시적으로 유보한 항목 — warm Editor에서 PlayMode 테스트 실행.
- **왜:** PlayMode는 궁극 목표(네트워크 테스트)의 전제이고, 경쟁 부재 영역. EditMode bridge에서 검증된 프로토콜/Pristine Gate 기반 위에 증축.
- **리스크:** PlayMode 진입/이탈의 도메인 리로드, 씬 상태 오염 — Pristine Gate 확장 설계가 선행 필요. 0010식 실제 Unity 검증 필수.
- **규모:** L / **v1.1+ 후보**

### F-2. NGO/Mirror 네트워크 하니스

- **무엇:** scenario 모드 + IPC 버스 위에 NGO/Mirror용 헬퍼(역할별 환경 주입 프리셋, 포트 할당, readiness 프로토콜)를 얹는 통합.
- **왜:** "멀티플레이어 테스트 자동화"는 Unity 커뮤니티의 오래된 미해결 통증. 이게 되는 순간 testplay는 니치 툴에서 **카테고리 정의자**가 된다. Ultimate goal과 일치.
- **전제:** D-1, D-2 (scenario 모드가 실용화되어야 함). 0002의 "인프라는 통과, 오케스트레이션은 미통과" 상태 해소가 선행.
- **규모:** L / **v1.x 후보**

### F-3. CI 테스트 샤딩

- **무엇:** suite를 N개 병렬 shadow workspace로 분할 실행 후 결과 병합.
- **긴장 (identity anchor):** "속도 계층이 아니다"와 충돌하는가? — 해석: 사람 TDD의 반복 속도가 아니라 **자동화 호출자의 CI 처리량**이므로 계약 내로 해석 가능. 단, 결과 정확성(병합 시 exit code 시맨틱 보존)이 우선이며, 워커 간 결과가 달라질 수 있는 테스트(공유 상태)는 사용자 책임임을 명시.
- **규모:** L / **보류 권장** — v1.0 전에는 착수하지 않음

### F-4. OpenTelemetry / webhook 결과 방출

- **무엇:** run 완료 시 결과 요약을 OTLP 또는 webhook으로 방출(옵트인 config).
- **왜:** CI 관측 파이프라인(Grafana/Datadog) 연결. 팀 단위 도입의 근거.
- **규모:** M / **v1.x 후보**

---

## G. 의도적으로 하지 않을 것 (identity anchor 인용)

| 항목 | 이유 |
|---|---|
| **watch 모드 / 에디터 내 빠른 반복** | Test Runner 창의 일. testplay는 계약 계층이지 속도 계층이 아니다 — 속도-정확성 트레이드오프는 항상 정확성 승. |
| **Godot/Unreal 확장** | v1.0 전에는 브랜드 희석. Unity 계약을 먼저 완결한다. (v2+ 장기 브랜드 플레이로만 재검토 — "게임 엔진 테스트 러너의 정직한 계약") |
| **결과 은폐형 재시도** | E-3 참조 — `retried` 필드 공개 없는 재시도는 the honest contract 위반. |
| **텔레메트리 수집** | 계약 계층의 신뢰가 자산. README에 "no telemetry" 명시는 오히려 저비용 신뢰 신호(선택). |

---

## H. 우선순위 및 실행 시나리오

### 스타 관점 Top 5

| 순위 | 항목 | 근거 |
|---|---|---|
| 1 | **D-1 scenario filter 전파/거부** | 마지막 완성도 구멍. 유입 후 이탈 방지가 모든 마케팅에 선행 |
| 2 | **A-2 GitHub Action + A-5 JUnit** | CI-side 최대 레버(MCP 대체). PR 인라인 어노테이션이 킬러 데모, 계약 무손상, Marketplace 발견 채널 |
| 3 | **C-1 + C-2 데모 GIF/asciinema** | 며칠 작업으로 README 전환율을 바꿈 |
| 4 | **B-1 Homebrew + B-2 Unity 자동탐지** | 5분 성공 경로 완성 |
| 5 | **B-3 examples/ + C-3 비교표** | 유입을 스타로 전환하는 깔때기 바닥 |

> **탈락: A-1 MCP 서버** (2026-07-09 재평가로 강등). CLI 계약과 중복·CI 무효·좁은 목적이라 12k 파도를 못 탐. v1.0 이후 선택적 발견 채널로만.

### 단계별 시나리오

```
v0.11 (진행 중)
  └─ 감사 strict blocker 마무리 + D-1 (filter 거부라도) + D-2 (scenario E2E)

v1.0-rc
  └─ D-3 validate-scenario, D-5 CONTRACT.md, A-4 JSON Schema
  └─ B-1 Homebrew, B-2 Unity 자동탐지, B-3 examples/
  └─ C-1, C-2 데모 자산, C-3 비교표, C-6 뱃지

v1.0 GA — 런치 모먼트 ★
  └─ C-4 런치 콘텐츠 동시 게시 (HN / r/Unity3D / Unity Discussions / 국내 커뮤니티)
  └─ C-5 awesome 리스트 PR 일괄 (awesome-unity / awesome-claude-code / awesome-ai-agents)
  └─ A-2 GitHub Action Marketplace 등재 + A-5 JUnit (CI 청중을 런치와 함께 개방)

v1.0.x ~ v1.1
  └─ A-3 에이전트 스킬 번들, E-1 flaky 감지
  └─ B-4 doctor, D-4 집계 status
  └─ (선택) A-1 MCP 서버 — 순수 발견 채널 목적, CLI 위 얇은 어댑터로만. 필요성 낮음

v1.x
  └─ F-1 PlayMode-warm → F-2 네트워크 하니스 (ultimate goal)
  └─ E-3 retry-flaky, E-4 coverage, F-4 OTel
```

**핵심 반복:** 스타는 순간에 온다. v1.0 GA에 설치 경로(B-1), 데모(C-1/2), 예제(B-3), 런치 글(C-4), 등재(C-5, A-2)를 **한 시점에 겹치는 것**이 이 문서 전체에서 가장 중요한 한 줄이다.

---

## References

- Case Study 0002 — Scenario Mode Orchestration Limits (결함 A/B/C, 교훈 3개)
- pre-v1.0 종합 감사 (2026-07-07) — 132건 확정, 실행 경로 4단계
- docs/25 — v0.10.0 bridge 실기 검증 (Unity 6000.3.8f1 + 2022.3.62f3)
- CLAUDE.md — Identity anchor ("계약 계층, 속도 계층 아님"), Output Design Rules 13개
- RELEASE-PLAN.md
