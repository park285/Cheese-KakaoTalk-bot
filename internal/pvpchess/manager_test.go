package pvpchess

import (
    "context"
    "fmt"
    "testing"

    miniredis "github.com/alicebob/miniredis/v2"
)

func newTestManager(t *testing.T) *Manager {
    t.Helper()
    mr, err := miniredis.Run()
    if err != nil { t.Fatalf("miniredis: %v", err) }
    t.Cleanup(func() { mr.Close() })
    url := fmt.Sprintf("redis://%s/0", mr.Addr())
    m, err := NewManager(url)
    if err != nil { t.Fatalf("pvpchess.NewManager: %v", err) }
    return m
}


// 수동 무승부 기능 제거됨: 관련 테스트 삭제

// 중단 기능 제거됨(테스트 삭제)

func TestPlayMove_UCI_SAN_Illegal(t *testing.T) {
    m := newTestManager(t)
    ctx := context.Background()
    g, err := m.CreateGameFromChallenge(ctx, "roomA", "roomB", "u1", "u1", "u2", "u2", "white", "none")
    if err != nil { t.Fatalf("CreateGameFromChallenge: %v", err) }

    // UCI move by white
    g1, txt, err := m.PlayMove(ctx, g.WhiteID, "e2e4")
    if err != nil || g1 == nil { t.Fatalf("PlayMove UCI: %v", err) }
    if txt == "" || len(g1.MovesUCI) != 1 { t.Fatalf("unexpected UCI result: txt=%q len=%d", txt, len(g1.MovesUCI)) }

    // SAN move by black (e.g., Nc6)
    g2, txt2, err := m.PlayMove(ctx, g.BlackID, "Nc6")
    if err != nil || g2 == nil { t.Fatalf("PlayMove SAN: %v", err) }
    if txt2 == "" || len(g2.MovesSAN) != 2 { t.Fatalf("unexpected SAN result: txt=%q lenSAN=%d", txt2, len(g2.MovesSAN)) }

    // Illegal move should return an explanatory message
    _, txt3, err := m.PlayMove(ctx, g.WhiteID, "invalid")
    if err != nil { t.Fatalf("PlayMove illegal returned error: %v", err) }
    if txt3 == "" {
        t.Fatalf("expected user-facing error message for illegal move")
    }
}
