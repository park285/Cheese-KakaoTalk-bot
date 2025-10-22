package chesspresenter

import (
	"fmt"
	"strings"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/util"
	"github.com/park285/Cheese-KakaoTalk-bot/pkg/chessdto"
)

const (
	chessHistoryInstruction = "♜ 최근 기보"
	chessHelpInstruction    = "♞ 체스 명령어 안내"
	chessProfileInstruction = "♞ 체스 프로필"

	defaultPreset        = "level3"
	materialScoreNeutral = 39
	capturedRecentLimit  = 3
)

// PrefixProvider exposes the Prefix that Kakao messages should use.
type PrefixProvider interface {
	Prefix() string
}

// Formatter renders chess DTOs into Kakao-friendly text blocks.
type Formatter struct {
	prefixProvider PrefixProvider
}

func NewFormatter(provider PrefixProvider) *Formatter {
	return &Formatter{prefixProvider: provider}
}

func (f *Formatter) Prefix() string {
	if f == nil || f.prefixProvider == nil {
		return ""
	}
	return strings.TrimSpace(f.prefixProvider.Prefix())
}

func (f *Formatter) Start(state *chessdto.SessionState, resumed bool) string {
	if state == nil {
		if resumed {
			return fmt.Sprintf("진행 중인 체스 게임 정보를 불러오지 못했습니다. `%s체스 현황` 명령으로 다시 확인해주세요.", f.Prefix())
		}
		return fmt.Sprintf("체스 게임을 시작할 수 없습니다. `%s체스 시작`을 다시 시도해주세요.", f.Prefix())
	}

	var sb strings.Builder
	if resumed {
		sb.WriteString("♞ 진행 중인 체스 게임을 불러왔습니다.\n")
	} else {
		sb.WriteString("♟️ 체스 게임을 시작했습니다.\n")
	}
	sb.WriteString(fmt.Sprintf("• 난이도: %s\n", formatPreset(state.Preset)))
	if profile := state.Profile; profile != nil {
		sb.WriteString(fmt.Sprintf("• 레이팅: %d", profile.Rating))
		if delta := state.RatingDelta; delta > 0 {
			sb.WriteString(fmt.Sprintf(" (▲%d)", delta))
		} else if delta < 0 {
			sb.WriteString(fmt.Sprintf(" (▼%d)", -delta))
		}
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("• 전적: %d승 %d패 %d무 (%d판)", profile.Wins, profile.Losses, profile.Draws, profile.GamesPlayed))
		if profile.PreferredPreset != "" {
			sb.WriteString(fmt.Sprintf(" | 선호: %s", formatPreset(profile.PreferredPreset)))
		}
		sb.WriteString("\n")
	}
	prefix := f.Prefix()
	sb.WriteString("• 플레이어는 백으로 시작합니다.\n")
	sb.WriteString("\n이동 방법: `")
	sb.WriteString(prefix)
	sb.WriteString("체스 <수>` .\n")
	sb.WriteString("무르기 기능: `")
	sb.WriteString(prefix)
	sb.WriteString("체스 무르기`.\n")
	sb.WriteString("난이도 선택: level1~level8.")
	return sb.String()
}

func (f *Formatter) Assist(suggestion *chessdto.AssistSuggestion) string {
	if suggestion == nil {
		return "엔진이 추천 수를 제공하지 못했습니다."
	}

	move := strings.ToLower(strings.TrimSpace(suggestion.MoveUCI))
	if move == "" {
		return "엔진이 추천 수를 제공하지 못했습니다."
	}
	return f.Prefix() + "체스 " + move
}

func (f *Formatter) Move(summary *chessdto.MoveSummary) string {
	if summary == nil || summary.State == nil {
		return ""
	}
	if !summary.Finished {
		return ""
	}

	state := summary.State
	var sb strings.Builder
	sb.WriteString(formatOutcome(state.Outcome, state.OutcomeMeta))
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("• 난이도: %s\n", formatPreset(state.Preset)))

	if profile := summary.Profile; profile != nil {
		delta := summary.RatingDelta
		sb.WriteString(fmt.Sprintf("• 현재 레이팅: %d", profile.Rating))
		if delta > 0 {
			sb.WriteString(fmt.Sprintf(" (▲%d)", delta))
		} else if delta < 0 {
			sb.WriteString(fmt.Sprintf(" (▼%d)", -delta))
		} else if profile.GamesPlayed > 0 {
			sb.WriteString(" (변동 없음)")
		}
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("• 누적 전적: %d승 %d패 %d무 (%d판)\n", profile.Wins, profile.Losses, profile.Draws, profile.GamesPlayed))
		sb.WriteString("\n")
	} else {
		sb.WriteString("\n")
	}

	if summary.GameID > 0 {
		sb.WriteString(fmt.Sprintf("기보 ID: #%d\n", summary.GameID))
	}
	return sb.String()
}

