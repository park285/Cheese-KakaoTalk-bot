-- Chess bot schema (PostgreSQL)
-- Safe to apply repeatedly

CREATE TABLE IF NOT EXISTS chess_profiles (
  player_hash TEXT NOT NULL,
  room_hash TEXT NOT NULL,
  preferred_preset TEXT DEFAULT '',
  rating INT NOT NULL DEFAULT 1200,
  games_played INT NOT NULL DEFAULT 0,
  wins INT NOT NULL DEFAULT 0,
  losses INT NOT NULL DEFAULT 0,
  draws INT NOT NULL DEFAULT 0,
  streak INT NOT NULL DEFAULT 0,
  streak_type TEXT NOT NULL DEFAULT '',
  last_preset TEXT NOT NULL DEFAULT '',
  last_played_at TIMESTAMP NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  PRIMARY KEY (player_hash, room_hash)
);

CREATE TABLE IF NOT EXISTS chess_games (
  id BIGSERIAL PRIMARY KEY,
  session_uuid TEXT NOT NULL UNIQUE,
  player_hash TEXT NOT NULL,
  room_hash TEXT NOT NULL,
  preset TEXT NOT NULL,
  engine_preset TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  result_method TEXT NOT NULL DEFAULT '',
  moves_uci JSONB NOT NULL DEFAULT '[]',
  moves_san JSONB NOT NULL DEFAULT '[]',
  pgn TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMP NOT NULL,
  ended_at TIMESTAMP NOT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  blunders INT NOT NULL DEFAULT 0,
  engine_latency_ms BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_chess_games_player ON chess_games(player_hash);

-- PvP chess results
CREATE TABLE IF NOT EXISTS pvp_games (
  game_id TEXT PRIMARY KEY,
  white_id TEXT NOT NULL,
  white_name TEXT NOT NULL,
  black_id TEXT NOT NULL,
  black_name TEXT NOT NULL,
  origin_room TEXT NOT NULL DEFAULT '',
  resolve_room TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL,
  result_method TEXT NOT NULL DEFAULT '',
  moves_uci JSONB NOT NULL DEFAULT '[]',
  moves_san JSONB NOT NULL DEFAULT '[]',
  pgn TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMP NOT NULL,
  ended_at TIMESTAMP NOT NULL,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pvp_games_white ON pvp_games(white_id);
CREATE INDEX IF NOT EXISTS idx_pvp_games_black ON pvp_games(black_id);
