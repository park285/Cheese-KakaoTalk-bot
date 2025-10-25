package chesspresenter

import (
	"fmt"
	"strings"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/msgcat"
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

	chessSeeMoreInstructionFallback = "전체보기를 눌러주세요"
	chessSeeMoreInstructionSuffix   = ""

	chessHelpTemplate = "%s\n" +
		"• %s 방 생성\n" +
		"  PvP 채널 생성 및 코드 발급\n" +
		"• %s 방 리스트\n" +
		"  대기 중인 PvP 방 목록(초대 코드 확인)\n" +
		"• %s 참가 <코드>\n" +
		"  코드로 PvP 방 참가\n" +
		"• %s 보드 | 현황\n" +
		"  현재 PvP 대국 보드/현황 표시\n" +
		"• %s 기권\n" +
		"  PvP 대국에서 기권\n" +
		"\n" +
		"• %s 시작 [난이도]\n" +
		"  새 게임 시작 (난이도: level1~level8)\n" +
		"• %s <수> (예: e2e4)\n" +
		"  SAN/UCI 모두 입력 가능\n" +
		"• %s 기권\n" +
		"  즉시 기권하고 세션 종료\n" +
		"• %s 무르기\n" +
		"  마지막 한 수(플레이어+봇) 되돌리기\n" +
		"• %s 기록 [n]\n" +
		"  최근 기보 확인 (기본 10개)\n" +
		"• %s 기보 <ID>\n" +
		"  특정 기보 상세 보기\n" +
		"• %s 프로필\n" +
		"  개인 승률·레이팅 확인"
)

// PrefixProvider exposes the Prefix that Kakao messages should use.
type PrefixProvider interface {
	Prefix() string
}

// Formatter renders chess DTOs into Kakao-friendly text blocks.
type Formatter struct {
	prefixProvider PrefixProvider
	catalog        *msgcat.Catalog
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

// SetCatalog sets a per-formatter message catalog (optional).
func (f *Formatter) SetCatalog(cat *msgcat.Catalog) { f.catalog = cat }

// defaultCatalog is used when a formatter doesn't have a catalog assigned.
var defaultCatalog *msgcat.Catalog

// SetCatalog sets the package-level default catalog for all formatters.
func SetCatalog(cat *msgcat.Catalog) { defaultCatalog = cat }

func (f *Formatter) Start(state *chessdto.SessionState, resumed bool) string {
	prefix := f.Prefix()
	if state == nil {
		if resumed {
			return fmt.Sprintf("진행 중인 체스 게임 정보를 불러오지 못했습니다. `%s 현황` 명령으로 다시 확인해주세요.", prefix)
		}
		return fmt.Sprintf("체스 게임을 시작할 수 없습니다. `%s 시작`을 다시 시도해주세요.", prefix)
	}
	// Build profile lines exactly as before for layout preservation
	var ratingLine, recordLine string
	if profile := state.Profile; profile != nil {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("• 레이팅: %d", profile.Rating))
		if delta := state.RatingDelta; delta > 0 {
			b.WriteString(fmt.Sprintf(" (▲%d)", delta))
		} else if delta < 0 {
			b.WriteString(fmt.Sprintf(" (▼%d)", -delta))
		}
		ratingLine = b.String()
		b.Reset()
		b.WriteString(fmt.Sprintf("• 전적: %d승 %d패 %d무 (%d판)", profile.Wins, profile.Losses, profile.Draws, profile.GamesPlayed))
		if profile.PreferredPreset != "" {
			b.WriteString(fmt.Sprintf(" | 선호: %s", formatPreset(profile.PreferredPreset)))
		}
		recordLine = b.String()
	}
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if body, err := cat.Render("formatter.start.body", map[string]any{
		"Resumed":           resumed,
		"Preset":            formatPreset(state.Preset),
		"ProfileRatingLine": ratingLine,
		"ProfileRecordLine": recordLine,
		"Prefix":            prefix,
	}); err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	// Fallback to original logic if catalog rendering fails
	var sb strings.Builder
	if resumed {
		sb.WriteString("♞ 진행 중인 체스 게임을 불러왔습니다.\n")
	} else {
		sb.WriteString("♟️ 체스 게임을 시작했습니다.\n")
	}
	sb.WriteString(fmt.Sprintf("• 난이도: %s\n", formatPreset(state.Preset)))
	if ratingLine != "" {
		sb.WriteString(ratingLine + "\n")
	}
	if recordLine != "" {
		sb.WriteString(recordLine + "\n")
	}
	sb.WriteString("• 플레이어는 백으로 시작합니다.\n\n")
	sb.WriteString("이동 방법: `" + prefix + " <수>`.\n")
	sb.WriteString("무르기 기능: `" + prefix + " 무르기`.\n")
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
	return f.Prefix() + " " + move
}

