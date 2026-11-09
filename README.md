# Poker Hand Distribution Indexer

![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)

Index poker hands from Pokerchain into PostgreSQL for statistical analysis and randomness verification.

## Overview

A standalone Go indexer that fetches poker hand events from any Pokerchain node and stores them in PostgreSQL for statistical analysis.

### Events Tracked

| Event | Description |
|-------|-------------|
| `hand_started` | New hand dealt - records the hand and its deck seed (the block hash) |
| `hand_completed` | Hand finished at showdown - records the community cards and the hole cards that were **shown** |

### Card privacy

The indexer stores and serves **only cards shown at the table**:

- **No decks.** Decks are never read or stored, and the API has no `deck` field. Older chain events carry a `deck` attribute on `hand_started`; it is ignored.
- **Community cards** come from the `hand_completed` event.
- **Hole cards** come only from players whose status was `showing` in the chain's **masked public game state at the block where the hand ended** (`GET /block52/pokerchain/poker/v1/game_state_public/{game_id}` with the `x-cosmos-block-height` header; if the next hand started in that same block, the block before it). The event's `revealed_hole_cards` attribute is not used, because on older blocks it lists every seat's cards.
- If the node no longer keeps state at that height (pruned history), only the community cards are stored for that hand.

### Randomness analysis uses community cards only

Community cards are an unbiased sample of deck positions. Shown hole cards are a **biased** sample (only hands that reached showdown), so the frequency views and chi-squared tests count community cards only. Shown hole cards are still stored and counted in `hole_card_appearances`.

### Analysis Capabilities

- **Card frequency distribution** - Verify each card appears ~1/52 of the time
- **Chi-squared tests** - Statistical test for uniform distribution
- **Suit/Rank distribution** - Check for bias in suits or ranks
- **Deck seed entropy** - Verify block hashes provide unique seeds
- **Time series analysis** - Track distribution over block ranges

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.22+ (for building binaries)
- Access to a Pokerchain RPC node

### Complete Setup

```bash
# 1. Start PostgreSQL database
docker compose up -d postgres

# 2. Verify database is running
docker compose ps

# 3. (Optional) Start Adminer database UI on port 8080
docker compose --profile tools up -d adminer

# 4. Build indexer
go build -o indexer ./cmd/indexer

# 5. Run backfill (replace with your node URL)
./backfill.sh http://localhost:26657

# 6. (Optional) Build and start REST API
go build -o api ./cmd/api
./api
# (REST is keyless; POST /graphql needs an API key, see API.md "GraphQL")
```

### Monitor Progress

```bash
# Check database is healthy
docker compose ps

# View database logs
docker compose logs -f postgres

# Check indexed hand count
docker compose exec postgres psql -U poker -d poker_hands -c "SELECT COUNT(*) FROM poker_hands;"

# Run analysis summary
./run-analysis.sh summary

# Test API (if running)
curl http://localhost:8000/health
curl http://localhost:8000/api/v1/stats/summary
```

### Stop Everything

```bash
# Stop all containers
docker compose down

# Stop and remove ALL data (⚠️ deletes indexed data)
docker compose down -v

# Stop API (if running in background)
pkill -f ./api
```

## Usage Examples

### Example 1: Index Local Development Node

```bash
# Start database
docker compose up -d postgres

# Build and run indexer against local node
go build -o indexer ./cmd/indexer
./backfill.sh http://localhost:26657

# View results
./run-analysis.sh summary
```

### Example 2: Index Production Node with API

```bash
# Start database
docker compose up -d postgres

# Index production node
./backfill.sh https://rpc.pokerchain.io

# Start REST API
go build -o api ./cmd/api
./api &

# Query via API
curl http://localhost:8000/api/v1/stats/summary
curl http://localhost:8000/api/v1/analysis/randomness
```

### Example 3: Index Specific Block Range

```bash
# Index blocks 1000-5000 only
./backfill.sh http://localhost:26657 1000 5000

# Check what was indexed
./run-analysis.sh summary
```

### Example 4: Continuous Monitoring

```bash
# Terminal 1: Run indexer continuously
while true; do
  ./backfill.sh http://localhost:26657
  sleep 60  # Wait 1 minute between runs
done

# Terminal 2: Monitor database
watch -n 5 'docker compose exec postgres psql -U poker -d poker_hands -c "SELECT COUNT(*) FROM poker_hands;"'

# Terminal 3: API for real-time queries
./api
```

