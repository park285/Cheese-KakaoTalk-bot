package pvpchess

import (
    "context"
    "fmt"
    "strings"
    "testing"

    miniredis "github.com/alicebob/miniredis/v2"
    "github.com/redis/go-redis/v9"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(func() { mr.Close() })
	url := fmt.Sprintf("redis://%s/0", mr.Addr())
	m, err := NewManager(url)
	if err != nil {
		t.Fatalf("pvpchess.NewManager: %v", err)
	}
	return m
}

func TestPlayMove_UCI_SAN_Illegal(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	g, err := m.CreateGameFromChallenge(ctx, "roomA", "roomB", "u1", "u1", "u2", "u2", "white", "none")
	if err != nil {
		t.Fatalf("CreateGameFromChallenge: %v", err)
	}

	// UCI move by white
	g1, txt, err := m.PlayMove(ctx, g.WhiteID, "e2e4")
	if err != nil || g1 == nil {
		t.Fatalf("PlayMove UCI: %v", err)
	}
	if txt == "" || len(g1.MovesUCI) != 1 {
		t.Fatalf("unexpected UCI result: txt=%q len=%d", txt, len(g1.MovesUCI))
	}

	// SAN move by black (e.g., Nc6)
	g2, txt2, err := m.PlayMove(ctx, g.BlackID, "Nc6")
	if err != nil || g2 == nil {
		t.Fatalf("PlayMove SAN: %v", err)
	}
	if txt2 == "" || len(g2.MovesSAN) != 2 {
		t.Fatalf("unexpected SAN result: txt=%q lenSAN=%d", txt2, len(g2.MovesSAN))
	}

	// Illegal move should return an explanatory message
	_, txt3, err := m.PlayMove(ctx, g.WhiteID, "invalid")
	if err != nil {
		t.Fatalf("PlayMove illegal returned error: %v", err)
	}
	if txt3 == "" {
		t.Fatalf("expected user-facing error message for illegal move")
	}
}

func TestIndexTTLOnCreate(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	url := fmt.Sprintf("redis://%s/0", mr.Addr())
	m, err := NewManager(url)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ctx := context.Background()
	g, err := m.CreateGameFromChallenge(ctx, "roomA", "roomB", "w1", "WName", "b1", "BName", "random", "none")
	if err != nil || g == nil {
		t.Fatalf("CreateGameFromChallenge: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ttlW, err := rdb.TTL(ctx, idxUserKey("w1")).Result()
	if err != nil {
		t.Fatalf("TTL white: %v", err)
	}
	ttlB, err := rdb.TTL(ctx, idxUserKey("b1")).Result()
	if err != nil {
		t.Fatalf("TTL black: %v", err)
	}
	if ttlW <= 0 || ttlB <= 0 {
		t.Fatalf("expected positive TTLs for user index keys, got w=%v b=%v", ttlW, ttlB)
	}
}

func TestPlayMoveByRoom_ScopedSelection(t *testing.T) {
    m := newTestManager(t)
    ctx := context.Background()

    // Same user (u1) participates in two games across different rooms
    g1, err := m.CreateGameFromChallenge(ctx, "roomA", "roomB", "u1", "U1", "v1", "V1", "white", "none")
    if err != nil { t.Fatalf("create g1: %v", err) }
    g2, err := m.CreateGameFromChallenge(ctx, "roomC", "roomD", "u1", "U1", "v2", "V2", "black", "none")
    if err != nil { t.Fatalf("create g2: %v", err) }

    // Move in roomA should only affect g1
    before1 := len(g1.MovesUCI)
    before2 := len(g2.MovesUCI)
    gg1, txt, err := m.PlayMoveByRoom(ctx, "u1", "roomA", "e2e4")
    if err != nil || gg1 == nil { t.Fatalf("PlayMoveByRoom: %v", err) }
    if len(gg1.MovesUCI) != before1+1 { t.Fatalf("g1 not advanced: len=%d", len(gg1.MovesUCI)) }
    // ensure g2 remains unchanged
    g2r, _ := m.get(ctx, g2.ID)
    if g2r == nil || len(g2r.MovesUCI) != before2 { t.Fatalf("g2 changed unexpectedly: len=%d", len(g2r.MovesUCI)) }
    if strings.TrimSpace(txt) == "" { t.Fatalf("expected result text for applied move") }
}

func TestPlayMoveByRoom_NotYourTurn(t *testing.T) {
    m := newTestManager(t)
    ctx := context.Background()
    _, err := m.CreateGameFromChallenge(ctx, "r1", "r2", "w", "W", "b", "B", "white", "none")
    if err != nil { t.Fatalf("create: %v", err) }
    // Black tries to move first in r1
    gg, txt, err := m.PlayMoveByRoom(ctx, "b", "r1", "e7e5")
    if err != nil { t.Fatalf("unexpected error: %v", err) }
    if gg == nil { t.Fatalf("game nil") }
    if strings.TrimSpace(txt) == "" { t.Fatalf("expected not-your-turn message") }
    if len(gg.MovesUCI) != 0 { t.Fatalf("move should not be applied") }
}
