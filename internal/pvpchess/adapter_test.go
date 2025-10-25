package pvpchess

import (
    "context"
    "testing"
    miniredis "github.com/alicebob/miniredis/v2"
)

func TestToDTOForViewer_FlipDifferent(t *testing.T) {
    mr, err := miniredis.Run()
    if err != nil { t.Fatalf("miniredis: %v", err) }
    defer mr.Close()
    url := "redis://" + mr.Addr() + "/0"
    m, err := NewManager(url)
    if err != nil { t.Fatalf("NewManager: %v", err) }

    g := &Game{ID: "g1", FEN: "startpos", MovesUCI: []string{"e2e4"}, WhiteID: "w", BlackID: "b", WhiteName: "W", BlackName: "B"}
    ctx := context.Background()
    dtoW, err := m.ToDTOForViewer(ctx, g, "w")
    if err != nil || dtoW == nil || len(dtoW.BoardImage) == 0 { t.Fatalf("white dto render failed: %v", err) }
    dtoB, err := m.ToDTOForViewer(ctx, g, "b")
    if err != nil || dtoB == nil || len(dtoB.BoardImage) == 0 { t.Fatalf("black dto render failed: %v", err) }
    if string(dtoW.BoardImage) == string(dtoB.BoardImage) {
        t.Fatalf("expected different images for flipped viewpoints")
    }
}

