package chessdto

type MaterialScore struct {
	White int
	Black int
}

type CapturedPieces struct {
	White []string
	Black []string
}

type SessionState struct {
	SessionUUID string
	Preset      string
	MovesSAN    []string
	MovesUCI    []string
	FEN         string
	BoardImage  []byte
	MoveCount   int
	Material    MaterialScore
	Captured    CapturedPieces
	AutoAssist  bool
	Profile     *ChessProfile
	RatingDelta int
	Outcome     string
	OutcomeMeta string
	GameID      int64
}
