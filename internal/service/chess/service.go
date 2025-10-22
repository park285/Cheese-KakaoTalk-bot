package chess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	nchess "github.com/corentings/chess/v2"
	"github.com/corentings/chess/v2/opening"
	"github.com/google/uuid"
	corechess "github.com/kapu/kakao-cheese-bot-go/internal/chess"
	"github.com/kapu/kakao-cheese-bot-go/internal/domain"
	"github.com/kapu/kakao-cheese-bot-go/internal/service/cache"
	"go.uber.org/zap"
)

var (
	ErrSessionNotFound   = errors.New("chess session not found")
	ErrSessionInProgress = errors.New("chess session already in progress")
	ErrInvalidMove       = errors.New("invalid chess move")
	ErrGameNotFound      = errors.New("chess game not found")
	ErrProfileNotFound   = errors.New("chess profile not found")
	ErrUndoNotAvailable  = errors.New("no moves available to undo")
	ErrEngineUnavailable = errors.New("chess engine unavailable")
	ErrEngineTimeout     = errors.New("chess engine timeout")
	ErrRoomNotAllowed    = errors.New("chess room not allowed")
)

const (
	defaultPlayerRating             = 1200
	kFactor                         = 24
	profileCacheTTL                 = 6 * time.Hour
	maxHistoryLimit                 = 50
	engineEvaluationFallbackTimeout = 8 * time.Second
	engineEvaluationBuffer          = 2 * time.Second
	playerLabelRuneLimit            = 24
	defaultHUDPlayerLabel           = "Player"
)

var (
	initialPieceCounts = map[nchess.Color]map[nchess.PieceType]int{
		nchess.White: {
			nchess.Pawn:   8,
			nchess.Knight: 2,
			nchess.Bishop: 2,
			nchess.Rook:   2,
			nchess.Queen:  1,
			nchess.King:   1,
		},
		nchess.Black: {
			nchess.Pawn:   8,
			nchess.Knight: 2,
			nchess.Bishop: 2,
			nchess.Rook:   2,
			nchess.Queen:  1,
			nchess.King:   1,
		},
	}
	pieceValues = map[nchess.PieceType]int{
		nchess.Pawn:   1,
		nchess.Knight: 3,
		nchess.Bishop: 3,
		nchess.Rook:   5,
		nchess.Queen:  9,
	}
	materialBase = func() int {
		base := 0
		for pt, count := range initialPieceCounts[nchess.White] {
			base += count * pieceValues[pt]
		}
		return base
	}()
	initialMaterialScore = MaterialScore{White: materialBase, Black: materialBase}
)

type Evaluator interface {
	Evaluate(ctx context.Context, req corechess.EvaluateRequest) (corechess.EvaluateResult, error)
}

type SessionMeta struct {
	SessionID string
	Room      string
	Sender    string
}

type sessionIdentity struct {
	SessionID  string
	RoomHash   string
	PlayerHash string
}

type Config struct {
	DefaultPreset       string
	SessionTTL          time.Duration
	HistoryLimit        int
	AllowedRooms        []string
	DefaultOpeningStyle string
}

type Service struct {
	engine       Evaluator
	cache        *cache.CacheService
	renderer     BoardRenderer
	repo         Repository
	cfg          Config
	allowedRooms map[string]struct{}
	logger       *zap.Logger
}