func (f *Formatter) Move(summary *chessdto.MoveSummary) string {
	if summary == nil || summary.State == nil {
		return ""
	}
	if !summary.Finished {
		return ""
	}
	state := summary.State
	outcomeText := formatOutcome(state.Outcome, state.OutcomeMeta)
	preset := formatPreset(state.Preset)
	var ratingLine, recordLine, gameIDLine string
	if profile := summary.Profile; profile != nil {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("• 현재 레이팅: %d", profile.Rating))
		delta := summary.RatingDelta
		if delta > 0 {
			b.WriteString(fmt.Sprintf(" (▲%d)", delta))
		} else if delta < 0 {
			b.WriteString(fmt.Sprintf(" (▼%d)", -delta))
		} else if profile.GamesPlayed > 0 {
			b.WriteString(" (변동 없음)")
		}
		ratingLine = b.String()
		b.Reset()
		b.WriteString(fmt.Sprintf("• 누적 전적: %d승 %d패 %d무 (%d판)", profile.Wins, profile.Losses, profile.Draws, profile.GamesPlayed))
		recordLine = b.String()
	}
	if summary.GameID > 0 {
		gameIDLine = fmt.Sprintf("기보 ID: #%d", summary.GameID)
	}
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if body, err := cat.Render("formatter.move.body", map[string]any{
		"OutcomeText": outcomeText,
		"Preset":      preset,
		"RatingLine":  ratingLine,
		"RecordLine":  recordLine,
		"GameIDLine":  gameIDLine,
	}); err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	// fallback
	var sb strings.Builder
	sb.WriteString(outcomeText + "\n\n")
	sb.WriteString("• 난이도: " + preset + "\n")
	if ratingLine != "" {
		sb.WriteString(ratingLine + "\n")
	}
	if recordLine != "" {
		sb.WriteString(recordLine + "\n\n")
	} else {
		sb.WriteString("\n")
	}
	if gameIDLine != "" {
		sb.WriteString(gameIDLine + "\n")
	}
	return sb.String()
}

func (f *Formatter) Status(state *chessdto.SessionState) string {
	if state == nil {
		return f.Help()
	}
	prefix := f.Prefix()
	preset := formatPreset(state.Preset)
	recentLine := ""
	if len(state.MovesSAN) > 0 {
		recentLine = "• 최근 " + formatRecentMoves(state.MovesSAN)
	}
	profileInfo := formatProfileSummary(state.Profile, state.RatingDelta)
	// material/captured lines
	var b strings.Builder
	appendMaterialLine(&b, state.Material)
	materialLine := strings.TrimSuffix(b.String(), "\n")
	b.Reset()
	appendCapturedLine(&b, state.Captured)
	capturedLine := strings.TrimSuffix(b.String(), "\n")
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if body, err := cat.Render("formatter.status.body", map[string]any{
		"Preset":       preset,
		"MoveCount":    state.MoveCount,
		"RecentLine":   recentLine,
		"ProfileInfo":  strings.TrimSpace(profileInfo),
		"MaterialLine": strings.TrimSpace(materialLine),
		"CapturedLine": strings.TrimSpace(capturedLine),
		"Prefix":       prefix,
	}); err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	// fallback to original composition
	var sb strings.Builder
	sb.WriteString("♞ 체스 현황\n")
	sb.WriteString("• 난이도 " + preset + "\n")
	sb.WriteString(fmt.Sprintf("• 진행 %d수\n", state.MoveCount))
	if recentLine != "" {
		sb.WriteString(recentLine + "\n")
	}
	if profileInfo != "" {
		sb.WriteString(profileInfo)
	}
	if materialLine != "" {
		sb.WriteString(materialLine + "\n")
	}
	if capturedLine != "" {
		sb.WriteString(capturedLine + "\n")
	}
	sb.WriteString("\n명령: `" + prefix + " <수>` (SAN/UCI)\n기권: `" + prefix + " 기권`\n무르기: `" + prefix + " 무르기`.")
	return sb.String()
}

func (f *Formatter) Resign(state *chessdto.SessionState) string {
	outcome := ""
	profileInfo := ""
	if state != nil {
		outcome = formatOutcome(state.Outcome, state.OutcomeMeta)
		profileInfo = formatProfileSummary(state.Profile, state.RatingDelta)
	}
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if body, err := cat.Render("formatter.resign.body", map[string]any{
		"OutcomeText": outcome,
		"ProfileInfo": strings.TrimSpace(profileInfo),
	}); err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	// fallback
	var sb strings.Builder
	sb.WriteString("🏳️ 기권 처리되었습니다.\n")
	if outcome == "" {
		sb.WriteString("🛑 기권하여 패배로 기록되었습니다.")
	} else {
		sb.WriteString(outcome)
	}
	if strings.TrimSpace(profileInfo) != "" {
		sb.WriteString("\n" + profileInfo)
	}
	return sb.String()
}

func (f *Formatter) Help() string {
	// YAML-only: must come from catalog. No hardcoded fallback.
	prefix := f.Prefix()
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	body, err := cat.Render("help.korean", map[string]string{"Prefix": prefix})
	if err != nil {
		return ""
	}
	return util.ApplySeeMoreWithHeader(body, chessHelpInstruction, chessSeeMoreInstructionFallback, chessSeeMoreInstructionSuffix)
}

