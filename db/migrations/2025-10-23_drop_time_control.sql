-- Drop obsolete time_control column from PvP results
ALTER TABLE IF EXISTS pvp_games
    DROP COLUMN IF EXISTS time_control;