type sessionPayload struct {
	SessionUUID string    `json:"session_uuid"`
	PlayerHash  string    `json:"player_hash"`
	RoomHash    string    `json:"room_hash"`
	PlayerName  string    `json:"player_name,omitempty"`
	Preset      string    `json:"preset"`
	Moves       []string  `json:"moves"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	AutoAssist  bool      `json:"auto_assist,omitempty"`
}

type SessionState struct {
	SessionUUID   string
	PlayerHash    string
	RoomHash      string
	PlayerName    string
	Preset        string
	Moves         []string
	MovesSAN      []string
	FEN           string
	BoardImage    []byte
	Turn          string
	MoveCount     int
	Outcome       nchess.Outcome
	OutcomeMethod nchess.Method
	StartedAt     time.Time
	UpdatedAt     time.Time
	RatingDelta   int
	Profile       *domain.ChessProfile
	Material      MaterialScore
	Captured      CapturedPieces
	AutoAssist    bool
}

type MoveSummary struct {
	State            *SessionState
	PlayerSAN        string
	PlayerUCI        string
	EngineSAN        string
	EngineUCI        string
	EngineResult     corechess.EvaluateResult
	Finished         bool
	GameID           int64
	Profile          *domain.ChessProfile
	RatingDelta      int
	Material         MaterialScore
	Captured         CapturedPieces
	AssistSuggestion *AssistSuggestion
}

type AssistSuggestion struct {
	MoveUCI      string
	MoveSAN      string
	EvaluationCP int
	Principal    []string
	Duration     time.Duration
}

type MaterialScore struct {
	White int
	Black int
}

func (m MaterialScore) Diff() int {
	return m.White - m.Black
}

func (m MaterialScore) CapturedValue(color nchess.Color) int {
	switch color {
	case nchess.White:
		return materialBase - m.Black
	case nchess.Black:
		return materialBase - m.White
	default:
		return 0
	}
}

type CapturedPieces struct {
	White      map[nchess.PieceType]int
	Black      map[nchess.PieceType]int
	WhiteOrder []nchess.PieceType
	BlackOrder []nchess.PieceType
}

func (c CapturedPieces) IsEmpty() bool {
	return len(c.White) == 0 && len(c.Black) == 0 && len(c.WhiteOrder) == 0 && len(c.BlackOrder) == 0
}

func (c CapturedPieces) Recent(color nchess.Color, limit int) []nchess.PieceType {
	if limit <= 0 {
		return nil
	}
	var order []nchess.PieceType
	switch color {
	case nchess.White:
		order = c.WhiteOrder
	case nchess.Black:
		order = c.BlackOrder
	default:
		return nil
	}
	if len(order) == 0 {
		return nil
	}
	start := len(order) - limit
	if start < 0 {
		start = 0
	}
	subset := order[start:]
	result := make([]nchess.PieceType, len(subset))
	for i := range subset {
		result[i] = subset[len(subset)-1-i]
	}
	return result
}

func InitialMaterialScore() MaterialScore {
	return initialMaterialScore
}

func ecoFromGameMove(game *nchess.Game, moveUCI string) (string, string) {
	if game == nil {
		return "", ""
	}
	g := game
	mvText := strings.ToLower(strings.TrimSpace(moveUCI))
	if mvText != "" {
		clone := game.Clone()
		pos := clone.Position()
		if pos != nil {
			uci := nchess.UCINotation{}
			if mv, err := uci.Decode(pos, mvText); err == nil {
				_ = clone.Move(mv, nil)
				g = clone
			}
		}
	}
	book := opening.NewBookECO()
	if book == nil {
		return "", ""
	}
	if eco := book.Find(g.Moves()); eco != nil {
		return eco.Code(), eco.Title()
	}
	return "", ""
}

func (s *Service) logOpeningLabel(game *nchess.Game, moveUCI, preset, styleKey string, forced bool) {
	if s == nil || s.logger == nil {
		return
	}
	code, title := ecoFromGameMove(game, moveUCI)
	source := "engine"
	if forced {
		source = "opening"
	}
	ply := 0
	if game != nil {
		ply = len(game.Moves()) + 1
	}
	s.logger.Info("chess opening label",
		zap.String("eco_code", code),
		zap.String("eco_title", title),
		zap.String("source", source),
		zap.Bool("forced", forced),
		zap.String("preset", strings.ToLower(strings.TrimSpace(preset))),
		zap.String("style_key", strings.TrimSpace(styleKey)),
		zap.Int("ply", ply),
		zap.String("move_uci", strings.ToLower(strings.TrimSpace(moveUCI))),
	)
}

func applyPresetStyle(styleKey, presetName string) error {
	style := strings.TrimSpace(styleKey)
	if style == "" {
		return nil
	}
	preset := strings.ToLower(strings.TrimSpace(presetName))
	if preset == "" {
		return fmt.Errorf("preset name required to apply opening style")
	}
	if err := corechess.SetPresetOpeningStyle(preset, style); err != nil {
		return fmt.Errorf("apply opening style %q to preset %s: %w", style, preset, err)
	}
	return nil
}

func NewService(engine Evaluator, cacheSvc *cache.CacheService, repo Repository, renderer BoardRenderer, cfg Config, logger *zap.Logger) (*Service, error) {
	if engine == nil {
		return nil, fmt.Errorf("chess engine evaluator is required")
	}
	if cacheSvc == nil {
		return nil, fmt.Errorf("cache service is required")
	}
	if repo == nil {
		return nil, fmt.Errorf("chess repository is required")
	}
	if renderer == nil {
		return nil, fmt.Errorf("board renderer is required")
	}
	if cfg.SessionTTL <= 0 {
		return nil, fmt.Errorf("session TTL must be greater than 0")
	}
	defaultPreset := strings.ToLower(strings.TrimSpace(cfg.DefaultPreset))
	if defaultPreset == "" {
		defaultPreset = "level3"
	}
	if _, err := corechess.GetPreset(defaultPreset); err != nil {
		return nil, fmt.Errorf("default preset validation failed: %w", err)
	}
	styleKey := strings.TrimSpace(cfg.DefaultOpeningStyle)
	if err := applyPresetStyle(styleKey, defaultPreset); err != nil {
		return nil, err
	}
	if cfg.HistoryLimit <= 0 || cfg.HistoryLimit > maxHistoryLimit {
		cfg.HistoryLimit = 10
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	allowedRooms := make(map[string]struct{})
	for _, room := range cfg.AllowedRooms {
		normalized := strings.ToLower(strings.TrimSpace(room))
		if normalized == "" {
			continue
		}
		allowedRooms[normalized] = struct{}{}
	}

	return &Service{
		engine:   engine,
		cache:    cacheSvc,
		renderer: renderer,
		repo:     repo,
		cfg: Config{
			DefaultPreset:       defaultPreset,
			SessionTTL:          cfg.SessionTTL,
			HistoryLimit:        cfg.HistoryLimit,
			AllowedRooms:        append([]string(nil), cfg.AllowedRooms...),
			DefaultOpeningStyle: styleKey,
		},
		allowedRooms: allowedRooms,
		logger:       logger,
	}, nil
}

func (s *Service) StartSession(ctx context.Context, meta SessionMeta, preset string, autoAssist bool) (*SessionState, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}

	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}

	identity := deriveIdentity(meta)

	existingPayload, err := s.loadSession(ctx, identity.SessionID)
	if err != nil {
		return nil, err
	}
	if existingPayload != nil {
		if err := applyPresetStyle(s.cfg.DefaultOpeningStyle, existingPayload.Preset); err != nil {
			return nil, err
		}
		if autoAssist && !existingPayload.AutoAssist {
			existingPayload.AutoAssist = true
			if err := s.saveSession(ctx, identity.SessionID, existingPayload); err != nil && s.logger != nil {
				s.logger.Warn("failed to persist auto assist flag for existing chess session",
					zap.Error(err),
					zap.String("session_id", identity.SessionID),
				)
			}
		}
		game, err := replaySession(existingPayload)
		if err != nil {
			return nil, err
		}
		state := s.stateFromGame(existingPayload, game)
		if profile, profErr := s.fetchProfile(ctx, identity, true); profErr == nil {
			state.Profile = profile
		}
		s.applyPlayerName(state, existingPayload, meta)
		s.attachBoardImage(ctx, state, game.Position(), nil, nil)
		return state, ErrSessionInProgress
	}

	chosenPreset := strings.ToLower(strings.TrimSpace(preset))

	profile, err := s.fetchProfile(ctx, identity, false)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return nil, err
	}

	if chosenPreset == "" {
		if profile != nil && profile.PreferredPreset != "" {
			chosenPreset = profile.PreferredPreset
		} else {
			chosenPreset = s.cfg.DefaultPreset
		}
	}

	if _, err := corechess.GetPreset(chosenPreset); err != nil {
		return nil, fmt.Errorf("preset validation failed: %w", err)
	}
	if err := applyPresetStyle(s.cfg.DefaultOpeningStyle, chosenPreset); err != nil {
		return nil, err
	}

	payload := &sessionPayload{
		SessionUUID: uuid.NewString(),
		PlayerHash:  identity.PlayerHash,
		RoomHash:    identity.RoomHash,
		PlayerName:  normalizeHUDPlayerLabel(meta.Sender),
		Preset:      chosenPreset,
		Moves:       []string{},
		StartedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		AutoAssist:  autoAssist,
	}

	if err := s.saveSession(ctx, identity.SessionID, payload); err != nil {
		return nil, err
	}

	game := nchess.NewGame()
	state := s.stateFromGame(payload, game)
	s.applyPlayerName(state, payload, meta)
	s.attachBoardImage(ctx, state, game.Position(), nil, nil)
	state.Profile = profile
	return state, nil
}

func (s *Service) Status(ctx context.Context, meta SessionMeta) (*SessionState, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}

	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}

	identity := deriveIdentity(meta)
	payload, err := s.loadSession(ctx, identity.SessionID)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, ErrSessionNotFound
	}

	if err := applyPresetStyle(s.cfg.DefaultOpeningStyle, payload.Preset); err != nil {
		return nil, err
	}

	game, err := replaySession(payload)
	if err != nil {
		return nil, err
	}
	state := s.stateFromGame(payload, game)
	profile, err := s.fetchProfile(ctx, identity, true)
	if err == nil {
		state.Profile = profile
	}
	s.applyPlayerName(state, payload, meta)
	s.attachBoardImage(ctx, state, game.Position(), nil, nil)
	return state, nil
}

func (s *Service) Assist(ctx context.Context, meta SessionMeta) (*AssistSuggestion, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}

	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}

	identity := deriveIdentity(meta)
	payload, err := s.loadSession(ctx, identity.SessionID)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, ErrSessionNotFound
	}

	if err := applyPresetStyle(s.cfg.DefaultOpeningStyle, payload.Preset); err != nil {
		return nil, err
	}

	game, err := replaySession(payload)
	if err != nil {
		return nil, err
	}

	return s.computeAssistSuggestion(ctx, payload, game)
}

func (s *Service) computeAssistSuggestion(ctx context.Context, payload *sessionPayload, game *nchess.Game) (*AssistSuggestion, error) {
	if payload == nil {
		return nil, ErrSessionNotFound
	}

	evalTimeout := s.evaluationTimeout("level8")
	evalCtx, cancel := context.WithTimeout(ctx, evalTimeout)
	defer cancel()

	result, err := s.engine.Evaluate(evalCtx, corechess.EvaluateRequest{
		PresetName: "level8",
		FEN:        "startpos",
		Moves:      append([]string(nil), payload.Moves...),
	})
	if err != nil {
		return nil, mapEngineError(err)
	}

	bestMove := strings.ToLower(strings.TrimSpace(result.EngineBestMove))
	if bestMove == "" {
		bestMove = strings.ToLower(strings.TrimSpace(result.Chosen.Move))
	}
	// Server-side logging only: ECO label and forced/source info
	s.logOpeningLabel(game, bestMove, "level8", s.cfg.DefaultOpeningStyle, result.Chosen.Forced)
	if bestMove == "" {
		return nil, ErrEngineUnavailable
	}

	var san string
	pos := game.Position()
	if pos != nil {
		notationUCI := nchess.UCINotation{}
		if mv, decodeErr := notationUCI.Decode(pos, bestMove); decodeErr == nil {
			san = nchess.AlgebraicNotation{}.Encode(pos, mv)
		}
	}

	evalCP := 0
	var principal []string
	for _, cand := range result.Candidates {
		if strings.EqualFold(cand.Move, bestMove) {
			evalCP = cand.EvalCP
			principal = append([]string(nil), cand.Principal...)
			break
		}
	}
	if evalCP == 0 && len(result.Candidates) > 0 {
		evalCP = result.Candidates[0].EvalCP
		if len(principal) == 0 {
			principal = append([]string(nil), result.Candidates[0].Principal...)
		}
	}

	return &AssistSuggestion{
		MoveUCI:      bestMove,
		MoveSAN:      san,
		EvaluationCP: evalCP,
		Principal:    principal,
		Duration:     result.Duration,
	}, nil
}

func (s *Service) populateAutoAssist(ctx context.Context, payload *sessionPayload, game *nchess.Game, summary *MoveSummary) {
	if summary == nil || summary.State == nil || payload == nil || !payload.AutoAssist || summary.Finished {
		return
	}
	pos := game.Position()
	if pos == nil || pos.Turn() != nchess.White {
		return
	}
	suggestion, err := s.computeAssistSuggestion(ctx, payload, game)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("auto assist suggestion failed",
				zap.Error(err),
				zap.String("session_uuid", payload.SessionUUID),
			)
		}
		return
	}
	if suggestion == nil {
		return
	}
	summary.AssistSuggestion = suggestion
	summary.State.AutoAssist = true
}

func (s *Service) Play(ctx context.Context, meta SessionMeta, moveInput string) (*MoveSummary, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}

	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}

	moveText := strings.TrimSpace(moveInput)
	if moveText == "" {
		return nil, ErrInvalidMove
	}

	identity := deriveIdentity(meta)
	payload, err := s.loadSession(ctx, identity.SessionID)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, ErrSessionNotFound
	}

	if err := applyPresetStyle(s.cfg.DefaultOpeningStyle, payload.Preset); err != nil {
		return nil, err
	}

	game, err := replaySession(payload)
	if err != nil {
		return nil, err
	}
	if game.Outcome() != nchess.NoOutcome {
		return nil, fmt.Errorf("game already finished")
	}

	notationSAN := nchess.AlgebraicNotation{}
	notationUCI := nchess.UCINotation{}
	posBeforePlayer := game.Position()
	move, err := notationSAN.Decode(posBeforePlayer, moveText)
	if err != nil {
		move, err = notationUCI.Decode(posBeforePlayer, strings.ToLower(moveText))
		if err != nil {
			return nil, ErrInvalidMove
		}
	}
	if err := game.Move(move, nil); err != nil {
		return nil, ErrInvalidMove
	}

	playerSAN := notationSAN.Encode(posBeforePlayer, move)
	playerUCI := strings.ToLower(notationUCI.Encode(posBeforePlayer, move))
	playerColor := posBeforePlayer.Turn()
	playerMarker := &PlayerMarker{Square: move.S2()}
	payload.Moves = append(payload.Moves, playerUCI)

	payload.UpdatedAt = time.Now()

	if game.Outcome() != nchess.NoOutcome {
		state := s.stateFromGame(payload, game)
		s.applyPlayerName(state, payload, meta)
		s.attachBoardImage(ctx, state, game.Position(), nil, playerMarker)
		summary := &MoveSummary{
			State:     state,
			PlayerSAN: playerSAN,
			PlayerUCI: playerUCI,
			Finished:  true,
			Material:  state.Material,
			Captured:  state.Captured,
		}

		gameID, profile, delta, persistErr := s.persistFinishedGame(ctx, identity, payload, game, corechess.EvaluateResult{})
		if persistErr != nil {
			return nil, persistErr
		}
		summary.GameID = gameID
		summary.Profile = profile
		summary.RatingDelta = delta
		if summary.State != nil {
			summary.State.Profile = profile
			summary.State.RatingDelta = delta
		}

		if err := s.deleteSession(ctx, identity.SessionID); err != nil {
			s.logger.Warn("failed to delete finished chess session", zap.Error(err))
		}
		return summary, nil
	}

	evalTimeout := s.evaluationTimeout(payload.Preset)
	evalCtx, cancel := context.WithTimeout(ctx, evalTimeout)
	defer cancel()

	result, err := s.engine.Evaluate(evalCtx, corechess.EvaluateRequest{
		PresetName: payload.Preset,
		FEN:        "startpos",
		Moves:      payload.Moves,
	})
	if err != nil {
		s.logger.Warn("chess engine evaluation failed",
			zap.Error(err),
			zap.String("session_id", identity.SessionID),
			zap.String("preset", payload.Preset),
			zap.Int("move_count", len(payload.Moves)),
			zap.Duration("timeout", evalTimeout),
		)
		return nil, mapEngineError(err)
	}

	engineMoveText := strings.ToLower(strings.TrimSpace(result.Chosen.Move))
	if engineMoveText == "" {
		state := s.stateFromGame(payload, game)
		s.applyPlayerName(state, payload, meta)
		s.attachBoardImage(ctx, state, game.Position(), nil, playerMarker)
		summary := &MoveSummary{
			State:        state,
			PlayerSAN:    playerSAN,
			PlayerUCI:    playerUCI,
			EngineResult: result,
			Finished:     state.Outcome != nchess.NoOutcome,
			Material:     state.Material,
			Captured:     state.Captured,
		}
		if err := s.finishIfNeeded(ctx, identity, payload, game, summary, result); err != nil {
			return nil, err
		}
		s.populateAutoAssist(ctx, payload, game, summary)
		return summary, nil
	}
	// Server-side logging only: ECO label and forced/source info for chosen engine reply
	s.logOpeningLabel(game, engineMoveText, payload.Preset, s.cfg.DefaultOpeningStyle, result.Chosen.Forced)

	posBeforeEngine := game.Position()
	engineMove, err := notationUCI.Decode(posBeforeEngine, engineMoveText)
	if err != nil {
		return nil, fmt.Errorf("decode engine move: %w", err)
	}
	if err := game.Move(engineMove, nil); err != nil {
		return nil, fmt.Errorf("apply engine move: %w", err)
	}

	engineSAN := notationSAN.Encode(posBeforeEngine, engineMove)
	engineUCI := strings.ToLower(notationUCI.Encode(posBeforeEngine, engineMove))
	payload.Moves = append(payload.Moves, engineUCI)

	payload.UpdatedAt = time.Now()
	state := s.stateFromGame(payload, game)
	s.applyPlayerName(state, payload, meta)
	highlight := &MoveHighlight{
		From: engineMove.S1(),
		To:   engineMove.S2(),
	}
	if playerMarker != nil && !pieceMatchesColor(game.Position(), playerMarker.Square, playerColor) {
		playerMarker = nil
	}
	s.attachBoardImage(ctx, state, game.Position(), highlight, playerMarker)
	summary := &MoveSummary{
		State:        state,
		PlayerSAN:    playerSAN,
		PlayerUCI:    playerUCI,
		EngineSAN:    engineSAN,
		EngineUCI:    engineUCI,
		EngineResult: result,
		Finished:     state.Outcome != nchess.NoOutcome,
		Material:     state.Material,
		Captured:     state.Captured,
	}

	if err := s.finishIfNeeded(ctx, identity, payload, game, summary, result); err != nil {
		return nil, err
	}
	s.populateAutoAssist(ctx, payload, game, summary)

	return summary, nil
}

func mapEngineError(err error) error {
	if err == nil {
		return ErrEngineUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || engineTimeoutMessage(err) {
		return ErrEngineTimeout
	}
	return ErrEngineUnavailable
}

func engineTimeoutMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout")
}

func (s *Service) evaluationTimeout(presetName string) time.Duration {
	preset, err := corechess.GetPreset(presetName)
	if err != nil {
		s.logger.Warn("failed to resolve chess preset for timeout, using fallback",
			zap.String("preset", presetName),
			zap.Error(err),
		)
		return engineEvaluationFallbackTimeout + engineEvaluationBuffer
	}
	timeout := evaluationTimeoutFromPreset(preset) + engineEvaluationBuffer
	if timeout < engineEvaluationFallbackTimeout {
		return engineEvaluationFallbackTimeout
	}
	return timeout
}

func evaluationTimeoutFromPreset(p corechess.DifficultyPreset) time.Duration {
	if p.MoveTimeMillis > 0 {
		ms := p.MoveTimeMillis + 800
		return time.Duration(ms) * time.Millisecond * 2
	}
	if p.DepthCap > 0 {
		base := time.Duration(p.DepthCap) * 200 * time.Millisecond
		if base < 3*time.Second {
			base = 3 * time.Second
		}
		if base > 15*time.Second {
			base = 15 * time.Second
		}
		return base
	}
	return 5 * time.Second
}

func (s *Service) finishIfNeeded(ctx context.Context, identity sessionIdentity, payload *sessionPayload, game *nchess.Game, summary *MoveSummary, result corechess.EvaluateResult) error {
	if summary == nil {
		return fmt.Errorf("move summary is nil")
	}

	if summary.Finished {
		gameID, profile, delta, persistErr := s.persistFinishedGame(ctx, identity, payload, game, result)
		if persistErr != nil {
			return persistErr
		}
		summary.GameID = gameID
		summary.Profile = profile
		summary.RatingDelta = delta
		if summary.State != nil {
			summary.State.Profile = profile
			summary.State.RatingDelta = delta
		}
		if err := s.deleteSession(ctx, identity.SessionID); err != nil {
			s.logger.Warn("failed to delete finished chess session", zap.Error(err))
		}
		return nil
	}

	return s.saveSession(ctx, identity.SessionID, payload)
}

func (s *Service) Resign(ctx context.Context, meta SessionMeta) (*SessionState, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}

	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}

	identity := deriveIdentity(meta)
	payload, err := s.loadSession(ctx, identity.SessionID)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, ErrSessionNotFound
	}
	game, err := replaySession(payload)
	if err != nil {
		return nil, err
	}
	game.Resign(nchess.White)
	payload.UpdatedAt = time.Now()

	state := s.stateFromGame(payload, game)
	s.applyPlayerName(state, payload, meta)
	s.attachBoardImage(ctx, state, game.Position(), nil, nil)
	gameID, profile, delta, persistErr := s.persistFinishedGame(ctx, identity, payload, game, corechess.EvaluateResult{})
	if persistErr != nil {
		return nil, persistErr
	}
	state.Profile = profile
	state.MoveCount = len(payload.Moves)
	state.RatingDelta = delta

	if err := s.deleteSession(ctx, identity.SessionID); err != nil {
		s.logger.Warn("failed to delete chess session after resignation", zap.Error(err))
	}

	if gameID == 0 {
		s.logger.Warn("resigned chess game did not persist with id")
	}
	return state, nil
}

func (s *Service) Undo(ctx context.Context, meta SessionMeta) (*SessionState, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}

	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}

	identity := deriveIdentity(meta)
	payload, err := s.loadSession(ctx, identity.SessionID)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, ErrSessionNotFound
	}
	if len(payload.Moves) < 2 {
		return nil, ErrUndoNotAvailable
	}

	trimmed := len(payload.Moves) - 2
	if trimmed < 0 {
		trimmed = 0
	}
	payload.Moves = append([]string(nil), payload.Moves[:trimmed]...)
	payload.UpdatedAt = time.Now()

	game, err := replaySession(payload)
	if err != nil {
		return nil, err
	}

	state := s.stateFromGame(payload, game)
	profile, profErr := s.fetchProfile(ctx, identity, true)
	if profErr == nil {
		state.Profile = profile
	}
	s.applyPlayerName(state, payload, meta)
	s.attachBoardImage(ctx, state, game.Position(), nil, nil)

	if err := s.saveSession(ctx, identity.SessionID, payload); err != nil {
		return nil, err
	}

	return state, nil
}

func (s *Service) History(ctx context.Context, meta SessionMeta, limit int) ([]*domain.ChessGame, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > s.cfg.HistoryLimit {
		limit = s.cfg.HistoryLimit
	}
	identity := deriveIdentity(meta)
	return s.repo.GetRecentGames(ctx, identity.PlayerHash, limit)
}

func (s *Service) Game(ctx context.Context, meta SessionMeta, id int64) (*domain.ChessGame, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}
	identity := deriveIdentity(meta)
	game, err := s.repo.GetGame(ctx, id, identity.PlayerHash)
	if err != nil {
		return nil, err
	}
	if game == nil {
		return nil, ErrGameNotFound
	}
	return game, nil
}

func (s *Service) Profile(ctx context.Context, meta SessionMeta) (*domain.ChessProfile, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}
	identity := deriveIdentity(meta)
	profile, err := s.fetchProfile(ctx, identity, true)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, ErrProfileNotFound
	}
	return profile, nil
}

func (s *Service) UpdatePreferredPreset(ctx context.Context, meta SessionMeta, preset string) (*domain.ChessProfile, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if err := s.ensureRoomAllowed(meta); err != nil {
		return nil, err
	}
	identity := deriveIdentity(meta)
	target := strings.ToLower(strings.TrimSpace(preset))
	if target == "" {
		return nil, fmt.Errorf("preset must be provided")
	}
	if _, err := corechess.GetPreset(target); err != nil {
		return nil, fmt.Errorf("preset validation failed: %w", err)
	}

	profile, err := s.fetchProfile(ctx, identity, false)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return nil, err
	}
	if profile == nil {
		profile = &domain.ChessProfile{
			PlayerHash: identity.PlayerHash,
			RoomHash:   identity.RoomHash,
			Rating:     defaultPlayerRating,
			CreatedAt:  time.Now(),
		}
	}

	now := time.Now()
	profile.PreferredPreset = target
	profile.LastPreset = target
	profile.LastPlayedAt = now
	profile.UpdatedAt = now

	if err := s.repo.UpsertProfile(ctx, profile); err != nil {
		return nil, err
	}
	s.cacheProfile(ctx, identity, profile)
	return profile, nil
}

func (s *Service) ensureReady() error {
	switch {
	case s.engine == nil:
		return fmt.Errorf("chess engine not configured")
	case s.cache == nil:
		return fmt.Errorf("cache service not configured")
	case s.renderer == nil:
		return fmt.Errorf("board renderer not configured")
	case s.repo == nil:
		return fmt.Errorf("chess repository not configured")
	default:
		return nil
	}
}

func (s *Service) ensureRoomAllowed(meta SessionMeta) error {
	if len(s.allowedRooms) == 0 {
		return nil
	}

	room := strings.ToLower(strings.TrimSpace(meta.Room))
	if room == "" {
		room = "unknown-room"
	}

	if _, ok := s.allowedRooms[room]; ok {
		return nil
	}

	if s.logger != nil {
		s.logger.Info("chess room access denied",
			zap.String("room", room),
			zap.String("sender", strings.TrimSpace(meta.Sender)),
		)
	}

	return ErrRoomNotAllowed
}

func (s *Service) sessionKey(sessionID string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(sessionID)))
	return "chess:sessions:" + hex.EncodeToString(hash[:])
}

func (s *Service) profileCacheKey(identity sessionIdentity) string {
	return "chess:profile:" + identity.PlayerHash + ":" + identity.RoomHash
}

func (s *Service) loadSession(ctx context.Context, sessionID string) (*sessionPayload, error) {
	key := s.sessionKey(sessionID)
	payload := &sessionPayload{}
	if err := s.cache.Get(ctx, key, payload); err != nil {
		return nil, err
	}
	if payload.Preset == "" {
		return nil, nil
	}
	return payload, nil
}

func (s *Service) saveSession(ctx context.Context, sessionID string, payload *sessionPayload) error {
	if payload == nil {
		return fmt.Errorf("cannot save nil chess session payload")
	}
	payload.UpdatedAt = time.Now()
	return s.cache.Set(ctx, s.sessionKey(sessionID), payload, s.cfg.SessionTTL)
}

func (s *Service) deleteSession(ctx context.Context, sessionID string) error {
	return s.cache.Del(ctx, s.sessionKey(sessionID))
}

func replaySession(payload *sessionPayload) (*nchess.Game, error) {
	game := nchess.NewGame()
	notation := nchess.UCINotation{}
	for _, mv := range payload.Moves {
		move, err := notation.Decode(game.Position(), strings.ToLower(strings.TrimSpace(mv)))
		if err != nil {
			return nil, fmt.Errorf("decode move %s: %w", mv, err)
		}
		if err := game.Move(move, nil); err != nil {
			return nil, fmt.Errorf("apply move %s: %w", mv, err)
		}
	}
	return game, nil
}

func (s *Service) stateFromGame(payload *sessionPayload, game *nchess.Game) *SessionState {
	positions := game.Positions()
	moves := game.Moves()
	sanMoves := make([]string, len(moves))
	notation := nchess.AlgebraicNotation{}
	for i, mv := range moves {
		if i < len(positions) {
			sanMoves[i] = notation.Encode(positions[i], mv)
		}
	}

	turn := strings.ToLower(game.Position().Turn().String())

	state := &SessionState{
		SessionUUID:   payload.SessionUUID,
		PlayerHash:    payload.PlayerHash,
		RoomHash:      payload.RoomHash,
		PlayerName:    payload.PlayerName,
		Preset:        payload.Preset,
		Moves:         append([]string(nil), payload.Moves...),
		MovesSAN:      sanMoves,
		FEN:           game.FEN(),
		Turn:          turn,
		MoveCount:     len(moves),
		Outcome:       game.Outcome(),
		OutcomeMethod: game.Method(),
		StartedAt:     payload.StartedAt,
		UpdatedAt:     payload.UpdatedAt,
		AutoAssist:    payload.AutoAssist,
	}
	state.Material, state.Captured = computeMaterial(game)
	return state
}

func normalizeHUDPlayerLabel(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	cleaned = strings.NewReplacer("\r", " ", "\n", " ").Replace(cleaned)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) > playerLabelRuneLimit {
		truncated := strings.TrimSpace(string(runes[:playerLabelRuneLimit]))
		if truncated == "" {
			return ""
		}
		return truncated + "..."
	}
	return cleaned
}

func normalizePresetLabel(preset string, fallback string) string {
	token := strings.ToLower(strings.TrimSpace(preset))
	if token == "" {
		token = strings.ToLower(strings.TrimSpace(fallback))
	}
	if token == "" {
		return "level3"
	}
	return token
}

func (s *Service) applyPlayerName(state *SessionState, payload *sessionPayload, meta SessionMeta) {
	if state == nil {
		return
	}
	label := ""
	if payload != nil {
		label = normalizeHUDPlayerLabel(payload.PlayerName)
	}
	if label == "" {
		label = normalizeHUDPlayerLabel(meta.Sender)
	}
	if label == "" {
		label = defaultHUDPlayerLabel
	}
	state.PlayerName = label
	if payload != nil {
		payload.PlayerName = label
	}
}

func (s *Service) attachBoardImage(ctx context.Context, state *SessionState, position *nchess.Position, highlight *MoveHighlight, player *PlayerMarker) {
	if state == nil || position == nil || s.renderer == nil {
		return
	}
	playerLabel := normalizeHUDPlayerLabel(state.PlayerName)
	if playerLabel == "" {
		playerLabel = defaultHUDPlayerLabel
	}
	presetLabel := normalizePresetLabel(state.Preset, s.cfg.DefaultPreset)
	hudHeader := fmt.Sprintf("%s vs Bot (%s)", playerLabel, presetLabel)
	turnNumber := state.MoveCount/2 + 1
	if turnNumber < 1 {
		turnNumber = 1
	}

	hudTurn := fmt.Sprintf("%d턴", turnNumber)
	switch strings.ToLower(strings.TrimSpace(state.Turn)) {
	case "white":
		hudTurn = fmt.Sprintf("White • %d턴", turnNumber)
	case "black":
		hudTurn = fmt.Sprintf("Black • %d턴", turnNumber)
	}

	opts := RenderOptions{
		Highlight: highlight,
		Player:    player,
		Material:  state.Material,
		Captured:  state.Captured,
		HUDHeader: hudHeader,
		HUDTurn:   hudTurn,
	}
	data, err := s.renderer.RenderPNG(ctx, position.Board(), opts)
	if err != nil {
		s.logger.Warn("failed to render chess board image", zap.Error(err))
		return
	}
	state.BoardImage = data
}

func pieceMatchesColor(position *nchess.Position, square nchess.Square, color nchess.Color) bool {
	if position == nil {
		return false
	}
	board := position.Board()
	if board == nil {
		return false
	}
	piece := board.Piece(square)
	if piece == nchess.NoPiece {
		return false
	}
	return piece.Color() == color
}

func computeMaterial(game *nchess.Game) (MaterialScore, CapturedPieces) {
	captured := CapturedPieces{
		White:      map[nchess.PieceType]int{},
		Black:      map[nchess.PieceType]int{},
		WhiteOrder: make([]nchess.PieceType, 0),
		BlackOrder: make([]nchess.PieceType, 0),
	}

	if game == nil {
		return initialMaterialScore, captured
	}

	position := game.Position()
	if position == nil {
		return initialMaterialScore, captured
	}

	currentTotals := map[nchess.Color]int{
		nchess.White: 0,
		nchess.Black: 0,
	}
	currentCounts := map[nchess.Color]map[nchess.PieceType]int{
		nchess.White: {},
		nchess.Black: {},
	}

	board := position.Board()
	for file := nchess.FileA; file <= nchess.FileH; file++ {
		for rank := nchess.Rank1; rank <= nchess.Rank8; rank++ {
			sq := nchess.NewSquare(file, rank)
			piece := board.Piece(sq)
			if piece == nchess.NoPiece {
				continue
			}
			color := piece.Color()
			pt := piece.Type()
			value := pieceValues[pt]
			if value == 0 {
				continue
			}
			currentTotals[color] += value
			if currentCounts[color] == nil {
				currentCounts[color] = map[nchess.PieceType]int{}
			}
			currentCounts[color][pt]++
		}
	}

	moves := game.Moves()
	positions := game.Positions()
	for i, mv := range moves {
		if i >= len(positions) {
			break
		}
		if !mv.HasTag(nchess.Capture) && !mv.HasTag(nchess.EnPassant) {
			continue
		}
		pos := positions[i]
		captureSquare := mv.S2()
		if mv.HasTag(nchess.EnPassant) {
			file := mv.S2().File()
			rank := mv.S2().Rank()
			if pos.Turn() == nchess.White {
				captureSquare = nchess.NewSquare(file, rank-1)
			} else {
				captureSquare = nchess.NewSquare(file, rank+1)
			}
		}
		capturedPiece := pos.Board().Piece(captureSquare)
		if capturedPiece == nchess.NoPiece {
			continue
		}
		pt := capturedPiece.Type()
		if pt == nchess.NoPieceType || pt == nchess.King {
			continue
		}
		if pos.Turn() == nchess.White {
			captured.White[pt]++
			captured.WhiteOrder = append(captured.WhiteOrder, pt)
		} else {
			captured.Black[pt]++
			captured.BlackOrder = append(captured.BlackOrder, pt)
		}
	}

	for color, initCounts := range initialPieceCounts {
		for pt, initCount := range initCounts {
			if pt == nchess.King {
				continue
			}
			curr := currentCounts[color][pt]
			lost := initCount - curr
			if lost <= 0 {
				continue
			}
			if color == nchess.White {
				captured.Black[pt] = lost
			} else {
				captured.White[pt] = lost
			}
		}
	}

	score := MaterialScore{
		White: currentTotals[nchess.White],
		Black: currentTotals[nchess.Black],
	}

	return score, captured
}

func (s *Service) persistFinishedGame(ctx context.Context, identity sessionIdentity, payload *sessionPayload, game *nchess.Game, engineResult corechess.EvaluateResult) (int64, *domain.ChessProfile, int, error) {
	result := resultFromOutcome(game.Outcome())
	method := methodFromOutcome(game.Method())
	now := time.Now()

	gameRecord := &domain.ChessGame{
		SessionUUID:   payload.SessionUUID,
		PlayerHash:    identity.PlayerHash,
		RoomHash:      identity.RoomHash,
		Preset:        payload.Preset,
		EnginePreset:  engineResult.Preset.Name,
		Result:        result,
		ResultMethod:  method,
		MovesUCI:      append([]string(nil), payload.Moves...),
		MovesSAN:      s.stateFromGame(payload, game).MovesSAN,
		PGN:           game.String(),
		StartedAt:     payload.StartedAt,
		EndedAt:       now,
		Duration:      now.Sub(payload.StartedAt),
		Blunders:      boolToInt(engineResult.Blunder),
		EngineLatency: engineResult.Duration,
	}

	if gameRecord.EnginePreset == "" {
		gameRecord.EnginePreset = payload.Preset
	}

	gameID, err := s.repo.InsertGame(ctx, gameRecord)
	if err != nil {
		if errors.Is(err, ErrDuplicateGame) {
			existing, fetchErr := s.repo.GetGameBySession(ctx, payload.SessionUUID, identity.PlayerHash)
			if fetchErr != nil || existing == nil {
				return 0, nil, 0, err
			}

			profile, profErr := s.fetchProfile(ctx, identity, true)
			if profErr != nil && !errors.Is(profErr, ErrProfileNotFound) {
				return existing.ID, nil, 0, profErr
			}
			return existing.ID, profile, 0, nil
		}
		return 0, nil, 0, err
	}
	gameRecord.ID = gameID

	profile, err := s.fetchProfile(ctx, identity, false)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return gameID, nil, 0, err
	}
	profile, delta := applyGameResult(profile, identity, payload.Preset, game.Outcome(), now)

	if err := s.repo.UpsertProfile(ctx, profile); err != nil {
		return gameID, nil, 0, err
	}
	s.cacheProfile(ctx, identity, profile)

	return gameID, profile, delta, nil
}

func (s *Service) fetchProfile(ctx context.Context, identity sessionIdentity, allowCache bool) (*domain.ChessProfile, error) {
	if !allowCache {
		profile, err := s.repo.GetProfile(ctx, identity.PlayerHash, identity.RoomHash)
		if profile == nil && err == nil {
			return nil, ErrProfileNotFound
		}
		if err != nil {
			return nil, err
		}
		if profile != nil {
			s.cacheProfile(ctx, identity, profile)
		}
		return profile, nil
	}

	key := s.profileCacheKey(identity)
	profile := &domain.ChessProfile{}
	if err := s.cache.Get(ctx, key, profile); err != nil {
		return nil, err
	}
	if profile.PlayerHash != "" {
		return profile, nil
	}

	stored, err := s.repo.GetProfile(ctx, identity.PlayerHash, identity.RoomHash)
	if stored == nil && err == nil {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	if stored != nil {
		s.cacheProfile(ctx, identity, stored)
	}
	return stored, nil
}

func (s *Service) cacheProfile(ctx context.Context, identity sessionIdentity, profile *domain.ChessProfile) {
	if profile == nil {
		return
	}
	if err := s.cache.Set(ctx, s.profileCacheKey(identity), profile, profileCacheTTL); err != nil {
		s.logger.Warn("failed to cache chess profile", zap.Error(err))
	}
}

func deriveIdentity(meta SessionMeta) sessionIdentity {
	sessionID := strings.ToLower(strings.TrimSpace(meta.SessionID))
	room := strings.ToLower(strings.TrimSpace(meta.Room))
	sender := strings.ToLower(strings.TrimSpace(meta.Sender))

	roomHash := hashString(room)
	playerHash := hashString(room + ":" + sender)

	return sessionIdentity{
		SessionID:  sessionID,
		RoomHash:   roomHash,
		PlayerHash: playerHash,
	}
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func resultFromOutcome(outcome nchess.Outcome) string {
	switch outcome {
	case nchess.WhiteWon:
		return "win"
	case nchess.BlackWon:
		return "loss"
	case nchess.Draw:
		return "draw"
	default:
		return "unknown"
	}
}

func methodFromOutcome(method nchess.Method) string {
	return strings.ToLower(method.String())
}

func applyGameResult(profile *domain.ChessProfile, identity sessionIdentity, preset string, outcome nchess.Outcome, endedAt time.Time) (*domain.ChessProfile, int) {
	if profile == nil {
		profile = &domain.ChessProfile{
			PlayerHash: identity.PlayerHash,
			RoomHash:   identity.RoomHash,
			Rating:     defaultPlayerRating,
			CreatedAt:  endedAt,
		}
	}

	prevRating := profile.Rating

	profile.GamesPlayed++
	profile.LastPreset = preset
	profile.LastPlayedAt = endedAt
	profile.UpdatedAt = endedAt

	resultType := ""
	var score float64
	switch outcome {
	case nchess.WhiteWon:
		profile.Wins++
		resultType = "win"
		score = 1.0
	case nchess.BlackWon:
		profile.Losses++
		resultType = "loss"
		score = 0.0
	default:
		profile.Draws++
		resultType = "draw"
		score = 0.5
	}

	if profile.StreakType == resultType {
		profile.Streak++
	} else {
		profile.Streak = 1
		profile.StreakType = resultType
	}

	engineRating := presetApproxRating(preset)
	expected := 1 / (1 + math.Pow(10, float64(engineRating-profile.Rating)/400))
	newRating := float64(profile.Rating) + kFactor*(score-expected)
	profile.Rating = int(math.Round(newRating))

	return profile, profile.Rating - prevRating
}

func presetApproxRating(preset string) int {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "level1":
		return 600
	case "level2":
		return 700
	case "level3":
		return 800
	case "level4":
		return 1000
	case "level5":
		return 1200
	case "level6":
		return 1400
	case "level7":
		return 1650
	case "level8":
		return 1900
	default:
		return 1500
	}
}