func (f *Formatter) Status(state *chessdto.SessionState) string {
	if state == nil {
		return f.Help()
	}
	var sb strings.Builder
	sb.WriteString("♞ 체스 현황\n")
	sb.WriteString(fmt.Sprintf("• 난이도 %s\n", formatPreset(state.Preset)))
	sb.WriteString(fmt.Sprintf("• 진행 %d수\n", state.MoveCount))
	if len(state.MovesSAN) > 0 {
		sb.WriteString(fmt.Sprintf("• 최근 %s\n", formatRecentMoves(state.MovesSAN)))
	}
	if info := formatProfileSummary(state.Profile, state.RatingDelta); info != "" {
		sb.WriteString(info)
	}
	appendMaterialLine(&sb, state.Material)
	appendCapturedLine(&sb, state.Captured)

	prefix := f.Prefix()
	sb.WriteString("\n명령: `")
	sb.WriteString(prefix)
	sb.WriteString("체스 <수>` (SAN/UCI)\n기권: `")
	sb.WriteString(prefix)
	sb.WriteString("체스 기권`\n무르기: `")
	sb.WriteString(prefix)
	sb.WriteString("체스 무르기`.")
	return sb.String()
}

func (f *Formatter) Resign(state *chessdto.SessionState) string {
	var sb strings.Builder
	sb.WriteString("🏳️ 기권 처리되었습니다.\n")
	if state == nil {
		sb.WriteString("🛑 기권하여 패배로 기록되었습니다.")
		return sb.String()
	}
	sb.WriteString(formatOutcome(state.Outcome, state.OutcomeMeta))
	if profile := state.Profile; profile != nil {
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("• 레이팅: %d", profile.Rating))
		if delta := state.RatingDelta; delta > 0 {
			sb.WriteString(fmt.Sprintf(" (▲%d)", delta))
		} else if delta < 0 {
			sb.WriteString(fmt.Sprintf(" (▼%d)", -delta))
		}
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("• 전적: %d승 %d패 %d무 (%d판)", profile.Wins, profile.Losses, profile.Draws, profile.GamesPlayed))
	}
	return sb.String()
}

func (f *Formatter) Help() string {
	content := fmt.Sprintf(`%s
• %s체스 시작 [난이도]
  새 게임 시작 (난이도: level1~level8)
• %s체스 <수> (예: e2e4)
  SAN/UCI 모두 입력 가능
• %s체스 기권
  즉시 기권하고 세션 종료
• %s체스 무르기
  마지막 한 수(플레이어+봇) 되돌리기
• %s체스 기록 [n]
  최근 기보 확인 (기본 10개)
• %s체스 기보 <ID>
  특정 기보 상세 보기
• %s체스 프로필
  개인 승률·레이팅 확인`, chessHelpInstruction,
		f.Prefix(), f.Prefix(), f.Prefix(), f.Prefix(), f.Prefix(), f.Prefix(), f.Prefix())

	return util.ApplyKakaoSeeMorePadding(stripChessHeader(content, chessHelpInstruction), chessHelpInstruction)
}

func (f *Formatter) History(games []*chessdto.ChessGame) string {
	var sb strings.Builder
	sb.WriteString(chessHistoryInstruction)
	sb.WriteByte('\n')
	for _, game := range games {
		dateText := formatShortTime(game.EndedAt)
		resultText := formatResultBadge(game.Result)
		durationText := formatGameDuration(game.Duration)
		movesCount := len(game.MovesSAN)
		if movesCount == 0 {
			movesCount = len(game.MovesUCI)
		}
		sb.WriteString(fmt.Sprintf("• #%d %s %s — %s (수: %d)\n", game.ID, resultText, dateText, formatPreset(game.Preset), movesCount))
		if durationText != "" {
			sb.WriteString(fmt.Sprintf("  소요 시간: %s\n", durationText))
		}
	}
	sb.WriteString(fmt.Sprintf("\n자세히 보려면 `%s체스 기보 <ID>` 명령을 사용하세요.", f.Prefix()))

	content := sb.String()
	if strings.TrimSpace(content) == "" {
		return content
	}
	return util.ApplyKakaoSeeMorePadding(stripChessHeader(content, chessHistoryInstruction), chessHistoryInstruction)
}

