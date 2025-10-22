package pvp

import (
    "errors"
    "fmt"
    "sync"
    "sync/atomic"
    "time"
)

var (
    ErrInvalidArgs       = errors.New("invalid arguments")
    ErrSelfChallenge     = errors.New("cannot challenge yourself")
    ErrAlreadyPending    = errors.New("target already has a pending challenge")
    ErrNoPendingForUser  = errors.New("no pending challenge for target user")
)

type Manager struct {
    mu sync.RWMutex
    // targetID -> list of challenges (append-only; last is latest)
    byTarget map[string][]*Challenge
    seq      uint64
}

func NewManager() *Manager {
    return &Manager{byTarget: make(map[string][]*Challenge)}
}

func (m *Manager) CreateChallenge(originRoom, challengerID, targetID string, color ColorChoice, timeControl string) (*Challenge, error) {
    if originRoom == "" || challengerID == "" || targetID == "" {
        return nil, ErrInvalidArgs
    }
    if challengerID == targetID {
        return nil, ErrSelfChallenge
    }

    m.mu.Lock()
    defer m.mu.Unlock()

    list := m.byTarget[targetID]
    // deny if there's a pending challenge for the target already
    if idx := latestPendingIndex(list); idx >= 0 {
        return nil, ErrAlreadyPending
    }
    ch := &Challenge{
        ID:           m.nextID(),
        OriginRoom:   originRoom,
        ChallengerID: challengerID,
        TargetID:     targetID,
        Color:        color,
        TimeControl:  timeControl,
        CreatedAt:    time.Now(),
        Status:       StatusAccepted,
    }
    // Auto-accept: mark resolved in origin room
    ch.ResolveRoom = originRoom
    m.byTarget[targetID] = append(list, ch)
    return ch, nil
}

func (m *Manager) Accept(targetID, acceptRoom string) (*Challenge, error) {
    if targetID == "" {
        return nil, ErrInvalidArgs
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    list := m.byTarget[targetID]
    if idx := latestPendingIndex(list); idx >= 0 {
        ch := list[idx]
        ch.Status = StatusAccepted
        ch.ResolveRoom = acceptRoom
        return ch, nil
    }
    return nil, ErrNoPendingForUser
}

func (m *Manager) Decline(targetID, declineRoom string) (*Challenge, error) {
    if targetID == "" {
        return nil, ErrInvalidArgs
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    list := m.byTarget[targetID]
    if idx := latestPendingIndex(list); idx >= 0 {
        ch := list[idx]
        ch.Status = StatusDeclined
        ch.ResolveRoom = declineRoom
        return ch, nil
    }
    return nil, ErrNoPendingForUser
}

func latestPendingIndex(list []*Challenge) int {
    for i := len(list) - 1; i >= 0; i-- {
        if list[i].Status == StatusPending {
            return i
        }
    }
    return -1
}

func (m *Manager) nextID() string {
    n := atomic.AddUint64(&m.seq, 1)
    return fmt.Sprintf("ch-%d-%d", time.Now().UnixNano(), n)
}
