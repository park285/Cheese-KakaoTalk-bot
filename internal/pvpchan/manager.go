package pvpchan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/obslog"
	"github.com/park285/Cheese-KakaoTalk-bot/internal/pvpchess"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Manager struct {
	rdb   *redis.Client
	store *Store
	pvp   *pvpchess.Manager
}

func NewManager(rdb *redis.Client, pvp *pvpchess.Manager) *Manager {
	return &Manager{rdb: rdb, store: NewStore(rdb), pvp: pvp}
}

func (m *Manager) Make(ctx context.Context, room, userID, userName string, color ColorChoice) (*MakeResult, error) {
    if strings.TrimSpace(room) == "" || strings.TrimSpace(userID) == "" {
        return nil, ErrInvalidArgs
    }
    // 동시성: 플레이어가 동일 방에서 이미 진행 중인 대국이 있으면 채널 생성 금지
    if g, _ := m.pvp.GetActiveGameByUserInRoom(ctx, userID, room); g != nil {
        return nil, ErrPlayerBusyInRoom
    }
    // 동일 사용자의 중복 대기방 생성 금지: 사용자 인덱스의 코드 중 LOBBY 상태가 있는지 검사
    if codes, err := m.store.CodesByUser(ctx, userID); err == nil {
        for _, c := range codes {
            if meta, _ := m.store.LoadMeta(ctx, c); meta != nil {
                if meta.State == StateLobby && strings.TrimSpace(meta.CreatorID) == strings.TrimSpace(userID) {
                    return nil, ErrCreatorHasLobby
                }
            }
        }
    }
	// generate unique code
	var code string
	for i := 0; i < 5; i++ {
		c, err := codeGen()
		if err != nil {
			return nil, err
		}
		meta := &ChannelMeta{
			ID:          c,
			State:       StateLobby,
			CreatedAt:   time.Now(),
			CreatorID:   userID,
			CreatorName: userName,
			CreatorRoom: room,
		}
		ok, err := m.rdb.SetNX(ctx, m.store.keyMeta(c), []byte("{}"), ttlChannel).Result()
		if err != nil {
			return nil, err
		}
		if ok {
			code = c
			if err := m.store.SaveMeta(ctx, code, meta); err != nil {
				return nil, err
			}
			if err := m.store.AddRoom(ctx, code, room); err != nil {
				return nil, err
			}
			if err := m.store.AddParticipant(ctx, code, userID); err != nil {
				return nil, err
			}
			if err := m.store.AddLobby(ctx, code); err != nil {
				return nil, err
			}
			obslog.L().Info("lobby_make", zap.String("code", code), zap.String("room", room), zap.String("creator_id", userID))
			return &MakeResult{Code: code, Meta: meta}, nil
		}
	}
	return nil, fmt.Errorf("failed to allocate channel code")
}