func (f *Formatter) Game(game *chessdto.ChessGame) string {
	if game == nil {
		return "기보 정보를 불러오지 못했습니다."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("♜ 기보 상세 #%d\n", game.ID))
	sb.WriteString(fmt.Sprintf("• 결과: %s\n", formatResultBadge(game.Result)))
	sb.WriteString(fmt.Sprintf("• 난이도: %s\n", formatPreset(game.Preset)))
	if game.EnginePreset != "" && game.EnginePreset != game.Preset {
		sb.WriteString(fmt.Sprintf("• 엔진 프리셋: %s\n", formatPreset(game.EnginePreset)))
	}
	if !game.StartedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("• 시작: %s\n", formatShortTime(game.StartedAt)))
	}
	if !game.EndedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("• 종료: %s\n", formatShortTime(game.EndedAt)))
	}
	if game.Duration > 0 {
		sb.WriteString(fmt.Sprintf("• 소요 시간: %s\n", formatGameDuration(game.Duration)))
	}
	if game.Blunders > 0 {
		sb.WriteString(fmt.Sprintf("• 블런더: %d회\n", game.Blunders))
	}
	if game.PGN != "" {
		sb.WriteString("\n```pgn\n")
		sb.WriteString(strings.TrimSpace(game.PGN))
		sb.WriteString("\n```\n")
	}
	return sb.String()
}

func (f *Formatter) Profile(profile *chessdto.ChessProfile) string {
	if profile == nil {
		return "저장된 체스 프로필이 없습니다."
	}
	var sb strings.Builder
	sb.WriteString(chessProfileInstruction)
	sb.WriteString("\n")
	if info := formatProfileSummary(profile, 0); info != "" {
		sb.WriteString(info)
	}
	if profile.Streak > 1 {
		sb.WriteString(fmt.Sprintf("• 연속 기록: %d%s 진행 중\n", profile.Streak, formatStreakSuffix(profile.StreakType)))
	}
	if !profile.LastPlayedAt.IsZero() {
		sb.WriteString(fmt.Sprintf("• 마지막 경기: %s\n", formatShortTime(profile.LastPlayedAt)))
	}
	prefix := f.Prefix()
	sb.WriteString(fmt.Sprintf("\n새 게임: `%s체스 시작`, 기록: `%s체스 기록`, 기보 상세: `%s체스 기보 <ID>`", prefix, prefix, prefix))

	content := sb.String()
	if !strings.HasPrefix(content, chessProfileInstruction) {
		return content
	}
	return util.ApplyKakaoSeeMorePadding(stripChessHeader(content, chessProfileInstruction), chessProfileInstruction)
}

func (f *Formatter) PreferredPresetUpdated(profile *chessdto.ChessProfile) string {
	if profile == nil {
		return "선호 난이도를 업데이트하지 못했습니다. 잠시 후 다시 시도해주세요."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ 선호 난이도를 %s로 설정했습니다.\n", formatPreset(profile.PreferredPreset)))
	if info := formatProfileSummary(profile, 0); info != "" {
		sb.WriteString(info)
	}
	sb.WriteString(fmt.Sprintf("새 게임 시작: `%s체스 시작`", f.Prefix()))
	return sb.String()
}

func (f *Formatter) Undo(state *chessdto.SessionState) string {
	if state == nil {
		return "무르기 결과를 불러오지 못했습니다."
	}
	var sb strings.Builder
	sb.WriteString("↩️ 마지막 한 수를 되돌렸습니다.\n")
	sb.WriteString(fmt.Sprintf("• 난이도: %s\n", formatPreset(state.Preset)))
	sb.WriteString(fmt.Sprintf("• 현재 진행 수: %d\n", state.MoveCount))
	if info := formatProfileSummary(state.Profile, 0); info != "" {
		sb.WriteString(info)
	}
	appendMaterialLine(&sb, state.Material)
	appendCapturedLine(&sb, state.Captured)
	sb.WriteString("\n이제 다시 당신의 턴입니다. `")
	sb.WriteString(f.Prefix())
	sb.WriteString("체스 <수>` 형식으로 새 수를 입력하세요.")
	sb.WriteString(fmt.Sprintf("\n최근 기록은 `%s체스 기록`으로 확인할 수 있습니다.", f.Prefix()))
	return sb.String()
}

func (f *Formatter) NoSession() string {
	return fmt.Sprintf("진행 중인 체스 게임이 없습니다. `%s체스 시작`으로 새 게임을 시작하세요.", f.Prefix())
}

func stripChessHeader(text, header string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	candidates := []string{
		header + "\r\n\r\n",
		header + "\n\n",
		header + "\r\n",
		header + "\n",
		header,
	}

	for _, candidate := range candidates {
		if strings.HasPrefix(text, candidate) {
			return strings.TrimPrefix(text, candidate)
		}
	}
	return text
}

func formatPreset(preset string) string {
	if strings.TrimSpace(preset) == "" {
		return defaultPreset
	}
	return strings.ToLower(preset)
}

func formatProfileSummary(profile *chessdto.ChessProfile, ratingDelta int) string {
	if profile == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("• 현재 레이팅: %d", profile.Rating))
	if ratingDelta > 0 {
		sb.WriteString(fmt.Sprintf(" (▲%d)", ratingDelta))
	} else if ratingDelta < 0 {
		sb.WriteString(fmt.Sprintf(" (▼%d)", -ratingDelta))
	} else if profile.GamesPlayed > 0 {
		sb.WriteString(" (변동 없음)")
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("• 누적 전적: %d승 %d패 %d무 (%d판)\n", profile.Wins, profile.Losses, profile.Draws, profile.GamesPlayed))
	if profile.PreferredPreset != "" {
		sb.WriteString(fmt.Sprintf("• 선호 난이도: %s\n", formatPreset(profile.PreferredPreset)))
	}
	return sb.String()
}

