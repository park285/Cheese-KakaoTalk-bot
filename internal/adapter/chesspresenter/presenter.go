package chesspresenter

import (
	"encoding/base64"
	"strings"

	"github.com/park285/Cheese-KakaoTalk-bot/pkg/chessdto"
)

// Presenter delivers formatted messages and board images without coupling to the command layer.
type Presenter struct {
	sendMessage func(room, message string) error
	sendImage   func(room, imageBase64 string) error
}

func NewPresenter(sendMessage func(room, message string) error, sendImage func(room, imageBase64 string) error) *Presenter {
	return &Presenter{
		sendMessage: sendMessage,
		sendImage:   sendImage,
	}
}

func (p *Presenter) Board(room, message string, state *chessdto.SessionState) error {
	if p == nil {
		return nil
	}

	if text := strings.TrimSpace(message); text != "" && p.sendMessage != nil {
		if err := p.sendMessage(room, message); err != nil {
			return err
		}
	}

	if state != nil && len(state.BoardImage) > 0 && p.sendImage != nil {
		encoded := base64.StdEncoding.EncodeToString(state.BoardImage)
		if err := p.sendImage(room, encoded); err != nil {
			return err
		}
	}

	return nil
}
