package chess

import (
    "context"
    "sort"
    "strings"
    "sync"

    "github.com/kapu/kakao-cheese-bot-go/internal/domain"
)

// memrepo is a development-only in-memory repository implementation used when no DB is configured.
type memrepo struct {
    mu sync.RWMutex

    nextID int64

    gamesByID    map[int64]*domain.ChessGame
    gamesByUser  map[string][]*domain.ChessGame // playerHash -> slice (append, latest last)
    gamesByIndex map[string]*domain.ChessGame   // sessionUUID|playerHash -> game

    profiles map[string]*domain.ChessProfile // playerHash|roomHash -> profile
}

func NewMemoryRepository() Repository {
    return &memrepo{
        gamesByID:    make(map[int64]*domain.ChessGame),
        gamesByUser:  make(map[string][]*domain.ChessGame),
        gamesByIndex: make(map[string]*domain.ChessGame),
        profiles:     make(map[string]*domain.ChessProfile),
    }
}

func (m *memrepo) InsertGame(ctx context.Context, game *domain.ChessGame) (int64, error) {
    if game == nil {
        return 0, ErrDuplicateGame
    }

    key := m.sessionKey(game.SessionUUID, game.PlayerHash)

    m.mu.Lock()
    defer m.mu.Unlock()

    if _, exists := m.gamesByIndex[key]; exists {
        return 0, ErrDuplicateGame
    }

    m.nextID++
    id := m.nextID
    copy := *game
    copy.ID = id

    m.gamesByID[id] = &copy
    m.gamesByIndex[key] = &copy
    m.gamesByUser[game.PlayerHash] = append(m.gamesByUser[game.PlayerHash], &copy)

    return id, nil
}

func (m *memrepo) GetRecentGames(ctx context.Context, playerHash string, limit int) ([]*domain.ChessGame, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    list := m.gamesByUser[playerHash]
    if len(list) == 0 {
        return []*domain.ChessGame{}, nil
    }
    // Sort by EndedAt desc (fallback to ID desc)
    items := append([]*domain.ChessGame(nil), list...)
    sort.Slice(items, func(i, j int) bool {
        if !items[i].EndedAt.Equal(items[j].EndedAt) {
            return items[i].EndedAt.After(items[j].EndedAt)
        }
        return items[i].ID > items[j].ID
    })
    if limit > 0 && len(items) > limit {
        items = items[:limit]
    }
    return items, nil
}

func (m *memrepo) GetGame(ctx context.Context, id int64, playerHash string) (*domain.ChessGame, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    g, ok := m.gamesByID[id]
    if !ok || g == nil {
        return nil, nil
    }
    if g.PlayerHash != playerHash {
        return nil, nil
    }
    copy := *g
    return &copy, nil
}

func (m *memrepo) GetGameBySession(ctx context.Context, sessionUUID string, playerHash string) (*domain.ChessGame, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if g, ok := m.gamesByIndex[m.sessionKey(sessionUUID, playerHash)]; ok && g != nil {
        copy := *g
        return &copy, nil
    }
    return nil, nil
}

func (m *memrepo) GetProfile(ctx context.Context, playerHash string, roomHash string) (*domain.ChessProfile, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if p, ok := m.profiles[m.profileKey(playerHash, roomHash)]; ok && p != nil {
        copy := *p
        return &copy, nil
    }
    return nil, nil
}

func (m *memrepo) UpsertProfile(ctx context.Context, profile *domain.ChessProfile) error {
    if profile == nil {
        return nil
    }
    key := m.profileKey(profile.PlayerHash, profile.RoomHash)
    m.mu.Lock()
    m.profiles[key] = &(*profile)
    m.mu.Unlock()
    return nil
}

func (m *memrepo) sessionKey(sessionUUID, playerHash string) string {
    return strings.TrimSpace(sessionUUID) + "|" + strings.TrimSpace(playerHash)
}

func (m *memrepo) profileKey(playerHash, roomHash string) string {
    return strings.TrimSpace(playerHash) + "|" + strings.TrimSpace(roomHash)
}

