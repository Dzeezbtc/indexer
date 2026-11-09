// Package apikey guards the GraphQL endpoint with API keys (block52/indexer#16).
//
// Only the sha256 of a key is stored. Active keys are cached for a minute, so
// revoking one takes effect within a minute. Each key has its own rate limit.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// Prefix starts every key, so a leaked key is easy to recognise.
const Prefix = "b52i_"

// Key is an active API key.
type Key struct {
	ID         int
	Name       string
	RatePerMin int
}

// Generate makes a new key and its hash. Show the key once; store the hash.
func Generate() (key, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	key = Prefix + base64.RawURLEncoding.EncodeToString(b)
	return key, Hash(key), nil
}

// Hash is the stored form of a key.
func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Lookup finds the active key with this hash.
type Lookup interface {
	Lookup(ctx context.Context, hash string) (Key, bool, error)
}

// Store reads active keys from the api_keys table and caches them.
type Store struct {
	db  *sql.DB
	ttl time.Duration
	// missReload: on an unknown key, reload at most this often, so a new key
	// works straight away but a flood of bad keys can't hammer the database.
	missReload time.Duration

	mu       sync.Mutex
	keys     map[string]Key
	loadedAt time.Time
}

// NewStore caches active keys for ttl (so a revoke takes up to ttl).
func NewStore(db *sql.DB, ttl time.Duration) *Store {
	return &Store{db: db, ttl: ttl, missReload: 5 * time.Second}
}

// Lookup implements Lookup.
func (s *Store) Lookup(ctx context.Context, hash string) (Key, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys == nil || time.Since(s.loadedAt) > s.ttl {
		if err := s.reload(ctx); err != nil && s.keys == nil {
			return Key{}, false, err
		}
		// On error with a cached set, keep serving the last good set.
	}
	k, ok := s.keys[hash]
	if !ok && time.Since(s.loadedAt) > s.missReload {
		if err := s.reload(ctx); err == nil {
			k, ok = s.keys[hash]
		}
	}
	return k, ok, nil
}

func (s *Store) reload(ctx context.Context) error {
	keys, err := s.load(ctx)
	if err != nil {
		return err
	}
	s.keys, s.loadedAt = keys, time.Now()
	return nil
}

func (s *Store) load(ctx context.Context) (map[string]Key, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, key_hash, rate_per_min FROM api_keys WHERE revoked_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("reading api_keys: %w", err)
	}
	defer rows.Close()
	keys := map[string]Key{}
	for rows.Next() {
		var k Key
		var hash string
		if err := rows.Scan(&k.ID, &k.Name, &hash, &k.RatePerMin); err != nil {
			return nil, err
		}
		keys[hash] = k
	}
	return keys, rows.Err()
}

// Middleware requires a valid key (Authorization: Bearer <key>) and applies
// that key's rate limit. It sets "api_key_name" on the context for logging.
func Middleware(l Lookup) gin.HandlerFunc {
	var mu sync.Mutex
	limiters := map[int]*rate.Limiter{}
	limiter := func(k Key) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		per := k.RatePerMin
		if per <= 0 {
			per = 1
		}
		lim := limiters[k.ID]
		r := rate.Limit(float64(per) / 60)
		if lim == nil || lim.Limit() != r {
			lim = rate.NewLimiter(r, int(math.Max(1, float64(per)/6)))
			limiters[k.ID] = lim
		}
		return lim
	}
	deny := func(c *gin.Context, status int, msg string) {
		c.AbortWithStatusJSON(status, gin.H{"errors": []gin.H{{"message": msg}}})
	}
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		key, ok := strings.CutPrefix(h, "Bearer ")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			deny(c, http.StatusUnauthorized, "an API key is required: send Authorization: Bearer <key>")
			return
		}
		k, found, err := l.Lookup(c.Request.Context(), Hash(key))
		if err != nil {
			deny(c, http.StatusServiceUnavailable, "can't check API keys right now")
			return
		}
		if !found {
			deny(c, http.StatusUnauthorized, "unknown or revoked API key")
			return
		}
		lim := limiter(k)
		if !lim.Allow() {
			c.Header("Retry-After", strconv.Itoa(int(math.Ceil(60/float64(max(k.RatePerMin, 1))))))
			deny(c, http.StatusTooManyRequests, fmt.Sprintf("rate limit: %d requests a minute for this key", k.RatePerMin))
			return
		}
		c.Set("api_key_name", k.Name)
		c.Next()
	}
}
