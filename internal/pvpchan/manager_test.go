package pvpchan

import (
    "context"
    "fmt"
    "testing"

    miniredis "github.com/alicebob/miniredis/v2"
    "github.com/redis/go-redis/v9"
    pvpchess "github.com/park285/Cheese-KakaoTalk-bot/internal/pvpchess"
)

func newTestManagers(t *testing.T) (*Manager, *pvpchess.Manager, func()) {
    t.Helper()
    mr, err := miniredis.Run()
    if err != nil { t.Fatalf("miniredis: %v", err) }
    cleanup := func() { mr.Close() }

    // PvP chess manager shares same Redis
    url := fmt.Sprintf("redis://%s/0", mr.Addr())
    chessMgr, err := pvpchess.NewManager(url)
    if err != nil { t.Fatalf("pvpchess.NewManager: %v", err) }

    // Channel manager uses go-redis to same server
    rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
    chanMgr := NewManager(rdb, chessMgr)
    return chanMgr, chessMgr, cleanup
}

func TestMakeJoinStartsGame(t *testing.T) {
    m, chessMgr, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    mr, err := m.Make(ctx, "roomA", "u1", "u1", ColorRandom)
    if err != nil { t.Fatalf("Make: %v", err) }
    if mr.Code == "" { t.Fatalf("expected non-empty code") }

    jr, err := m.Join(ctx, "roomB", mr.Code, "u2", "u2", ColorRandom)
    if err != nil { t.Fatalf("Join: %v", err) }
    if !jr.Started || jr.Meta.GameID == "" { t.Fatalf("expected game to start on second join: started=%v game=%q", jr.Started, jr.Meta.GameID) }

    // Verify chess manager sees the active game
    g, err := chessMgr.GetActiveGameByUser(ctx, "u1")
    if err != nil || g == nil { t.Fatalf("GetActiveGameByUser: %v", err) }
    if g.ID != jr.Meta.GameID { t.Fatalf("gameID mismatch: %q vs %q", g.ID, jr.Meta.GameID) }

    rooms, err := m.Rooms(ctx, mr.Code)
    if err != nil { t.Fatalf("Rooms: %v", err) }
    if len(rooms) != 2 { t.Fatalf("expected 2 rooms, got %d (%v)", len(rooms), rooms) }
}

func TestThirdJoinRejected(t *testing.T) {
    m, _, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    mr, err := m.Make(ctx, "roomA", "u1", "u1", ColorRandom)
    if err != nil { t.Fatalf("Make: %v", err) }
    if _, err := m.Join(ctx, "roomB", mr.Code, "u2", "u2", ColorRandom); err != nil { t.Fatalf("Join#1: %v", err) }
    // Third join should fail
    if _, err := m.Join(ctx, "roomC", mr.Code, "u3", "u3", ColorRandom); err == nil {
        t.Fatalf("expected error on third join")
    }
}

func TestRoomsByUserAndGame(t *testing.T) {
    m, chessMgr, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    mr, err := m.Make(ctx, "roomA", "u1", "u1", ColorRandom)
    if err != nil { t.Fatalf("Make: %v", err) }
    jr, err := m.Join(ctx, "roomB", mr.Code, "u2", "u2", ColorRandom)
    if err != nil { t.Fatalf("Join: %v", err) }
    if !jr.Started { t.Fatalf("game not started") }

    g, err := chessMgr.GetActiveGameByUser(ctx, "u2")
    if err != nil || g == nil { t.Fatalf("GetActiveGameByUser: %v", err) }
    rooms, err := m.RoomsByUserAndGame(ctx, "u2", g.ID)
    if err != nil { t.Fatalf("RoomsByUserAndGame: %v", err) }
    if len(rooms) != 2 { t.Fatalf("expected 2 rooms, got %d (%v)", len(rooms), rooms) }
}

func TestJoinUsesProvidedUserName(t *testing.T) {
    m, chessMgr, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    mr, err := m.Make(ctx, "roomA", "u1", "Alice", ColorRandom)
    if err != nil { t.Fatalf("Make: %v", err) }
    jr, err := m.Join(ctx, "roomB", mr.Code, "u2", "Bob", ColorRandom)
    if err != nil { t.Fatalf("Join: %v", err) }
    if !jr.Started { t.Fatalf("game not started") }

    g, err := chessMgr.GetActiveGameByUser(ctx, "u2")
    if err != nil || g == nil { t.Fatalf("GetActiveGameByUser: %v", err) }
    // Expect names to be reflected
    names := []string{g.WhiteName, g.BlackName}
    foundAlice, foundBob := false, false
    for _, n := range names {
        if n == "Alice" { foundAlice = true }
        if n == "Bob" { foundBob = true }
    }
    if !foundAlice || !foundBob {
        t.Fatalf("expected names Alice and Bob in game participants, got: %v vs %v", g.WhiteName, g.BlackName)
    }
}

func TestMakeBlockedIfActiveGameInSameRoom(t *testing.T) {
    m, chessMgr, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    // Create an active game for u1 in roomA
    if _, err := chessMgr.CreateGameFromChallenge(ctx, "roomA", "roomB", "u1", "u1", "u2", "u2", "random", "none"); err != nil {
        t.Fatalf("CreateGameFromChallenge: %v", err)
    }
    // Try to create a new channel in the same room for u1 → should be blocked
    if _, err := m.Make(ctx, "roomA", "u1", "u1", ColorRandom); err == nil {
        t.Fatalf("expected ErrPlayerBusyInRoom on Make when user has active game in same room")
    }
}

func TestJoinBlockedIfUserActiveInSameRoom(t *testing.T) {
    m, chessMgr, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    // Pre-create an active game for u2 in roomB
    if _, err := chessMgr.CreateGameFromChallenge(ctx, "roomX", "roomB", "x1", "x1", "u2", "u2", "random", "none"); err != nil {
        t.Fatalf("CreateGameFromChallenge: %v", err)
    }

    // Make a lobby by u1 in roomA
    mr, err := m.Make(ctx, "roomA", "u1", "u1", ColorRandom)
    if err != nil { t.Fatalf("Make: %v", err) }

    // Attempt to join from roomB as u2 → should be blocked due to active game in roomB
    if _, err := m.Join(ctx, "roomB", mr.Code, "u2", "u2", ColorRandom); err == nil {
        t.Fatalf("expected ErrPlayerBusyInRoom on Join when user has active game in same room")
    }
}

func TestMakeRestrictedDuplicateCreator(t *testing.T) {
    m, _, cleanup := newTestManagers(t)
    defer cleanup()
    ctx := context.Background()

    // first lobby ok
    if _, err := m.Make(ctx, "roomA", "u1", "u1", ColorRandom); err != nil {
        t.Fatalf("first Make: %v", err)
    }
    // second lobby by same user should be rejected while first is LOBBY
    if _, err := m.Make(ctx, "roomB", "u1", "u1", ColorRandom); err == nil {
        t.Fatalf("expected ErrCreatorHasLobby on duplicate creator lobby")
    }
}
