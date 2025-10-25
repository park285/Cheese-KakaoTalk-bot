package irisfast

type Config struct {
	Port              int    `json:"port"`
	PollingSpeed      int    `json:"pollingSpeed"`
	MessageRate       int    `json:"messageRate"`
	WebserverEndpoint string `json:"webserverEndpoint"`
}

type DecryptRequest struct {
	Data string `json:"data"`
}

type DecryptResponse struct {
	Decrypted string `json:"decrypted"`
}

type ReplyRequest struct {
	Type string `json:"type"`
	Room string `json:"room"`
	Data string `json:"data"`
}

type ImageReplyRequest struct {
	Type string `json:"type"`
	Room string `json:"room"`
	Data string `json:"data"`
}

type Message struct {
	Msg    string       `json:"msg"`
	Room   string       `json:"room"`
	Sender *string      `json:"sender,omitempty"`
	JSON   *MessageJSON `json:"json,omitempty"`
}

type MessageJSON struct {
    UserID    string `json:"user_id,omitempty"`
    Message   string `json:"message,omitempty"`
    RoomID    string `json:"room_id,omitempty"`
    ChatID    string `json:"chat_id,omitempty"`
    Type      string `json:"type,omitempty"`
    CreatedAt string `json:"created_at,omitempty"`
}

type WebSocketState string

const (
	WSStateConnecting   WebSocketState = "CONNECTING"
	WSStateConnected    WebSocketState = "CONNECTED"
	WSStateDisconnected WebSocketState = "DISCONNECTED"
	WSStateReconnecting WebSocketState = "RECONNECTING"
	WSStateFailed       WebSocketState = "FAILED"
)

func (s WebSocketState) String() string {
	return string(s)
}
