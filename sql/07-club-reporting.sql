-- Migration: money per player per hand, hand totals and table details, for
-- club reporting (Dzeezbtc/block52-club). Idempotent: safe to run again.
-- Hands indexed before this have zeros until the indexer re-runs over their blocks.

ALTER TABLE hand_players ADD COLUMN IF NOT EXISTS contributed BIGINT NOT NULL DEFAULT 0; -- what the player put in this hand
ALTER TABLE hand_players ADD COLUMN IF NOT EXISTS rake_share  BIGINT NOT NULL DEFAULT 0; -- their share of the hand's rake, by contribution
ALTER TABLE hand_players ADD COLUMN IF NOT EXISTS net         BIGINT NOT NULL DEFAULT 0; -- won_amount - contributed

CREATE TABLE IF NOT EXISTS hands_meta (
    game_id        TEXT NOT NULL,
    hand_number    INTEGER NOT NULL,
    rake           BIGINT NOT NULL DEFAULT 0,
    pot            BIGINT NOT NULL DEFAULT 0,
    small_blind    BIGINT NOT NULL DEFAULT 0,
    big_blind      BIGINT NOT NULL DEFAULT 0,
    players_dealt  INTEGER NOT NULL DEFAULT 0,
    saw_flop       BOOLEAN NOT NULL DEFAULT false,
    block_height   BIGINT NOT NULL,
    ended_at       TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (game_id, hand_number)
);
CREATE INDEX IF NOT EXISTS idx_hands_meta_ended ON hands_meta (ended_at);

CREATE TABLE IF NOT EXISTS games (
    game_id        TEXT PRIMARY KEY,
    creator        TEXT NOT NULL DEFAULT '',
    format         TEXT NOT NULL DEFAULT '',
    variant        TEXT NOT NULL DEFAULT '',
    betting_limit  TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL DEFAULT '',
    max_players    INTEGER NOT NULL DEFAULT 0,
    rake_owner     TEXT NOT NULL DEFAULT '',
    indexed_at     TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_games_rake_owner ON games (rake_owner);
