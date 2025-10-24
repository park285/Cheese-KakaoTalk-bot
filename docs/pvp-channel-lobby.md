# PvP 채널 로비 설계서

본 문서는 PvP를 “방 생성 → 참가(구독) → 진행”의 채널 모델로 전환/확장하기 위한 설계 초안입니다. 기존 `pvp @user` 흐름을 유지하되 내부적으로 채널을 활용하는 래퍼로 동작시킬 수 있습니다. 본 설계는 변경 게이트(동시성/행동 변화)에 해당하므로 점진 도입과 게이트 제어를 전제로 합니다.

## 목표(Goals)
- 방(채널) 중심의 PvP 흐름 제공: `make`로 채널 생성, `join`으로 참가.
- 교차 방(멀티 룸) 브로드캐스트: 채널에 구독된 모든 방에 상태/보드를 전파.
- 현 구조와의 호환: 기존 `pvp @user …` 명령은 내부적으로 채널 생성/초대 안내로 매핑.
- Redis를 사용한 상태 보관, Postgres에는 최종 결과만 저장(현행 유지).
- 동시성 안전성: 채널 단위의 짧은 임계 구역, 장기 상주 goroutine 없이 처리.

## 비목표(Non-goals)
- 시간제(3+2 등) 집행/표시 전면 비활성(의도).
- 즉시 멀티 인스턴스 간 이벤트 버스 운영(추후 Pub/Sub/Streams 게이트로 확장 가능).

## 명령 계약(Command Contract)
- `BOT_PREFIX` = 예: `!체스`
- 신규/확장 명령:
  - `방 생성` — 채널 생성, 코드 발급(예: `CH-ABC123`)
  - `방 리스트` — 대기 중인 채널(방) 목록 표시(코드 포함)
  - `참가 <코드>` — 코드로 참가(2명 모이면 게임 시작)
  - `보드 | 현황` — 현 PvP 대국 보드 재표시 (영어 별칭 미제공)
  - `기권` — 기권(RESIGN)
- 초대는 ‘방 생성’ 후 코드 공유, 상대는 ‘참가 <코드>’로 입장합니다(레거시 pvp 경로 제거).

## 상태 모델(State Model)
- 채널 상태: `LOBBY` → `ACTIVE` → `FINISHED` (또는 `ABORTED`)
- 게임 상태: pvpchess의 `ACTIVE`/`FINISHED`/`RESIGNED`/`DRAW` 유지.
- 참가 제약: “1인당/1방당 동시 1대국” 정책 유지(AGENTS.md). 교차 방은 허용하되, 동일 방 기준으로 병행 대국 불가.

## 데이터 모델(Data Model on Redis)
- 키 설계(예시):
  - `ch:<id>`: 채널 메타(JSON)
    - `id`: 채널 ID(예: `CH-ABC123`)
    - `state`: `LOBBY|ACTIVE|FINISHED|ABORTED`
    - `created_at`
    - `creator_id`, `creator_name`
    - `white_id`, `white_name`, `black_id`, `black_name`(선택/배정 결과)
    - `game_id`(시작 후 pvp:game:<gid> 연결)
  - `ch:<id>:participants`: 참가자 집합(Set, 최대 2)
  - `ch:<id>:rooms`: 브로드캐스트 대상 방(Set)
  - (제거) 무승부 제안 상태 저장 키는 사용하지 않습니다.
- TTL: 채널 키군 24h. 게임 키(`pvp:game:<id>`)는 현행 TTL(24h) 유지.
- 결과 저장: 종료 시 기존 Repository로 Postgres `pvp_games` 업서트.

## ID/코드 정책
- 코드 형식: `CH-` 접두 + 6자리 영숫자(대문자). 충돌 방지 위해 crypto/rand 사용.
- UI/메시지: 방 코드와 `join` 예시를 함께 출력.

## 흐름(Flows)
1) 채널 생성(make)
- 입력: 없음
- 동작: Redis에 `ch:<id>` 메타 생성, 상태=LOBBY, `rooms`에 최초 명령 방 추가
- 출력: 채널 코드, `join` 예시 안내

2) 참가(join)
- 입력: 채널 코드, 색 선택(옵션)
- 검증: 채널 존재/LOBBY, 참가자 수<2, 동일 방 1대국 제한 검사
- 처리: 참가자 집합에 추가, `rooms`에 현재 방 추가
- 2명 충족 시 색 배정(요청/랜덤), pvpchess.CreateGameFromChallenge 호출 → 게임 생성, 채널 상태=ACTIVE, game_id 저장
- 브로드캐스트: `rooms`의 모든 방에 시작 보드/안내 출력

3) 수 / 보드 / 현황
- `<수>`: 기존 pvpchess.PlayMove 사용. 보드 이미지를 `rooms` 전방송.
- `보드`/`현황`: 현재 상태 재표시.

4) 기권(resign)
- pvpchess.Resign 호출 → 결과 저장 → 모든 `rooms` 브로드캐스트.

