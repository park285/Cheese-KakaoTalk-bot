package chessdto

type RequestMeta struct {
	SessionID string
	Room      string
	Sender    string
}

type StartSessionRequest struct {
	Meta       RequestMeta
	Preset     string
	AutoAssist bool
}

type StartSessionResponse struct {
	State   *SessionState
	Resumed bool
}

type StatusRequest struct {
	Meta RequestMeta
}

type StatusResponse struct {
	State *SessionState
}

type PlayRequest struct {
	Meta RequestMeta
	Move string
}

type PlayResponse struct {
	Summary *MoveSummary
}

type AssistRequest struct {
	Meta RequestMeta
}

type AssistResponse struct {
	Suggestion *AssistSuggestion
}

type ResignRequest struct {
	Meta RequestMeta
}

type ResignResponse struct {
	State *SessionState
}

type UndoRequest struct {
	Meta RequestMeta
}

type UndoResponse struct {
	State *SessionState
}

type HistoryRequest struct {
	Meta  RequestMeta
	Limit int
}

type HistoryResponse struct {
	Games []*ChessGame
}

type GameRequest struct {
	Meta   RequestMeta
	GameID int64
}

type GameResponse struct {
	Game *ChessGame
}

type ProfileRequest struct {
	Meta RequestMeta
}

type ProfileResponse struct {
	Profile *ChessProfile
}

type UpdatePreferredPresetRequest struct {
	Meta   RequestMeta
	Preset string
}

type UpdatePreferredPresetResponse struct {
	Profile *ChessProfile
}