func (m *Manager) Join(ctx context.Context, room, code, userID, userName string, pref ColorChoice) (*JoinResult, error) {
	code = strings.TrimSpace(code)
	if room == "" || code == "" || userID == "" {
		return nil, ErrInvalidArgs
	}
	meta, err := m.store.LoadMeta(ctx, code)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, ErrChannelGone
	}
	// ACTIVE 상태에서는 추가 참가를 허용하지 않음
	if meta.State != StateLobby {
		return nil, ErrChannelActive
	}

	// WATCH participants to prevent race joins
	partKey := m.store.keyParticipants(code)
	err = m.rdb.Watch(ctx, func(tx *redis.Tx) error {
		cnt, err := tx.SCard(ctx, partKey).Result()
		if err != nil && err != redis.Nil {
			return err
		}
		if cnt >= 2 {
			return ErrFull
		}
		pipe := tx.TxPipeline()
		pipe.SAdd(ctx, partKey, userID)
		pipe.Expire(ctx, partKey, ttlChannel)
		pipe.SAdd(ctx, m.store.keyRooms(code), room)
		pipe.Expire(ctx, m.store.keyRooms(code), ttlChannel)
		pipe.SAdd(ctx, m.store.keyUserIdx(userID), code)
		pipe.Expire(ctx, m.store.keyUserIdx(userID), ttlChannel)
		_, pErr := pipe.Exec(ctx)
		return pErr
	}, partKey)
	if err != nil {
		obslog.L().Warn("lobby_join_error", zap.String("code", code), zap.String("room", room), zap.String("user_id", userID), zap.Error(err))
		return nil, err
	}

	// reload meta and decide start
	if err := m.store.AddRoom(ctx, code, room); err != nil {
		return nil, err
	}
	meta, err = m.store.LoadMeta(ctx, code)
	if err != nil {
		return nil, err
	}

	// check how many now
	cnt, _ := m.store.ParticipantCount(ctx, code)
	if cnt < 2 || meta.GameID != "" {
		// joined but not started yet
		obslog.L().Info("lobby_join", zap.String("code", code), zap.String("room", room), zap.String("user_id", userID), zap.String("reason", "queued"))
		return &JoinResult{Started: false, GameID: meta.GameID, Meta: meta}, nil
	}

	// Two participants → assign colors and (검사 후) start game through pvpchess
	// 참가자는 호출자(userID), 도전자는 생성자(meta.CreatorID)
	challengerID, challengerName := meta.CreatorID, meta.CreatorName
	targetID, targetName := userID, userName
	// 색 선호 입력은 무시하고 항상 랜덤 배정
	colorChoice := string(ColorRandom)

	// 방 기준 중복 대국 금지: 참가자/생성자 각각 자신의 방에서 ACTIVE 대국이 있는지 검사
	if busy, _ := m.pvp.GetActiveGameByUserInRoom(ctx, userID, room); busy != nil {
		return nil, ErrPlayerBusyInRoom
	}
	if busy2, _ := m.pvp.GetActiveGameByUserInRoom(ctx, challengerID, meta.CreatorRoom); busy2 != nil {
		return nil, ErrPlayerBusyInRoom
	}

	g, gerr := m.pvp.CreateGameFromChallenge(ctx, meta.CreatorRoom, room, challengerID, challengerName, targetID, targetName, colorChoice, "")
	if gerr != nil {
		return nil, gerr
	}

	// persist meta with colors and game id
	if g.WhiteID == challengerID {
		meta.WhiteID, meta.WhiteName = g.WhiteID, g.WhiteName
		meta.BlackID, meta.BlackName = g.BlackID, g.BlackName
	} else {
		meta.WhiteID, meta.WhiteName = g.WhiteID, g.WhiteName
		meta.BlackID, meta.BlackName = g.BlackID, g.BlackName
	}
	meta.State = StateActive
	meta.GameID = g.ID
	if err := m.store.SaveMeta(ctx, code, meta); err != nil {
		return nil, err
	}
	// remove from lobby index once game starts
	_ = m.store.RemoveLobby(ctx, code)
	obslog.L().Info("lobby_start_game", zap.String("code", code), zap.String("game_id", g.ID), zap.String("white_id", g.WhiteID), zap.String("black_id", g.BlackID))
	return &JoinResult{Started: true, GameID: g.ID, Meta: meta}, nil
}

func (m *Manager) Rooms(ctx context.Context, code string) ([]string, error) {
    return m.store.Rooms(ctx, code)
}

// RoomsByUserAndGame finds channel rooms for a user where its channel binds the given game.
func (m *Manager) RoomsByUserAndGame(ctx context.Context, userID, gameID string) ([]string, error) {
	codes, err := m.store.CodesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, c := range codes {
		meta, _ := m.store.LoadMeta(ctx, c)
		if meta != nil && meta.GameID == gameID {
			return m.store.Rooms(ctx, c)
		}
	}
	return nil, nil
}

// ListLobby returns lobby (waiting) channels' metadata for listing.
func (m *Manager) ListLobby(ctx context.Context) ([]*ChannelMeta, error) {
    return m.store.ListLobby(ctx)
}

// MetaByGame는 게임 ID로 채널 메타를 조회합니다. (참가자 인덱스를 이용해 선형 탐색)
func (m *Manager) MetaByGame(ctx context.Context, g *pvpchess.Game) (*ChannelMeta, string, error) {
    if g == nil { return nil, "", nil }
    for _, uid := range []string{strings.TrimSpace(g.WhiteID), strings.TrimSpace(g.BlackID)} {
        if uid == "" { continue }
        codes, err := m.store.CodesByUser(ctx, uid)
        if err != nil { continue }
        for _, code := range codes {
            meta, _ := m.store.LoadMeta(ctx, code)
            if meta != nil && strings.TrimSpace(meta.GameID) == strings.TrimSpace(g.ID) {
                return meta, code, nil
            }
        }
    }
    return nil, "", nil
}
