# PvP: 인메모리 불가 및 Redis 전용 아키텍처 제안서

본 문서는 Kakao Chess Bot의 PvP 기능을 인메모리 의존 없이 Redis 기반으로 일관되게 설계/구현하기 위한 기준과 실행 방안을 정리합니다. 기존 채널 로비 문서와 함께 참고하십시오.

- 관련 문서: `docs/pvp-channel-lobby.md`

## Report

- 현재 상태
  - PvP는 채널 로비 기반(`internal/pvpchan/*`)으로 동작. 과거의 `internal/pvp/*` 자동수락(PENDING 미사용) 구현은 제거됨.
  - 체스 PvP 상태 저장소는 Redis 전제: `internal/pvpchess/manager.go`가 `REDIS_URL` 필수 + Ping 검증.
  - Draw/Abort/시간제 파싱 미구현(의도적 제거), 교차 방 브로드캐스트는 2방(Origin/Resolve) 한정.
  - WS 재연결/백오프 관측 로그 최소.
  - 정책 고지: 커스텀 헤더(X‑User‑*) 미사용(의도), 영어 별칭(status/board) 미제공(의도).

- 인메모리 한계
  - 단일 프로세스에서만 상태 일관 가능, 재기동 시 세션/게임 소실.
  - 다중 인스턴스/교차 방 동시 참가/경합 처리 불가.
  - 참가/시작의 원자성 보장 어려움(동시 `join` 경쟁).
  - 운영 기준(AGENTS.md): Redis=기본, 인메모리=옵션. 현재 코드도 Redis 없이는 PvP 초기화 실패.

결론: 인메모리만으로는 PvP 요구사항(교차 방, 동시성, 복구성)을 충족할 수 없습니다. 개발/운영 모두 Redis 기반이 타당합니다.

## Proposal

- 원칙
  - PvP 채널 로비를 공식 흐름으로 채택. 인메모리 폴백은 도입하지 않음.
  - Redis를 단일 소스 오브 트루스로 사용(24h TTL 캐시 + 결과는 Postgres 업서트).
  - 동시성은 Redis WATCH(EVAL 대안)로 원자성 보장.

- 범위/목표
  - 채널(방) 모델: `make`로 생성, `join`으로 참가, 2인 충족 시 게임 시작.
  - 교차 방 브로드캐스트: 채널에 등록된 모든 방으로 보드/상태 전파.
  - 레거시 `pvp …` 경로는 제거하고, 한국어 상위 명령(방 생성/참가/보드/현황/기권)으로 통일.
  - 시간제 제거: 파싱/집행/표시 비활성(의도).

- 데이터 모델(Redis)
  - `ch:<id>`: 채널 메타(JSON)
    - `id`, `state`(LOBBY|ACTIVE|FINISHED|ABORTED), `created_at`
    - `creator_id`, `creator_name`
    - `white_id`, `white_name`, `black_id`, `black_name`
    - `game_id`
  - `ch:<id>:participants`: 참가자 Set(최대 2)
  - `ch:<id>:rooms`: 브로드캐스트 대상 방 Set
  - (제거) 무승부 제안 상태 저장 키는 사용하지 않습니다.
  - TTL: 채널 키군 24h, 게임 키(`pvp:game:<gid>`) 24h 유지

- 패키지/API
  - 신규 패키지: `internal/pvpchan/`
    - `types.go`: Channel/State/Errors/DTO
    - `manager.go`: API(아래)
    - `store_redis.go`: Redis 접근(WATCH/EVAL, TTL, JSON)
  - API(초안)
    - `Make(room, userID, userName, color, timeCtl) -> (code, meta)`
    - `Join(room, code, userID, userName, colorOpt) -> (start, gameID)`
    - `Rooms(code) -> []room`
    - `OfferDraw(userID)`, `AcceptDraw(userID)`
    - `Abort(userID)`(첫 수 전)
    - `ResignWrap(userID)`(pvpchess.Resign 위임)
  - 채널 코드: `CH-` + 6자리 대문자 영숫자(crypto/rand)

- 명령 계약
  - `!체스 방 생성`
  - `!체스 방 리스트`
  - `!체스 참가 <코드>`
  - `!체스 보드 | 현황`
  - `!체스 기권`

  색 배정: 항상 랜덤

- PvP 체스 매니저 보강
  - 동작:
    - 수 입력/보드/현황/기권만 명령으로 지원. 규칙상 자동 무승부는 엔진 상태로 처리.
    - 시간제 옵션: 제공하지 않음(표시/집행 모두 비활성).

- 교차 방 브로드캐스트
  - `Rooms(code)`에서 전체 방 취득 → 보드/상태 전파 시 전방송.
  - 중복 방은 호출 측에서 한 번만 출력.

- 관측성
  - WS: 재연결 시도/백오프/최근 에러 구조화 로그.
  - PvP 이벤트 카운터: make/join/start/move/resign/finish.

- 보안/검증
  - 레거시와 동일하게 HTTP/WS 커스텀 헤더(X-User-*)를 사용하지 않습니다. Iris 메시지의 `user_id`/`room`으로 식별하며, 필요 시(정책 변경) 게이트로 재도입합니다.
  - 사용자/방 식별: Iris 메시지의 `user_id`/`room` 사용.
  - 제약: “한 사용자·한 방 1대국” 유지.
  - 영어 별칭 미제공: `status/board` 등 영어 별칭은 제공하지 않습니다(한국어 명령 사용).