func (f *Formatter) History(games []*chessdto.ChessGame) string {
	var sb strings.Builder
	// header from YAML
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	header := chessHistoryInstruction
	if h, err := cat.Render("formatter.history.header", nil); err == nil && strings.TrimSpace(h) != "" {
		header = h
	}
	sb.WriteString(header)
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
	prefix := f.Prefix()
	if ft, err := cat.Render("formatter.history.footer", map[string]string{"Prefix": prefix}); err == nil && strings.TrimSpace(ft) != "" {
		sb.WriteString(ft)
	} else {
		sb.WriteString(fmt.Sprintf("\n자세히 보려면 `%s 기보 <ID>` 명령을 사용하세요.", prefix))
	}

	content := sb.String()
	if strings.TrimSpace(content) == "" {
		return content
	}
	return util.ApplySeeMoreWithHeader(content, header, chessSeeMoreInstructionFallback, chessSeeMoreInstructionSuffix)
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
	header := chessProfileInstruction
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if h, err := cat.Render("formatter.profile.header", nil); err == nil && strings.TrimSpace(h) != "" {
		header = h
	}
	sb.WriteString(header)
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
	sb.WriteString(fmt.Sprintf("\n새 게임: `%s 시작`, 기록: `%s 기록`, 기보 상세: `%s 기보 <ID>`", prefix, prefix, prefix))

	content := sb.String()
	if !strings.HasPrefix(content, chessProfileInstruction) {
		return content
	}
	return util.ApplySeeMoreWithHeader(content, header, chessSeeMoreInstructionFallback, chessSeeMoreInstructionSuffix)
}

func (f *Formatter) PreferredPresetUpdated(profile *chessdto.ChessProfile) string {
	if profile == nil {
		return "선호 난이도를 업데이트하지 못했습니다. 잠시 후 다시 시도해주세요."
	}
	prefix := f.Prefix()
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	body, err := cat.Render("formatter.preferred_updated.body", map[string]any{
		"PreferredPreset": formatPreset(profile.PreferredPreset),
		"ProfileInfo":     strings.TrimSpace(formatProfileSummary(profile, 0)),
		"Prefix":          prefix,
	})
	if err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	// fallback
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ 선호 난이도를 %s로 설정했습니다.\n", formatPreset(profile.PreferredPreset)))
	if info := formatProfileSummary(profile, 0); info != "" {
		sb.WriteString(info)
	}
	sb.WriteString(fmt.Sprintf("새 게임 시작: `%s 시작`", prefix))
	return sb.String()
}

func (f *Formatter) Undo(state *chessdto.SessionState) string {
	if state == nil {
		return "무르기 결과를 불러오지 못했습니다."
	}
	prefix := f.Prefix()
	// profile/material/captured
	profileInfo := strings.TrimSpace(formatProfileSummary(state.Profile, 0))
	var b strings.Builder
	appendMaterialLine(&b, state.Material)
	materialLine := strings.TrimSuffix(b.String(), "\n")
	b.Reset()
	appendCapturedLine(&b, state.Captured)
	capturedLine := strings.TrimSuffix(b.String(), "\n")
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if body, err := cat.Render("formatter.undo.body", map[string]any{
		"Preset":       formatPreset(state.Preset),
		"MoveCount":    state.MoveCount,
		"ProfileInfo":  profileInfo,
		"MaterialLine": strings.TrimSpace(materialLine),
		"CapturedLine": strings.TrimSpace(capturedLine),
		"Prefix":       prefix,
	}); err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	// fallback
	var sb strings.Builder
	sb.WriteString("↩️ 마지막 한 수를 되돌렸습니다.\n")
	sb.WriteString("• 난이도: " + formatPreset(state.Preset) + "\n")
	sb.WriteString(fmt.Sprintf("• 현재 진행 수: %d\n", state.MoveCount))
	if profileInfo != "" {
		sb.WriteString(profileInfo)
	}
	if materialLine != "" {
		sb.WriteString(materialLine + "\n")
	}
	if capturedLine != "" {
		sb.WriteString(capturedLine + "\n")
	}
	sb.WriteString("\n이제 다시 당신의 턴입니다. `" + prefix + " <수>` 형식으로 새 수를 입력하세요.")
	sb.WriteString("\n최근 기록은 `" + prefix + " 기록`으로 확인할 수 있습니다.")
	return sb.String()
}

func (f *Formatter) NoSession() string {
	cat := f.catalog
	if cat == nil {
		cat = defaultCatalog
	}
	if body, err := cat.Render("formatter.no_session.body", map[string]string{"Prefix": f.Prefix()}); err == nil && strings.TrimSpace(body) != "" {
		return body
	}
	return fmt.Sprintf("진행 중인 체스 게임이 없습니다. `%s 시작`으로 새 게임을 시작하세요.", f.Prefix())
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
