# Poker Indexer REST API

REST API for querying poker hand statistics and analysis data from the indexer database.

## Quick Start

### 1. Start the Database

```bash
docker compose up -d postgres
```

### 2. Run the API

```bash
# Build
go build -o api ./cmd/api

# Run with default settings
./api

# Or with custom configuration
API_PORT=8000 \
DB_HOST=localhost \
DB_PORT=5432 \
DB_USER=poker \
DB_PASSWORD=poker_indexer_dev \
DB_NAME=poker_hands \
./api
```

The API will be available at `http://localhost:8000`

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `API_PORT` | API server port | `8000` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_USER` | PostgreSQL user | `poker` |
| `DB_PASSWORD` | PostgreSQL password | `poker_indexer_dev` |
| `DB_NAME` | Database name | `poker_hands` |
| `CORS_ORIGINS` | CORS allowed origins | `*` |
| `ENVIRONMENT` | Environment mode | `development` |
| `DB_MAX_CONNS` | Max DB connections | `25` |
| `DB_MAX_IDLE` | Max idle DB connections | `5` |
| `GRAPHQL_ENABLED` | Serve `POST /graphql` | `true` |
| `GRAPHQL_DB_USER` / `GRAPHQL_DB_PASSWORD` | Database role for GraphQL queries (e.g. a read-only role) | `DB_USER` / `DB_PASSWORD` |
| `GRAPHQL_DB_MAX_CONNS` | Max DB connections for GraphQL (a separate pool) | `5` |
| `GRAPHQL_TIMEOUT_MS` | Longest a GraphQL query may run | `5000` |
| `GRAPHQL_DEFAULT_LIMIT` / `GRAPHQL_MAX_LIMIT` | Rows per list when none is asked for / at most | `100` / `1000` |

## API Endpoints

### Health Check

#### `GET /health`

Returns service health status.

**Response:**
```json
{
  "status": "healthy",
  "database": "connected",
  "uptime": "2h15m30s"
}
```

---

## Hand Data Endpoints

### `GET /api/v1/hands`

List poker hands with pagination and optional filters.

**Query Parameters:**
- `limit` (int, optional): Results per page (default: 50, max: 1000)
- `offset` (int, optional): Results offset (default: 0)
- `game_id` (string, optional): Filter by game ID
- `start_block` (int, optional): Filter by minimum block height
- `end_block` (int, optional): Filter by maximum block height

**Example:**
```bash
curl "http://localhost:8000/api/v1/hands?limit=10&offset=0"
curl "http://localhost:8000/api/v1/hands?game_id=game123"
curl "http://localhost:8000/api/v1/hands?start_block=1000&end_block=2000"
```

**Response:**
```json
{
  "data": [
    {
      "game_id": "game123",
      "hand_number": 42,
      "block_height": 12345,
      "deck_seed": "abc123...",
      "tx_hash": "tx123",
      "created_at": "2024-02-24T10:00:00Z"
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 1234
  }
}
```

> **Cards:** responses never include a deck. `revealed_cards` holds the community cards and only the hole cards that were **shown** at the table (see README, *Card privacy*). `deck_seed` is the block hash used as the shuffle seed.

### `GET /api/v1/hands/:game_id/:hand_number`

Get detailed information about a specific hand.

**Example:**
```bash
curl "http://localhost:8000/api/v1/hands/game123/42"
```

**Response:**
```json
{
  "game_id": "game123",
  "hand_number": 42,
  "block_height": 12345,
  "deck_seed": "abc123...",
  "tx_hash": "tx123",
  "created_at": "2024-02-24T10:00:00Z",
  "result": {
    "game_id": "game123",
    "hand_number": 42,
    "block_height": 12346,
    "community_cards": ["AS", "KH", "QD", "JC", "10S"],
    "winner_count": 2,
    "tx_hash": "tx124",
    "created_at": "2024-02-24T10:05:00Z"
  },
  "revealed_cards": [
    {
      "id": 1,
      "game_id": "game123",
      "hand_number": 42,
      "block_height": 12346,
      "card": "AS",
      "card_type": "community",
      "position": 0,
      "created_at": "2024-02-24T10:05:00Z"
    }
  ]
}
```

### `GET /api/v1/hands/:game_id/:hand_number/cards`

Get all revealed cards for a specific hand.

**Example:**
```bash
curl "http://localhost:8000/api/v1/hands/game123/42/cards"
```

**Response:**
```json
{
  "game_id": "game123",
  "hand_number": 42,
  "cards": [
    {
      "id": 1,
      "game_id": "game123",
      "hand_number": 42,
      "block_height": 12346,
      "card": "AS",
      "card_type": "community",
      "position": 0,
      "created_at": "2024-02-24T10:05:00Z"
    }
  ]
}
```

---

## Statistics Endpoints

### `GET /api/v1/stats/summary`

Get overall statistics summary.

**Example:**
```bash
curl "http://localhost:8000/api/v1/stats/summary"
```

**Response:**
```json
{
  "total_hands": 1234,
  "total_completed_hands": 987,
  "total_revealed_cards": 5432,
  "unique_games": 42,
  "block_height_range": "1-12345",
  "first_indexed_at": "2024-02-01T00:00:00Z",
  "last_indexed_at": "2024-02-24T10:00:00Z"
}
```

### `GET /api/v1/stats/cards`

Get card frequency statistics for all cards.

**Example:**
```bash
curl "http://localhost:8000/api/v1/stats/cards"
```

**Response:**
```json
[
  {
    "card": "AS",
    "rank": "A",
    "suit": "♠",
    "total_appearances": 105,
    "expected_frequency": 1.923,
    "actual_frequency": 1.932,
    "deviation": 0.009,
    "deviation_percent": 0.468
  }
]
```

### `GET /api/v1/stats/cards/:card`

Get statistics for a specific card.

**Example:**
```bash
curl "http://localhost:8000/api/v1/stats/cards/AS"
```

### `GET /api/v1/stats/suits`

Get suit distribution statistics.

**Example:**
```bash
curl "http://localhost:8000/api/v1/stats/suits"
```

**Response:**
```json
[
  {
    "suit": "♠",
    "total_appearances": 1358,
    "expected_frequency": 25.0,
    "actual_frequency": 24.98,
    "deviation": -0.02
  }
]
```

### `GET /api/v1/stats/ranks`

Get rank distribution statistics.

**Example:**
```bash
curl "http://localhost:8000/api/v1/stats/ranks"
```

**Response:**
```json
[
  {
    "rank": "A",
    "total_appearances": 417,
    "expected_frequency": 7.692,
    "actual_frequency": 7.676,
    "deviation": -0.016
  }
]
```

### `GET /api/v1/stats/chi-squared`

Get chi-squared randomness test results.

**Example:**
```bash
curl "http://localhost:8000/api/v1/stats/chi-squared"
```

**Response:**
```json
{
  "chi_squared": 52.45,
  "degrees_of_freedom": 51,
  "p_value": 0.15,
  "result": "PASS",
  "interpretation": "Distribution is consistent with random expectation (p > 0.05)"
}
```

---

## Analysis Endpoints

### `GET /api/v1/analysis/randomness`

Get comprehensive randomness analysis report.

**Example:**
```bash
curl "http://localhost:8000/api/v1/analysis/randomness"
```

**Response:**
```json
{
  "summary": {
    "total_hands": 1234,
    "total_completed_hands": 987,
    "total_revealed_cards": 5432,
    "unique_games": 42,
    "block_height_range": "1-12345"
  },
  "card_chi_squared": {
    "chi_squared": 52.45,
    "degrees_of_freedom": 51,
    "p_value": 0.15,
    "result": "PASS",
    "interpretation": "Distribution is consistent with random expectation"
  },
  "outlier_cards": [],
  "duplicate_seeds": 0
}
```

### `GET /api/v1/analysis/outliers`

Get cards with significant deviation from expected frequency.

**Query Parameters:**
- `threshold` (float, optional): Deviation threshold percentage (default: 1.0)

**Example:**
```bash
curl "http://localhost:8000/api/v1/analysis/outliers?threshold=1.5"
```

**Response:**
```json
{
  "threshold": 1.5,
  "outlier_count": 3,
  "outliers": [
    {
      "card": "2H",
      "rank": "2",
      "suit": "♥",
      "total_appearances": 95,
      "expected_frequency": 1.923,
      "actual_frequency": 1.748,
      "deviation": -0.175,
      "deviation_percent": -9.10
    }
  ]
}
```

### `GET /api/v1/analysis/seeds`

Get deck seed entropy analysis.

**Example:**
```bash
curl "http://localhost:8000/api/v1/analysis/seeds"
```

**Response:**
```json
{
  "total_seeds": 1234,
  "unique_seeds": 1234,
  "duplicate_count": 0,
  "entropy_score": 100.0
}
```

---

## Player Endpoints

> Money fields are raw USDC micro-units (6 decimals): `5000000` = $5.00.
> Percentage fields (`vpip`, `pfr`, `aggression_factor`, `wtsd`, `won_at_showdown`)
> are integers scaled ×100: `2550` = 25.50%.
>
> The aggregate fields (VIP, playing-style, records) come from the `player_stats`
> table, which the indexer refreshes after each indexing run
> (`refresh_all_player_stats` + `update_vip_tiers`). Volume/money fields are
> computed live and are always current.

### `GET /api/v1/players`

Searchable, sortable, paginated player directory.

**Query Parameters:**
- `search` (string, optional): case-insensitive substring match on address
- `sort` (string, optional): one of `net_profit` (default), `total_hands`, `total_rake_contributed`, `vip_points`, `last_seen_block`
- `order` (string, optional): `asc` or `desc` (default)
- `limit` (int, optional): default 50, max 1000
- `offset` (int, optional): default 0

**Example:**
```bash
curl "http://localhost:8000/api/v1/players?search=poker1&sort=net_profit&order=desc&limit=20"
```

**Response:**
```json
{
  "data": [
    {
      "player_address": "poker1abc...",
      "vip_tier": "gold",
      "rakeback_pct": 10,
      "total_hands": 156,
      "total_actions": 478,
      "net_profit": 5000000,
      "total_rake_contributed": 250000000,
      "last_seen_block": 12500
    }
  ],
  "pagination": { "limit": 20, "offset": 0, "total": 342 }
}
```

### `GET /api/v1/players/:address/stats`

Get a player's full profile (volume + VIP + playing style + records).

**Example:**
```bash
curl "http://localhost:8000/api/v1/players/poker1abc.../stats"
```

**Response:**
```json
{
  "player_address": "poker1abc...",
  "total_hands": 156,
  "total_actions": 478,
  "total_buy_ins": 50000000,
  "total_cash_outs": 55000000,
  "net_profit": 5000000,
  "session_count": 12,
  "avg_session_length": 245.5,
  "vip_tier": "gold",
  "rakeback_pct": 10,
  "vip_points": 25000,
  "total_rake_contributed": 250000000,
  "current_month_rake": 210000000,
  "vpip": 2550,
  "pfr": 1800,
  "aggression_factor": 320,
  "wtsd": 2800,
  "won_at_showdown": 5200,
  "total_bets": 120,
  "total_raises": 90,
  "total_calls": 65,
  "total_folds": 140,
  "total_checks": 63,
  "biggest_pot_won": 8000000,
  "biggest_hand_profit": 6000000,
  "longest_session_blocks": 900,
  "first_seen_block": 10000,
  "last_seen_block": 12500,
  "stats_updated_at": "2024-02-24T10:00:00Z"
}
```

### `GET /api/v1/players/:address/sessions`

Get player game sessions with pagination.

**Query Parameters:**
- `limit` (int, optional): Results per page (default: 50)
- `offset` (int, optional): Results offset (default: 0)

**Example:**
```bash
curl "http://localhost:8000/api/v1/players/poker1abc.../sessions?limit=10"
```

**Response:**
```json
{
  "data": [
    {
      "player_address": "poker1abc...",
      "game_id": "game123",
      "join_block": 12000,
      "leave_block": 12500,
      "buy_in_amount": 5000,
      "cash_out_amount": 5500,
      "created_at": "2024-02-24T10:00:00Z"
    }
  ],
  "pagination": {
    "limit": 10,
    "offset": 0,
    "total": 12
  }
}
```

### `GET /api/v1/players/:address/hands`

The finished hands a wallet played, newest first (block52/ui#721 "My Hand History").

A player is in a hand if the hand's own actions name them (blinds, folds, calls, …) or they won it. Seats that were busted, sitting out or waiting are not. Rows come from the public state at each hand's end, so they hold only public data; for shown hole cards use `GET /api/v1/hands/:game_id/:hand_number`.

**Query Parameters:**
- `limit` (int, optional): Results per page (default: 50, max: 1000)
- `offset` (int, optional): Results offset (default: 0)

**Example:**
```bash
curl "http://localhost:8000/api/v1/players/b521abc.../hands?limit=20&offset=0"
```

**Response:**
```json
{
  "data": [
    {
      "game_id": "0x64a6...",
      "hand_number": 13,
      "seat": 3,
      "status": "showing",
      "won_amount": 120,
      "block_height": 470120,
      "ended_at": "2026-10-07T02:14:09Z",
      "community_cards": ["Ah", "7d", "2c", "Ks", "9h"],
      "winner_count": 1
    }
  ],
  "pagination": { "limit": 20, "offset": 0, "total": 57 }
}
```

- `status`: the player's status at the hand's end (`""` if they have since left the table).
- `won_amount`: what the player won in this hand, in the table's units: chips for Sit & Go/tournament, micro-USDC for cash. `0` if they didn't win.
- `ended_at`: block time of the hand's end.
- Hands indexed before `sql/06-hand-players.sql` was applied have no rows until the indexer re-runs over their blocks.

---

## GraphQL: `POST /graphql`

Ask for exactly the data you need: filter on any column, sort, page, and follow relations (a hand → its players → their stats), in one request. It's read-only and needs an API key. The REST endpoints above are unchanged and need no key.

### API keys

Send the key as a bearer token:

```bash
curl -X POST https://indexer.block52.xyz/graphql \
  -H "Authorization: Bearer $INDEXER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"query": "{ indexing_progress { last_scanned_block } }"}'
```

- No key, an unknown key or a revoked key gets `401`. Going over the key's rate gets `429` with `Retry-After`.
- Make, list and revoke keys on the server with `apikey` (it reads the same `DB_*` variables as the API). It prints a new key once; only its sha256 is stored.

```bash
go build -o apikey ./cmd/apikey
./apikey create -name club-service -rate 120   # requests a minute
./apikey list
./apikey revoke -id 3                          # stops working within a minute
```

Run `sql/08-api-keys.sql` once before first use (it's safe to run again). Without it, the API logs `GraphQL disabled` and serves REST as usual.

### What you can query

Every root field is a table: `poker_hands`, `hand_results`, `revealed_cards`, `hand_players`, `player_actions`, `player_sessions`, `player_stats`, `player_monthly_stats`, `vip_tier_history`, `card_distribution_stats`, `indexing_progress`, plus `hands_meta` and `games` once `sql/07-club-reporting.sql` (#15) is applied. Columns come from the database, so new columns appear without a code change. Introspection is on, so any GraphQL client (Altair, Insomnia, Postman, `graphql-codegen`) can show the schema.

| Argument | |
|---|---|
| `where` | `{column: {_eq, _neq, _gt, _gte, _lt, _lte, _in, _nin, _is_null}}`; text also has `_like`, `_ilike`, `_nlike`, `_nilike`; arrays have `_contains`. Combine with `_and`, `_or`, `_not`. Filter through a relation too: `{players: {player_address: {_eq: "b521…"}}}` matches hands that player played. |
| `order_by` | `[{block_height: desc}, {hand_number: asc}]`. Directions: `asc`, `desc`, `asc_nulls_first`, `asc_nulls_last`, `desc_nulls_first`, `desc_nulls_last`. The table's key is always added last, so paging is stable. |
| `limit`, `offset` | Default 100 rows, at most 1,000. |

Relations: `poker_hands` → `result`, `players`, `cards`, `actions` (and `meta`, `game`); `hand_players` → `hand`, `player`; `player_stats` → `hands`, `sessions`, `actions`, `monthly`, `tier_history`; and each child back to its parent. List relations take the same arguments.

`<table>_aggregate(where: …) { aggregate { count sum { … } avg { … } min { … } max { … } } }` gives totals over the matching rows. There's no group-by: for a breakdown (per day, per player), fetch the rows and group them in the client.

Types: `BigInt` (block heights, amounts) is a JSON number; every value the indexer stores is below 2^53, so it's exact in JavaScript. `Timestamp` is an ISO 8601 string, and filters accept anything Postgres reads as a timestamp (`"2026-11-01"`). Amounts are in the table's units: micro-USDC for cash, chips for Sit & Go and tournaments.

### Examples

A player's last 20 hands, with the board and what they won:

```graphql
query ($player: String!) {
  hand_players(where: {player_address: {_eq: $player}}, order_by: {block_height: desc}, limit: 20) {
    game_id
    hand_number
    won_amount
    ended_at
    hand { result { community_cards winner_count } }
  }
}
```

Hands at a table where a given player won something, with everyone's result:

```graphql
{
  poker_hands(where: {game_id: {_eq: "0x…"}, players: {player_address: {_eq: "b521…"}, won_amount: {_gt: 0}}}) {
    hand_number
    players(order_by: {won_amount: desc}) { player_address seat won_amount }
  }
}
```

Total won across all hands this month:

```graphql
{ hand_players_aggregate(where: {ended_at: {_gte: "2026-11-01"}}) { aggregate { count sum { won_amount } } } }
```

A club's play (once #15 is applied), for the club service to group by day and player:

```graphql
query ($treasury: String!, $from: Timestamp!, $to: Timestamp!) {
  hand_players(where: {game: {rake_owner: {_eq: $treasury}}, ended_at: {_gte: $from, _lt: $to}}, limit: 1000) {
    player_address ended_at contributed won_amount net rake_share
    meta { big_blind }
    game { format variant }
  }
}
```

### Limits

- Each root field is one SQL statement, run in a **read-only** transaction with a **5 s** statement timeout (`GRAPHQL_TIMEOUT_MS`). A query that runs longer is stopped, with an error saying to narrow it. Unindexed filters on big tables (`player_actions`) are the usual cause.
- At most 1,000 rows per list, relations nested 4 deep, 25 relation fields per root field, 10 root fields per request, and a 100 KB request body.
- GraphQL uses its own small connection pool, so heavy queries can't take the REST API's connections. Only the tables above are reachable; `api_keys` never is. There are no mutations.

---

## Error Responses

All endpoints return errors in a consistent format:

```json
{
  "error": "Error type",
  "message": "Detailed error message",
  "code": 400
}
```

**HTTP Status Codes:**
- `200` - Success
- `400` - Bad Request (invalid parameters)
- `404` - Not Found
- `500` - Internal Server Error (database error)

---

## Example Usage

### Using cURL

```bash
# Get health status
curl http://localhost:8000/health

# Get first 10 hands
curl "http://localhost:8000/api/v1/hands?limit=10"

# Get stats summary
curl http://localhost:8000/api/v1/stats/summary

# Get randomness report
curl http://localhost:8000/api/v1/analysis/randomness

# Get player stats
curl http://localhost:8000/api/v1/players/poker1abc.../stats
```

### Using JavaScript (fetch)

```javascript
// Get hands
const response = await fetch('http://localhost:8000/api/v1/hands?limit=10');
const data = await response.json();
console.log(data);

// Get stats summary
const stats = await fetch('http://localhost:8000/api/v1/stats/summary');
const summary = await stats.json();
console.log(summary);
```

### Using Python (requests)

```python
import requests

# Get hands
response = requests.get('http://localhost:8000/api/v1/hands', params={'limit': 10})
data = response.json()
print(data)

# Get stats summary
summary = requests.get('http://localhost:8000/api/v1/stats/summary').json()
print(summary)
```

---

## Development

### Running Tests

```bash
# Run API in development mode
ENVIRONMENT=development ./api

# Run with verbose logging
GIN_MODE=debug ./api
```

### Building

```bash
# Build for current platform
go build -o api ./cmd/api

# Build for Linux
GOOS=linux GOARCH=amd64 go build -o api-linux ./cmd/api
```

---

## CORS Configuration

By default, the API allows requests from all origins (`*`). To restrict CORS:

```bash
CORS_ORIGINS=http://localhost:3000 ./api
```

For multiple origins, modify the `loadConfig()` function in `cmd/api/main.go`.