func formatStreakSuffix(streakType string) string {
	switch strings.ToLower(strings.TrimSpace(streakType)) {
	case "win":
		return "연승"
	case "loss":
		return "연패"
	case "draw":
		return "연속 무승부"
	default:
		return "연속 기록"
	}
}

func formatRecentMoves(moves []string) string {
	if len(moves) == 0 {
		return "-"
	}
	const limit = 4
	if len(moves) <= limit {
		return strings.Join(moves, " ")
	}
	return "… " + strings.Join(moves[len(moves)-limit:], " ")
}

func formatOutcome(outcome, method string) string {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "white_won":
		return "✅ 승리했습니다! 축하드립니다."
	case "black_won":
		if strings.ToLower(strings.TrimSpace(method)) == "resignation" {
			return "🛑 기권하여 패배로 기록되었습니다."
		}
		return "❌ 패배했습니다. 다음엔 더 나은 수를 준비해봐요."
	case "draw":
		return "🤝 무승부로 종료되었습니다."
	default:
		return "게임이 종료되었습니다."
	}
}

func formatResultBadge(result string) string {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "win":
		return "✅ 승"
	case "loss":
		return "❌ 패"
	case "draw":
		return "🤝 무"
	default:
		return "▫️ 진행"
	}
}

func formatShortTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return util.FormatKST(t, "2006-01-02 15:04")
}

func formatGameDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func appendMaterialLine(sb *strings.Builder, material chessdto.MaterialScore) {
	if sb == nil {
		return
	}
	sb.WriteString("• 잡은 기물 점수 ")
	sb.WriteString(formatMaterial(material))
	sb.WriteString("\n")
}

func appendCapturedLine(sb *strings.Builder, captured chessdto.CapturedPieces) {
	if sb == nil {
		return
	}
	formatted := formatCaptured(captured)
	if formatted == "" {
		return
	}
	sb.WriteString("• 잡은 기물 ")
	sb.WriteString(formatted)
	sb.WriteString("\n")
}

func formatMaterial(score chessdto.MaterialScore) string {
	whiteCaptured := materialScoreNeutral - score.Black
	blackCaptured := materialScoreNeutral - score.White
	if whiteCaptured < 0 {
		whiteCaptured = 0
	}
	if blackCaptured < 0 {
		blackCaptured = 0
	}

	var parts []string
	if whiteCaptured > 0 {
		parts = append(parts, fmt.Sprintf("백 +%d", whiteCaptured))
	}
	if blackCaptured > 0 {
		parts = append(parts, fmt.Sprintf("흑 +%d", blackCaptured))
	}
	if len(parts) == 0 {
		return "없음"
	}
	return strings.Join(parts, " / ")
}

func formatCaptured(captured chessdto.CapturedPieces) string {
	white := formatCapturedSequence(recentPieces(captured.White, capturedRecentLimit))
	black := formatCapturedSequence(recentPieces(captured.Black, capturedRecentLimit))
	if white == "" && black == "" {
		return ""
	}
	var parts []string
	if white != "" {
		parts = append(parts, "백 "+white)
	}
	if black != "" {
		parts = append(parts, "흑 "+black)
	}
	return strings.Join(parts, " / ")
}

func formatCapturedSequence(order []string) string {
	if len(order) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(order))
	for _, token := range order {
		if symbol := capturedSymbol(token); symbol != "" {
			tokens = append(tokens, symbol)
		}
	}
	return strings.Join(tokens, " ")
}

func capturedSymbol(piece string) string {
	switch strings.ToLower(strings.TrimSpace(piece)) {
	case "queen", "q":
		return "Q"
	case "rook", "r":
		return "R"
	case "bishop", "b":
		return "B"
	case "knight", "n":
		return "N"
	case "pawn", "p":
		return "P"
	default:
		if piece == "" {
			return ""
		}
		return strings.ToUpper(string([]rune(piece)[0]))
	}
}

func recentPieces(order []string, limit int) []string {
	if len(order) == 0 || limit <= 0 {
		return nil
	}
	if len(order) > limit {
		order = order[len(order)-limit:]
	}
	result := make([]string, len(order))
	for i := range order {
		result[i] = order[len(order)-1-i]
	}
	return result
}
