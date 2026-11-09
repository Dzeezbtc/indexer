-- Migration: API keys for the GraphQL endpoint (block52/indexer#16).
-- Idempotent: safe to run again.
--
-- Only the sha256 of each key is stored; the key is shown once, when it's
-- made with `apikey create`. This table is never exposed through GraphQL.

CREATE TABLE IF NOT EXISTS api_keys (
    id           SERIAL PRIMARY KEY,
    name         TEXT NOT NULL,                 -- who the key is for, e.g. club-service
    key_hash     TEXT NOT NULL UNIQUE,          -- sha256 hex of the key
    rate_per_min INTEGER NOT NULL DEFAULT 120,  -- requests a minute for this key
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    revoked_at   TIMESTAMP WITH TIME ZONE       -- set by `apikey revoke`; the key stops working within a minute
);
