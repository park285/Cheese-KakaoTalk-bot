# Iris 표기가 user로 기록되는 현상

## 개요
- 현상: 수신 로그의 `user` 필드가 실제 발신자가 아닌 "Iris"(시스템 게이트웨이)로 기록되는 경우가 있음.
- 영향: 운영 로그 해석 시 혼동 유발. 명령 파싱/권한/상태 전이에는 영향 없음.

## 배경
- Iris WS로부터 메시지는 두 단계로 유입됨:
  - 최소 프레임: 상위 `Sender`만 포함(표시명 목적)
  - JSON 프레임: `user_id`/`room_id` 등 실제 식별자 포함
- 현재 로직은 최소 프레임의 `sender`를 짧게 캐시 후, 직후 도착하는 JSON 프레임과 합성해 표시용 이름(`displayUser`)을 만든 다음, 이를 진단 로그에 `user`로 기록함.

## 증상
- 예시 로그: `logs/bot.log:151`
  - `2025-10-23 ... | recv_message | {"room": "...", "room_id": "...", "user": "Iris"}`

## 원인 분석
- 메시지 구조 정의: `internal/irisfast/types.go:1`
  - `Message`(상위): `Msg`, `Room`, `Sender`
  - `MessageJSON`(중첩): `user_id`, `message`, `chat_id`, `room_id`, `type`
- WS 수신/콜백 경유: `internal/irisfast/ws_nhooyr.go:100`
- 표시명 합성 및 진단 로그: `cmd/chess-bot/main.go:138`
  - 최소 프레임의 `sender` + JSON의 `user_id`를 조합해 `displayUser` 결정
  - 현재 `recv_message` 로그에 `user`로 `displayUser`를 기록하여, 시스템 `sender`("Iris")가 남을 수 있음
- 참고(무관 항목): `internal/pvpchess/repository.go:122`의 `[Site "Iris"]`는 PGN 메타필드로 본 현상과 무관

## 영향 범위
- 운영/관찰용 로그에서만 의미 혼동 발생
- 명령 파싱/집행·권한·상태 전이에는 영향 없음
- 정책상 커스텀 헤더(X‑User‑*) 미사용(의도)이며, Iris 메시지 페이로드의 사용자/방 정보를 신뢰함.

## 운영 가이드
- 로그 해석 시 필드 의미를 구분해서 사용:
  - `sender`: 프레임 제공자(시스템 포함)
  - `user_id`: 실제 사용자 식별자(JSON에 존재 시 우선)
- `user = "Iris"` 관찰 시, 동일 타임스탬프 ±2s 내 JSON 프레임 유무를 함께 확인
- 알림/대시보드 등 사람 대상 표시는 `user_id` 우선 사용 권장

## 개선 제안(출력만 변경, 기능 불변)
- 진단 로그(`recv_message`) 필드 분리:
  - `sender`: 합성된 표시명(사람 친화)
  - `user_id`: JSON의 실제 사용자 식별자(있을 때)
  - `origin`: `system`(sender가 "Iris" 등) | `user`
- BOT_PREFIX 불일치 메시지는 `debug` 등급으로 강등하거나 생략해 소음 감소
- `MessageJSON.Type`이 제공될 경우 system/human 분류에 보강 지표로 사용

## 정책/준수
- 커스텀 헤더 미사용(의도): README 및 설계 문서에 명시
- 시간제 제거: 스키마/환경 변수/표시 기능에서 제외됨
- 영어 별칭 미제공: 한국어 명령(`현황`/`보드`) 사용

## 코드/로그 근거
- 로그 예시: `logs/bot.log:151`, `logs/bot.log:154`, `logs/bot.log:172`
- 로깅 위치: `cmd/chess-bot/main.go:138`
- 수신 처리: `internal/irisfast/ws_nhooyr.go:100`
- 메시지 구조: `internal/irisfast/types.go:1`
- PGN 메타: `internal/pvpchess/repository.go:122`

## 결정/다음 단계
- 기본: 본 문서로 현상 공유(코드 변경 없음)
- 선택: 진단 로그 필드 분리(출력만 변경) — 별도 승인 후 반영
