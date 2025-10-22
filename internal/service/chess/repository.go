package chess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/park285/Cheese-KakaoTalk-bot/internal/domain"
)

var ErrDuplicateGame = errors.New("chess game already exists")

type Repository interface {
	InsertGame(ctx context.Context, game *domain.ChessGame) (int64, error)
	GetRecentGames(ctx context.Context, playerHash string, limit int) ([]*domain.ChessGame, error)
	GetGame(ctx context.Context, id int64, playerHash string) (*domain.ChessGame, error)
	GetGameBySession(ctx context.Context, sessionUUID string, playerHash string) (*domain.ChessGame, error)
	GetProfile(ctx context.Context, playerHash string, roomHash string) (*domain.ChessProfile, error)
	UpsertProfile(ctx context.Context, profile *domain.ChessProfile) error
}

type repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) InsertGame(ctx context.Context, game *domain.ChessGame) (int64, error) {
	if game == nil {
		return 0, fmt.Errorf("nil chess game payload")
	}

	movesUCI, err := json.Marshal(game.MovesUCI)
	if err != nil {
		return 0, fmt.Errorf("marshal moves_uci: %w", err)
	}
	movesSAN, err := json.Marshal(game.MovesSAN)
	if err != nil {
		return 0, fmt.Errorf("marshal moves_san: %w", err)
	}

	const query = `
		INSERT INTO chess_games (
			session_uuid,
			player_hash,
			room_hash,
			preset,
			engine_preset,
			result,
			result_method,
			moves_uci,
			moves_san,
			pgn,
			started_at,
			ended_at,
			duration_ms,
			blunders,
			engine_latency_ms
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (session_uuid) DO NOTHING
		RETURNING id`

	var id sql.NullInt64
	err = r.db.QueryRowContext(
		ctx,
		query,
		game.SessionUUID,
		game.PlayerHash,
		game.RoomHash,
		game.Preset,
		game.EnginePreset,
		game.Result,
		game.ResultMethod,
		movesUCI,
		movesSAN,
		game.PGN,
		game.StartedAt,
		game.EndedAt,
		game.Duration.Milliseconds(),
		game.Blunders,
		game.EngineLatency.Milliseconds(),
	).Scan(&id)
	if err == sql.ErrNoRows || (err == nil && !id.Valid) {
		return 0, ErrDuplicateGame
	}
	if err != nil {
		return 0, fmt.Errorf("insert chess game: %w", err)
	}
	return id.Int64, nil
}