- 환경/운영
  - Redis 7, Postgres 16 단독 컨테이너(Non-root). Compose 개발 중 사용 금지.
  - `.env.example`를 템플릿으로 실제 `.env`는 커밋 금지.
  - PvP 전용 모드: `CHESS_PVP_ONLY=true` 설정 시 싱글(엔진) 비활성. Stockfish 불필요.

## Approval

다음 항목에 대해 승인을 요청드립니다.
- Redis 전용 채널 로비 구현(인메모리 폴백 제외)
- `internal/pvpchan/` 도입 및 Redis 키 스키마 채택(상기 명세)
- 한국어 상위 명령 통일 및 도움말 갱신
- 수동 무승부/중단 제거 반영
- WS 재연결/백오프 구조화 로그 추가
- 구현 검증을 위한 `go mod tidy` · `go build ./...` 실행

## Execution

- 1차 구현(패치 범위)
  - `internal/pvpchan/*` 스캐폴딩 + Redis 트랜잭션 로직
  - `cmd/chess-bot/main.go` 한국어 상위 명령 라우팅 정리 및 도움말 갱신
  - `internal/pvpchess`: 수동 무승부/중단 제거 후 상태만 유지
  - `internal/irisfast/ws_nhooyr.go` 관측성 로그 추가
  - 최소 유닛테스트 추가

- 수용 기준(1차)
  - `make/join`로 2인 매칭 및 시작 성공
  - `보드/현황`, `수`, `기권` 정상 동작(규칙상 자동 무승부는 엔진 결과로 확인)
  - 교차 방 보드/결과 브로드캐스트 확인
  - Redis에 채널/게임 키 생성 및 TTL 설정 확인

- 일정(제안)
  - D+1: pvpchan 스캐폴딩/명령 라우팅/도움말
  - D+2: 정책 반영(무승부 자동/중단 미지원) 및 관측 로그
  - D+3: 테스트/스모크 및 피드백 반영

## Risks & Mitigations

- Redis 장애 시 PvP 전체 영향
  - 완곡 실패(친절 메시지) + 재시도 가이드. 인메모리 폴백은 미도입(일관성/운영 리스크).
- 동시 `join` 경합
  - WATCH(EVAL)로 원자성 확보. 실패 시 사용성 메시지 제공.
- 멀티 인스턴스 확장
  - 1단계: 단일 인스턴스 가정, Redis 원자성만 활용
  - 2단계(게이트): Redis Pub/Sub/Streams 기반 이벤트 버스 검토

## Alternatives

- 인메모리 폴백: 단일 프로세스 제한, 재기동/확장/경합 불가 → 불채택
- DB 직결만 사용: 핫패스 성능/동시성/TTL 운용 어려움 → Redis+DB(결과) 혼용 유지
- 초기 Pub/Sub 도입: 변경 게이트로 후속 검토

## Testing Plan

- pvpchan: make/join 경합·제약 위반, rooms 브로드캐스트
- cmd/chess-bot: 서브커맨드 파서/흐름
- pvpchess: SAN/UCI 파싱, 불법수 응답, resign 처리 및 자동 무승부 전이
- WS: 재연결/백오프 상태 전이 콜백(단위 스텁)

## Runbook(개발)

- 사전
  - `.env.docker`(미커밋): `REDIS_PASSWORD`, `POSTGRES*`
  - Redis: `docker volume create chess_redis` → `docker run -d --name chess-redis --env-file .env.docker -p 6379:6379 -v chess_redis:/bitnami/redis bitnami/redis:7`
  - Postgres: `docker volume create chess_pg` → `docker run -d --name chess-postgres --env-file .env.docker -p 5432:5432 -v chess_pg:/bitnami/postgresql bitnami/postgresql:16`
- 앱 환경
  - `REDIS_URL=redis://:REDIS_PASSWORD@localhost:6379/0`
  - `DATABASE_URL=postgres://USER:PASS@localhost:5432/DB?sslmode=disable`
- 빌드
  - `go mod tidy`
  - `go build ./...`
  - `go run ./cmd/irischeck` → Iris HTTP/WS 점검
  - PvP 전용 실행 예시(엔진 없이):
    - `export CHESS_PVP_ONLY=true`
    - `export REDIS_URL=redis://:PASSWORD@localhost:6379/0`
    - `export DATABASE_URL=postgres://USER:PASS@localhost:5432/DB?sslmode=disable`
    - `go run ./cmd/chess-bot`
  - 일반 모드(싱글 포함) 실행 예시:
    - `export CHESS_PVP_ONLY=false`
    - `export STOCKFISH_PATH=/usr/local/bin/stockfish`
    - 그 외 동일

## Change Gates

- 동시성 모델 변경(채널/브로드캐스트), 명령 추가/행동 변화, Pub/Sub 도입은 별도 승인 후 진행.

## Memory Entry

```
Context: PvP Redis 전용 채널 로비 채택
Directive: PvP는 Redis 기반 채널 모델로 일관 구현. 인메모리 폴백 미도입. 보안 헤더는 레거시와 동일하게 미사용.
Evidence: docs/pvp-channel-lobby.md, docs/pvp-redis-architecture.md, internal/pvpchess/manager.go, internal/pvpchess/types.go
Review-Date:
```
