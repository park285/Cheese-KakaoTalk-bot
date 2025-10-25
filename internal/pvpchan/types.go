package pvpchan

import "time"

// ChannelState represents the lifecycle of a PvP channel.
type ChannelState string

const (
    StateLobby    ChannelState = "LOBBY"
    StateActive   ChannelState = "ACTIVE"
    StateFinished ChannelState = "FINISHED"
    StateAborted  ChannelState = "ABORTED"
)

// ColorChoice is a textual color preference for join/make.
type ColorChoice string

const (
    ColorWhite  ColorChoice = "white"
    ColorBlack  ColorChoice = "black"
    ColorRandom ColorChoice = "random"
)

// ChannelMeta is stored as JSON in Redis under ch:<code>.
type ChannelMeta struct {
    ID          string       `json:"id"`
    State       ChannelState `json:"state"`
    CreatedAt   time.Time    `json:"created_at"`

    CreatorID   string `json:"creator_id"`
    CreatorName string `json:"creator_name"`
    CreatorRoom string `json:"creator_room"`

    WhiteID     string `json:"white_id,omitempty"`
    WhiteName   string `json:"white_name,omitempty"`
    BlackID     string `json:"black_id,omitempty"`
    BlackName   string `json:"black_name,omitempty"`

    GameID      string `json:"game_id,omitempty"`
}

// Results
type MakeResult struct {
    Code string
    Meta *ChannelMeta
}

type JoinResult struct {
    Started bool
    GameID  string
    Meta    *ChannelMeta
}

// Errors
var (
    ErrInvalidArgs   = errf("invalid arguments")
    ErrChannelGone   = errf("channel not found or expired")
    ErrChannelActive = errf("channel already active")
    ErrFull          = errf("channel already has two participants")
    // 플레이어가 동일 방에서 이미 진행 중인 대국이 있는 경우
    ErrPlayerBusyInRoom = errf("player has active game in this room")
    // 동일 사용자가 동시에 2개 이상 대기방 생성 불가
    ErrCreatorHasLobby = errf("user already has a lobby")
)

type staticErr string
func (e staticErr) Error() string { return string(e) }
func errf(s string) error { return staticErr(s) }