### Reading from a validator's own RPC

Every block costs the node a `block_results` read, and every finished hand adds historical state queries. On a small validator those reads compete with consensus. Pass `-block-delay-ms` (or set `BLOCK_DELAY_MS` for docker compose) to pause after each chain read:

```bash
# when backfilling from a validator's local RPC, use a delay (e.g. 25ms) so the node keeps up with consensus
./indexer -node http://localhost:26657 -continuous -block-delay-ms 25
```

The default is `0` (no delay). The effective delay is logged at startup.

### Upgrading an existing database

Existing databases may hold decks and hole cards that were never shown. `sql/init.sql` and `sql/analysis.sql` are idempotent, so re-apply them, then run the migration:

```bash
for f in sql/init.sql sql/analysis.sql sql/05-shown-cards-only.sql sql/06-hand-players.sql; do
  docker compose exec -T postgres psql -U poker -d poker_hands < "$f"
done
```

The migration drops `poker_hands.deck`, deletes all stored hole cards (it can't tell which were shown), normalizes card codes, and recomputes the stats. Then re-run the indexer from your earliest block to repopulate the shown hole cards.

`06-hand-players.sql` adds `hand_players` (who played each hand, for per-wallet hand history). Hands indexed before it was applied get rows when the indexer re-runs over their blocks.

## Backfill Script

The `backfill.sh` script handles everything:

```bash
./backfill.sh <node_rpc_url> [start_block] [end_block]
```

**Arguments:**
- `node_rpc_url` - RPC endpoint (e.g., `http://localhost:26657`)
- `start_block` - Starting block height (default: 1)
- `end_block` - Ending block height (default: 0 = latest)

**Environment Variables:**
- `DB_HOST` - PostgreSQL host (default: localhost)
- `DB_PORT` - PostgreSQL port (default: 5432)
- `DB_USER` - PostgreSQL user (default: poker)
- `DB_PASS` - PostgreSQL password (default: poker_indexer_dev)
- `DB_NAME` - PostgreSQL database (default: poker_hands)

**Examples:**
```bash
# Backfill all blocks from local node
./backfill.sh http://localhost:26657

# Backfill blocks 1000-2000 from remote node
./backfill.sh https://rpc.pokerchain.io 1000 2000

# Use custom database
DB_HOST=db.example.com DB_PASS=secret ./backfill.sh http://localhost:26657
```

## Continuous Indexing (Cron)

The indexer is a one-time backfill tool that exits after processing blocks. For continuous indexing, use the cron setup script:

```bash
./setup-cron.sh http://node1.block52.xyz:26657
```

This will:
- Create a wrapper script that automatically resumes from the last indexed block
- Set up a cron job to run every 5 minutes (configurable)
- Create a logs directory with daily log files
- Auto-cleanup logs older than 30 days

**Cron Management:**

```bash
# View cron jobs
crontab -l

# View today's indexer log
tail -f logs/indexer-$(date +%Y%m%d).log

# View all logs
ls -lh logs/

# Remove cron job
crontab -l | grep -v 'run-indexer-cron.sh' | crontab -

# Run indexer manually
./run-indexer-cron.sh
```

**How it works:**
1. Queries database for last indexed block
2. Runs indexer from `last_block + 1` to latest
3. Logs output to daily log file
4. Runs every N minutes via cron

**Production Setup:**
```bash
# On production server
cd poker-indexer
./setup-cron.sh http://node1.block52.xyz:26657

# Monitor logs
tail -f logs/indexer-$(date +%Y%m%d).log
```

## Direct Indexer Usage

You can also run the indexer directly:

```bash
# Build (if not already built)
go build -o indexer ./cmd/indexer

# Run with flags
./indexer \
  -node http://localhost:26657 \
  -start 1 \
  -end 10000 \
  -db-host localhost \
  -db-port 5432 \
  -db-user poker \
  -db-pass poker_indexer_dev \
  -db-name poker_hands
```

## Analysis Commands

| Command | Description |
|---------|-------------|
| `./run-analysis.sh report` | Comprehensive randomness report |
| `./run-analysis.sh summary` | Basic statistics |
| `./run-analysis.sh cards` | Card frequency analysis |
| `./run-analysis.sh outliers` | Cards with >1% deviation |
| `./run-analysis.sh suits` | Suit distribution + chi-squared |
| `./run-analysis.sh ranks` | Rank distribution + chi-squared |
| `./run-analysis.sh chi-squared` | Full chi-squared test |
| `./run-analysis.sh seeds` | Check for duplicate deck seeds |
| `./run-analysis.sh timeline` | Distribution over time |
| `./run-analysis.sh full` | All analyses |

## Understanding Results

### Chi-Squared Test

The chi-squared test compares observed card frequencies to expected uniform distribution:

| Result | Meaning |
|--------|---------|
| `PASS (p > 0.05)` | Distribution is consistent with random |
| `MARGINAL (0.01 < p < 0.05)` | Borderline - may need more data |
| `FAIL (p < 0.01)` | Distribution appears non-random |

### Critical Values (df=51)

- p=0.05: 68.67
- p=0.01: 76.15
- p=0.001: 86.66

### Sample Size Requirements

For statistically significant results:
- Minimum: 100 cards
- Recommended: 1,000+ cards
- High confidence: 10,000+ cards

## Database Schema

### Tables

```sql
-- Hands that started (with deck info)
poker_hands (game_id, hand_number, block_height, deck_seed, deck)

-- Completed hands with results
hand_results (game_id, hand_number, community_cards, winner_count)

-- Individual revealed cards
revealed_cards (game_id, hand_number, card, card_type, position)

-- Aggregated statistics (auto-updated via trigger)
card_distribution_stats (card, rank, suit, total_appearances, ...)
```

### Views

```sql
card_frequency_analysis   -- Card frequency with expected vs actual
rank_distribution        -- Aggregated by rank
suit_distribution        -- Aggregated by suit
seed_entropy_analysis    -- Check for duplicate seeds
```

## Manual Queries

```sql
-- Card frequency
SELECT * FROM card_frequency_analysis ORDER BY total_appearances DESC;

-- Run randomness report
SELECT * FROM generate_randomness_report();

-- Chi-squared test
SELECT * FROM calculate_chi_squared();

-- Check for patterns in first two community cards
SELECT * FROM community_card_sequences;
```

## REST API

The indexer includes a comprehensive REST API for programmatic access to statistics and analysis data.

### Starting the API

```bash
# Build
go build -o api ./cmd/api

# Run
./api
```

API available at `http://localhost:8000`

### Production API

The API is available at: **https://indexer.block52.xyz**

### API Endpoints & Response Shapes

#### Health Check
```bash
GET https://indexer.block52.xyz/health
```
**Response:**
```json
{
  "status": "healthy",
  "database": "connected",
  "uptime": "1h23m45s"
}
```

#### Indexing Status
```bash
GET https://indexer.block52.xyz/api/v1/status
```
**Response:**
```json
{
  "total_blocks": 0,
  "blocks_indexed": 115394,
  "percent_complete": 0,
  "last_block_indexed": 115394,
  "first_block_indexed": 1,
  "total_hands": 1523,
  "total_games": 142
}
```

#### Statistics Summary
```bash
GET https://indexer.block52.xyz/api/v1/stats/summary
```
**Response:**
```json
{
  "total_hands": 1523,
  "total_cards_revealed": 7615,
  "unique_cards": 52,
  "total_games": 142,
  "block_range": {
    "min": 1,
    "max": 45892
  }
}
```

#### Card Frequency Distribution
```bash
GET https://indexer.block52.xyz/api/v1/stats/cards
```
**Response:**
```json
{
  "cards": [
    {
      "card": "As",
      "rank": "A",
      "suit": "s",
      "total_appearances": 147,
      "expected_frequency": 0.0192,
      "actual_frequency": 0.0193,
      "deviation": 0.0001
    },
    {
      "card": "Kh",
      "rank": "K",
      "suit": "h",
      "total_appearances": 145,
      "expected_frequency": 0.0192,
      "actual_frequency": 0.0190,
      "deviation": -0.0002
    }
  ],
  "total_cards": 7615,
  "unique_cards": 52
}
```

#### Randomness Analysis
```bash
GET https://indexer.block52.xyz/api/v1/analysis/randomness
```
**Response:**
```json
{
  "chi_squared_test": {
    "chi_squared": 51.23,
    "degrees_of_freedom": 51,
    "p_value": 0.47,
    "result": "PASS",
    "interpretation": "Distribution is consistent with random"
  },
  "total_cards": 7615,
  "expected_per_card": 146.44
}
```

#### Suit Distribution
```bash
GET https://indexer.block52.xyz/api/v1/stats/suits
```
**Response:**
```json
{
  "suits": [
    {
      "suit": "hearts",
      "symbol": "h",
      "total_appearances": 1904,
      "expected_frequency": 0.25,
      "actual_frequency": 0.2501,
      "deviation": 0.0001
    }
  ],
  "chi_squared": 0.42,
  "p_value": 0.94
}
```

#### Rank Distribution
```bash
GET https://indexer.block52.xyz/api/v1/stats/ranks
```
**Response:**
```json
{
  "ranks": [
    {
      "rank": "A",
      "name": "Ace",
      "total_appearances": 585,
      "expected_frequency": 0.0769,
      "actual_frequency": 0.0768,
      "deviation": -0.0001
    }
  ],
  "chi_squared": 5.67,
  "p_value": 0.89
}
```

#### Player Statistics
```bash
GET https://indexer.block52.xyz/api/v1/players/{address}/stats
```
**Response:**
```json
{
  "player_address": "poker1abc...",
  "total_hands": 42,
  "hands_won": 18,
  "win_rate": 0.4286,
  "total_wagered": "1250000",
  "total_won": "2100000",
  "net_profit": "850000",
  "best_hand": "Royal Flush",
  "games_played": 8
}
```

#### Player Sessions
```bash
GET https://indexer.block52.xyz/api/v1/players/{address}/sessions
```
**Response:**
```json
{
  "sessions": [
    {
      "game_id": "game_123",
      "hands_played": 12,
      "hands_won": 5,
      "total_wagered": "150000",
      "total_won": "280000",
      "net_profit": "130000",
      "start_block": 1000,
      "end_block": 1200
    }
  ],
  "total_sessions": 8
}
```

#### Outlier Cards
```bash
GET https://indexer.block52.xyz/api/v1/analysis/outliers
```
**Response:**
```json
{
  "outliers": [
    {
      "card": "7d",
      "total_appearances": 132,
      "expected": 146.44,
      "deviation": -0.0983,
      "deviation_percentage": -9.83
    }
  ],
  "threshold": 0.01
}
```

**Full API documentation:** See [API.md](API.md)

## Connect to Database

```bash
# Via psql
PGPASSWORD=poker_indexer_dev psql -h localhost -U poker -d poker_hands

# Via Adminer UI
open http://localhost:8080
# Server: postgres, User: poker, Password: poker_indexer_dev, Database: poker_hands

# Via REST API
curl http://localhost:8000/api/v1/stats/summary
```

## Verifying Setup

### Check Database Connection

```bash
# Check container is running
docker compose ps

# Should show:
# NAME                 IMAGE                  STATUS
# poker-indexer-db     postgres:16-alpine     Up (healthy)

# Test database connection
docker compose exec postgres psql -U poker -d poker_hands -c "SELECT version();"

# Check tables exist
docker compose exec postgres psql -U poker -d poker_hands -c "\dt"
```

### Check Indexed Data

```bash
# Count total hands
docker compose exec postgres psql -U poker -d poker_hands -c "SELECT COUNT(*) FROM poker_hands;"

# View recent hands
docker compose exec postgres psql -U poker -d poker_hands -c "SELECT game_id, hand_number, block_height FROM poker_hands ORDER BY block_height DESC LIMIT 5;"

# Run analysis summary
./run-analysis.sh summary
```

### Check API (if running)

```bash
# Test health endpoint
curl http://localhost:8000/health

# Should return:
# {"status":"healthy","database":"connected","uptime":"..."}

# Test stats endpoint
curl http://localhost:8000/api/v1/stats/summary

# View all available endpoints
curl http://localhost:8000/api/v1/
```

## Troubleshooting

### Database not starting
```bash
# Check Docker is running
docker ps

# View database logs
docker compose logs postgres

# Restart database
docker compose restart postgres
```

### No data appearing
1. Check that the node has poker hand events (`hand_started`, `hand_completed`)
2. Verify the block range contains poker game transactions
3. Run `./run-analysis.sh summary` to see indexed counts

### Statistics not updating
```bash
./run-analysis.sh refresh-stats
```

### Reset database
```bash
docker compose down -v
docker compose up -d postgres
```

### Build errors
```bash
go mod tidy
go build -o indexer ./cmd/indexer
```
