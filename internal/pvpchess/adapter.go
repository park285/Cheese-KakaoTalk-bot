package pvpchess

import (
	"context"
	"fmt"
	"strings"

	nchess "github.com/corentings/chess/v2"
	svcchess "github.com/park285/Cheese-KakaoTalk-bot/internal/service/chess"
	"github.com/park285/Cheese-KakaoTalk-bot/pkg/chessdto"
)

// ToDTO renders PNG using the shared chess renderer and returns a DTO SessionState for presenter.Board.
func (m *Manager) ToDTO(ctx context.Context, g *Game) (*chessdto.SessionState, error) {
	if m == nil || g == nil {
		return nil, nil
	}
	game := reconstruct(g.FEN, g.MovesUCI)
	if game == nil {
		return nil, fmt.Errorf("reconstruct failed")
	}
	pos := game.Position()
	opts := svcchess.RenderOptions{
		HUDHeader: fmt.Sprintf("%s vs %s", g.WhiteName, g.BlackName),
		HUDTurn:   hudTurn(game),
		Highlight: lastHighlight(game),
	}
	png, err := m.renderer.RenderPNG(ctx, pos.Board(), opts)
	if err != nil {
		return nil, err
	}
	state := &chessdto.SessionState{
		SessionUUID: g.ID,
		MovesUCI:    append([]string(nil), g.MovesUCI...),
		MovesSAN:    append([]string(nil), g.MovesSAN...),
		FEN:         g.FEN,
		BoardImage:  png,
		MoveCount:   len(g.MovesUCI),
	}
	return state, nil
}

func (m *Manager) ToDTOForViewer(ctx context.Context, g *Game, viewerID string) (*chessdto.SessionState, error) {
	if m == nil || g == nil {
		return nil, nil
	}
	game := reconstruct(g.FEN, g.MovesUCI)
	if game == nil {
		return nil, fmt.Errorf("reconstruct failed")
	}
	pos := game.Position()
	viewerColor := nchess.White
	if strings.TrimSpace(viewerID) == strings.TrimSpace(g.BlackID) {
		viewerColor = nchess.Black
	}
	myName := g.WhiteName
	oppName := g.BlackName
	if viewerColor == nchess.Black {
		myName = g.BlackName
		oppName = g.WhiteName
	}
	opts := svcchess.RenderOptions{
		HUDHeader:   fmt.Sprintf("%s vs %s", strings.TrimSpace(myName), strings.TrimSpace(oppName)),
		HUDTurn:     hudTurnForViewer(game, viewerColor),
		Highlight:   lastHighlight(game),
		Flip:        viewerColor == nchess.Black,
		ViewerColor: viewerColor,
	}
	png, err := m.renderer.RenderPNG(ctx, pos.Board(), opts)
	if err != nil {
		return nil, err
	}
	state := &chessdto.SessionState{
		SessionUUID: g.ID,
		MovesUCI:    append([]string(nil), g.MovesUCI...),
		MovesSAN:    append([]string(nil), g.MovesSAN...),
		FEN:         g.FEN,
		BoardImage:  png,
		MoveCount:   len(g.MovesUCI),
	}
	return state, nil
}

func hudTurn(game *nchess.Game) string {
	turnNumber := len(game.Moves())/2 + 1
	if turnNumber < 1 {
		turnNumber = 1
	}
	if game.Position().Turn() == nchess.White {
		return fmt.Sprintf("White • %d턴", turnNumber)
	}
	return fmt.Sprintf("Black • %d턴", turnNumber)
}

// hudTurnForViewer는 뷰어의 진영(White/Black)을 고정적으로 표시합니다.
func hudTurnForViewer(game *nchess.Game, viewerColor nchess.Color) string {
	turnNumber := len(game.Moves())/2 + 1
	if turnNumber < 1 {
		turnNumber = 1
	}
	if viewerColor == nchess.Black {
		return fmt.Sprintf("Black • %d턴", turnNumber)
	}
	if viewerColor == nchess.White {
		return fmt.Sprintf("White • %d턴", turnNumber)
	}
	return hudTurn(game)
}

func lastHighlight(game *nchess.Game) *svcchess.MoveHighlight {
	moves := game.Moves()
	if len(moves) == 0 {
		return nil
	}
	mv := moves[len(moves)-1]
	return &svcchess.MoveHighlight{From: mv.S1(), To: mv.S2()}
}