func (r *repository) GetRecentGames(ctx context.Context, playerHash string, limit int) ([]*domain.ChessGame, error) {
	if limit <= 0 {
		limit = 10
	}
	const query = `
		SELECT
			id,
			session_uuid,
			player_hash,
			room_hash,
			preset,
			engine_preset,
			result,
			result_method,
			moves_uci,
			moves_san,
			pgn,
			started_at,
			ended_at,
			duration_ms,
			blunders,
			engine_latency_ms
		FROM chess_games
		WHERE player_hash = $1
		ORDER BY ended_at DESC
		LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, playerHash, limit)
	if err != nil {
		return nil, fmt.Errorf("select chess games: %w", err)
	}
	defer rows.Close()

	games := make([]*domain.ChessGame, 0, limit)
	for rows.Next() {
		var (
			game         domain.ChessGame
			movesUCIJSON []byte
			movesSANJSON []byte
			durationMS   sql.NullInt64
			latencyMS    sql.NullInt64
		)
		if err := rows.Scan(
			&game.ID,
			&game.SessionUUID,
			&game.PlayerHash,
			&game.RoomHash,
			&game.Preset,
			&game.EnginePreset,
			&game.Result,
			&game.ResultMethod,
			&movesUCIJSON,
			&movesSANJSON,
			&game.PGN,
			&game.StartedAt,
			&game.EndedAt,
			&durationMS,
			&game.Blunders,
			&latencyMS,
		); err != nil {
			return nil, fmt.Errorf("scan chess game: %w", err)
		}
		if durationMS.Valid {
			game.Duration = time.Duration(durationMS.Int64) * time.Millisecond
		}
		if latencyMS.Valid {
			game.EngineLatency = time.Duration(latencyMS.Int64) * time.Millisecond
		}
		if err := json.Unmarshal(movesUCIJSON, &game.MovesUCI); err != nil {
			return nil, fmt.Errorf("unmarshal moves_uci: %w", err)
		}
		if err := json.Unmarshal(movesSANJSON, &game.MovesSAN); err != nil {
			return nil, fmt.Errorf("unmarshal moves_san: %w", err)
		}
		games = append(games, &game)
	}
	return games, nil
}

func (r *repository) GetGame(ctx context.Context, id int64, playerHash string) (*domain.ChessGame, error) {
	const query = `
		SELECT
			id,
			session_uuid,
			player_hash,
			room_hash,
			preset,
			engine_preset,
			result,
			result_method,
			moves_uci,
			moves_san,
			pgn,
			started_at,
			ended_at,
			duration_ms,
			blunders,
			engine_latency_ms
		FROM chess_games
		WHERE id = $1 AND player_hash = $2`

	var (
		game         domain.ChessGame
		movesUCIJSON []byte
		movesSANJSON []byte
		durationMS   sql.NullInt64
		latencyMS    sql.NullInt64
	)

	err := r.db.QueryRowContext(ctx, query, id, playerHash).Scan(
		&game.ID,
		&game.SessionUUID,
		&game.PlayerHash,
		&game.RoomHash,
		&game.Preset,
		&game.EnginePreset,
		&game.Result,
		&game.ResultMethod,
		&movesUCIJSON,
		&movesSANJSON,
		&game.PGN,
		&game.StartedAt,
		&game.EndedAt,
		&durationMS,
		&game.Blunders,
		&latencyMS,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select chess game: %w", err)
	}

	if durationMS.Valid {
		game.Duration = time.Duration(durationMS.Int64) * time.Millisecond
	}
	if latencyMS.Valid {
		game.EngineLatency = time.Duration(latencyMS.Int64) * time.Millisecond
	}
	if err := json.Unmarshal(movesUCIJSON, &game.MovesUCI); err != nil {
		return nil, fmt.Errorf("unmarshal moves_uci: %w", err)
	}
	if err := json.Unmarshal(movesSANJSON, &game.MovesSAN); err != nil {
		return nil, fmt.Errorf("unmarshal moves_san: %w", err)
	}
	return &game, nil
}

func (r *repository) GetGameBySession(ctx context.Context, sessionUUID string, playerHash string) (*domain.ChessGame, error) {
	const query = `
		SELECT
			id,
			session_uuid,
			player_hash,
			room_hash,
			preset,
			engine_preset,
			result,
			result_method,
			moves_uci,
			moves_san,
			pgn,
			started_at,
			ended_at,
			duration_ms,
			blunders,
			engine_latency_ms
		FROM chess_games
		WHERE session_uuid = $1 AND player_hash = $2
		ORDER BY ended_at DESC
		LIMIT 1`

	var (
		game         domain.ChessGame
		movesUCIJSON []byte
		movesSANJSON []byte
		durationMS   sql.NullInt64
		latencyMS    sql.NullInt64
	)

	err := r.db.QueryRowContext(ctx, query, sessionUUID, playerHash).Scan(
		&game.ID,
		&game.SessionUUID,
		&game.PlayerHash,
		&game.RoomHash,
		&game.Preset,
		&game.EnginePreset,
		&game.Result,
		&game.ResultMethod,
		&movesUCIJSON,
		&movesSANJSON,
		&game.PGN,
		&game.StartedAt,
		&game.EndedAt,
		&durationMS,
		&game.Blunders,
		&latencyMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select chess game by session: %w", err)
	}

	if durationMS.Valid {
		game.Duration = time.Duration(durationMS.Int64) * time.Millisecond
	}
	if latencyMS.Valid {
		game.EngineLatency = time.Duration(latencyMS.Int64) * time.Millisecond
	}
	if err := json.Unmarshal(movesUCIJSON, &game.MovesUCI); err != nil {
		return nil, fmt.Errorf("unmarshal moves_uci: %w", err)
	}
	if err := json.Unmarshal(movesSANJSON, &game.MovesSAN); err != nil {
		return nil, fmt.Errorf("unmarshal moves_san: %w", err)
	}

	return &game, nil
}

func (r *repository) GetProfile(ctx context.Context, playerHash string, roomHash string) (*domain.ChessProfile, error) {
	const query = `
		SELECT
			player_hash,
			room_hash,
			preferred_preset,
			rating,
			games_played,
			wins,
			losses,
			draws,
			streak,
			streak_type,
			last_preset,
			last_played_at,
			updated_at,
			created_at
		FROM chess_profiles
		WHERE player_hash = $1 AND room_hash = $2
		LIMIT 1`

	var profile domain.ChessProfile
	err := r.db.QueryRowContext(ctx, query, playerHash, roomHash).Scan(
		&profile.PlayerHash,
		&profile.RoomHash,
		&profile.PreferredPreset,
		&profile.Rating,
		&profile.GamesPlayed,
		&profile.Wins,
		&profile.Losses,
		&profile.Draws,
		&profile.Streak,
		&profile.StreakType,
		&profile.LastPreset,
		&profile.LastPlayedAt,
		&profile.UpdatedAt,
		&profile.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select chess profile: %w", err)
	}
	return &profile, nil
}

func (r *repository) UpsertProfile(ctx context.Context, profile *domain.ChessProfile) error {
	if profile == nil {
		return fmt.Errorf("nil chess profile payload")
	}
	const query = `
		INSERT INTO chess_profiles (
			player_hash,
			room_hash,
			preferred_preset,
			rating,
			games_played,
			wins,
			losses,
			draws,
			streak,
			streak_type,
			last_preset,
			last_played_at,
			updated_at,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		ON CONFLICT (player_hash, room_hash)
		DO UPDATE SET
			preferred_preset = EXCLUDED.preferred_preset,
			rating = EXCLUDED.rating,
			games_played = EXCLUDED.games_played,
			wins = EXCLUDED.wins,
			losses = EXCLUDED.losses,
			draws = EXCLUDED.draws,
			streak = EXCLUDED.streak,
			streak_type = EXCLUDED.streak_type,
			last_preset = EXCLUDED.last_preset,
			last_played_at = EXCLUDED.last_played_at,
			updated_at = NOW()`

	_, err := r.db.ExecContext(
		ctx,
		query,
		profile.PlayerHash,
		profile.RoomHash,
		profile.PreferredPreset,
		profile.Rating,
		profile.GamesPlayed,
		profile.Wins,
		profile.Losses,
		profile.Draws,
		profile.Streak,
		profile.StreakType,
		profile.LastPreset,
		profile.LastPlayedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert chess profile: %w", err)
	}
	return nil
}