5) 무승부
- 수동 무승부(제안/수락)는 지원하지 않습니다. 규칙상 자동 무승부(예: 스테일메이트)만 허용됩니다.

6) 중단
- 중단 기능은 제공하지 않습니다.

## 시간제(Time Control)
- 본 프로젝트에서는 시간제를 의도적으로 제거합니다. 파싱/집행/표시 모두 비활성이며 스키마/환경변수에도 포함하지 않습니다.

## 교차 방 브로드캐스트(Cross-room Broadcast)
- 채널 `rooms`에 조인/수행 중 관여한 모든 방을 등록.
- 상태/보드/결과를 전방송. 동일 방 중복 방지는 클라이언트에서 1회 출력 보장.

## 동시성/확장성(Concurrency & Scale-out)
- 1단계(기본): 단일 인스턴스 기준, 채널 단위 `sync.Mutex` 또는 Redis 키 단위 트랜잭션(LUA/Watch)로 임계 구역 보호.
- 2단계(옵션): Redis Pub/Sub 또는 Streams(XADD/XREADGROUP) 기반의 채널 이벤트 버스 도입(게이트로 활성).

## 보안/검증(Security)
- 레거시와 동일하게 HTTP/WS 커스텀 헤더(X-User-*)를 사용하지 않습니다. Iris 메시지 페이로드의 `user_id`/`room`으로 식별하며, 필요 시(정책 변경) 게이트로 재도입합니다.
- 사용자/방 식별은 Iris 메시지에서 온 `user_id`/`room` 사용, 입력 값 정규화/검증.
- “한 사용자·한 방 1대국” 제약 체크.

## 관측성(Observability)
- WS 상태 변화/재연결/백오프/최근 에러 로그(구조화) 유지.
- 채널 이벤트 로그: `make/join/start/move/draw/resign/abort/finish` 별 이벤트 카운터.

## 레거시 호환(Legacy Compatibility)
- `pvp @user …` → 내부적으로 `make` 수행, 대상에게 코드 안내 메시지 전송. 사용자는 `join <코드>`로 참가.
- 싱글 체스 명령과의 충돌 방지: 프리픽스 및 명령어 네임스페이스 유지.

## 테스트 계획(Testing)
- 유닛: 채널 생성/참가(제약 위반/정상), 동일 방 중복 대국 차단, 기권 처리.
- 라우팅: 명령 파서(`방 생성/방 리스트/참가/보드/현황/기권`).
- 통합: Iris HTTP/WS 스텁으로 전파/브로드캐스트 검증.
- 동시성: 동시 `join`/`move` 요청 시 정합성 확인.

## 마이그레이션/롤아웃(Migration & Rollout)
- 단계적: `make/join` 노출 후, `pvp @user`를 점진 래핑.
- 게이트: Pub/Sub 도입, 시간제 집행, 이미지 렌더링 고도화는 별도 승인 후 진행.
- 롤백: 채널 생성 비활성화 시 기존 `pvp @user` 직결 흐름으로 복귀 가능.

## Change Gates
- 동시성 모델 변경(채널/브로드캐스트), 외부 행태 변경(명령 추가/의미 변화), Pub/Sub 도입은 승인 필요.

## 오픈 이슈(Open Questions)
- 채널 코드 포맷 최종안(영숫자 길이 고정 여부).
- 관전자(3인 이상) 허용/표시 범위.
- “한 사용자·한 방 1대국”을 “한 사용자 전역 1대국”으로 확장할지 여부.

## 커맨드 예시(Examples)
```
!체스 방 생성
# → CH-ABC123 채널 생성. 안내: 상대는 '!체스 참가 CH-ABC123'으로 참가하세요.

!체스 방 리스트
# → 대기 중인 방 목록과 각 방의 코드 표시

!체스 참가 CH-ABC123
# → 두 명 충족 시 즉시 시작. 보드가 채널에 등록된 모든 방으로 방송됨.

!체스 보드
```

## 환경/실행 메모(Environment & Run Notes)
- PvP 전용 모드: `CHESS_PVP_ONLY=true` 설정 시 싱글(엔진) 비활성화. Stockfish 불필요.
- 일반 모드(싱글 포함)에서는 `STOCKFISH_PATH` 필요.

참고: 색 배정은 항상 랜덤입니다.

## Memory Entry
```
Context: PvP 채널 로비 설계(Separation → PvP 확장)
Directive: 채널(방) 모델을 PvP의 정식 흐름으로 채택. make/join/board/resign 명령 계약 수립. 무승부/중단은 명령 미제공(자동 무승부만). 보안 헤더는 레거시와 동일하게 미사용으로 정합화.
Evidence: docs/pvp-channel-lobby.md, internal/pvpchess/manager.go, internal/pvpchess/types.go
Review-Date: 
```

---

부록: 구현 참고(파일 위치)
- WS/HTTP: `internal/irisfast/*`
- PvP(현행): `internal/pvpchan/*`, `internal/pvpchess/*`, `cmd/chess-bot/main.go`
- 싱글 체스: `internal/service/chess/*`
- 스키마: `db/schema.sql`
