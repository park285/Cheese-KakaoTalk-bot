package pvpchan

import (
    "context"
    "crypto/rand"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "github.com/redis/go-redis/v9"
)

const (
    ttlChannel = 24 * time.Hour
)

type Store struct{ rdb *redis.Client }

func NewStore(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

func (s *Store) keyMeta(code string) string         { return "ch:" + strings.TrimSpace(code) }
func (s *Store) keyRooms(code string) string        { return s.keyMeta(code) + ":rooms" }
func (s *Store) keyParticipants(code string) string { return s.keyMeta(code) + ":participants" }
func (s *Store) keyUserIdx(user string) string      { return "ch:index:user:" + strings.TrimSpace(user) }
func (s *Store) keyLobby() string                  { return "ch:lobby" }

func (s *Store) SaveMeta(ctx context.Context, code string, meta *ChannelMeta) error {
    raw, err := json.Marshal(meta)
    if err != nil { return err }
    if err := s.rdb.Set(ctx, s.keyMeta(code), raw, ttlChannel).Err(); err != nil { return err }
    // ensure TTL on companions
    _ = s.rdb.Expire(ctx, s.keyRooms(code), ttlChannel).Err()
    _ = s.rdb.Expire(ctx, s.keyParticipants(code), ttlChannel).Err()
    return nil
}

func (s *Store) LoadMeta(ctx context.Context, code string) (*ChannelMeta, error) {
    raw, err := s.rdb.Get(ctx, s.keyMeta(code)).Bytes()
    if err == redis.Nil { return nil, nil }
    if err != nil { return nil, err }
    var m ChannelMeta
    if err := json.Unmarshal(raw, &m); err != nil { return nil, err }
    return &m, nil
}

func (s *Store) AddRoom(ctx context.Context, code, room string) error {
    if strings.TrimSpace(room) == "" { return nil }
    if err := s.rdb.SAdd(ctx, s.keyRooms(code), room).Err(); err != nil { return err }
    return s.rdb.Expire(ctx, s.keyRooms(code), ttlChannel).Err()
}

func (s *Store) Rooms(ctx context.Context, code string) ([]string, error) {
    return s.rdb.SMembers(ctx, s.keyRooms(code)).Result()
}

func (s *Store) ParticipantCount(ctx context.Context, code string) (int64, error) {
    return s.rdb.SCard(ctx, s.keyParticipants(code)).Result()
}

func (s *Store) AddParticipant(ctx context.Context, code, userID string) error {
    if strings.TrimSpace(userID) == "" { return nil }
    if err := s.rdb.SAdd(ctx, s.keyParticipants(code), userID).Err(); err != nil { return err }
    _ = s.rdb.Expire(ctx, s.keyParticipants(code), ttlChannel).Err()
    // index by user → codes
    if err := s.rdb.SAdd(ctx, s.keyUserIdx(userID), code).Err(); err != nil { return err }
    return s.rdb.Expire(ctx, s.keyUserIdx(userID), ttlChannel).Err()
}

func (s *Store) CodesByUser(ctx context.Context, userID string) ([]string, error) {
    return s.rdb.SMembers(ctx, s.keyUserIdx(userID)).Result()
}

// 무승부 제안/수락 기능 제거: 관련 스토어 키 제거됨

// codeGen returns `CH-` + 6 upper alnum.
func codeGen() (string, error) {
    const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    b := make([]byte, 6)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    for i := range b {
        b[i] = letters[int(b[i])%len(letters)]
    }
    return fmt.Sprintf("CH-%s", string(b)), nil
}

// Lobby index helpers
func (s *Store) AddLobby(ctx context.Context, code string) error {
    if strings.TrimSpace(code) == "" { return nil }
    if err := s.rdb.SAdd(ctx, s.keyLobby(), code).Err(); err != nil { return err }
    // refresh TTL of the lobby index
    _ = s.rdb.Expire(ctx, s.keyLobby(), ttlChannel).Err()
    return nil
}

func (s *Store) RemoveLobby(ctx context.Context, code string) error {
    if strings.TrimSpace(code) == "" { return nil }
    return s.rdb.SRem(ctx, s.keyLobby(), code).Err()
}

func (s *Store) ListLobby(ctx context.Context) ([]*ChannelMeta, error) {
    codes, err := s.rdb.SMembers(ctx, s.keyLobby()).Result()
    if err != nil { return nil, err }
    var out []*ChannelMeta
    for _, c := range codes {
        m, _ := s.LoadMeta(ctx, c)
        if m == nil { continue }
        if m.State != StateLobby { continue }
        out = append(out, m)
    }
    return out, nil
}
