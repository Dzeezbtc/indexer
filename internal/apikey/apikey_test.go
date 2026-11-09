package apikey

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

type fakeLookup map[string]Key

func (f fakeLookup) Lookup(_ context.Context, hash string) (Key, bool, error) {
	k, ok := f[hash]
	return k, ok, nil
}

func TestGenerateAndHash(t *testing.T) {
	key, hash, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, Prefix) || len(key) < 40 {
		t.Fatalf("key %q", key)
	}
	if hash != Hash(key) || len(hash) != 64 || strings.Contains(hash, key) {
		t.Fatalf("hash %q", hash)
	}
	other, _, _ := Generate()
	if other == key {
		t.Fatal("two keys were the same")
	}
}

func TestMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, hash, _ := Generate()
	r := gin.New()
	r.POST("/graphql", Middleware(fakeLookup{hash: {ID: 1, Name: "club-service", RatePerMin: 12}}), func(c *gin.Context) {
		c.String(http.StatusOK, c.GetString("api_key_name"))
	})
	do := func(auth string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		r.ServeHTTP(w, req)
		return w
	}
	if w := do(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", w.Code)
	}
	if w := do("Bearer " + Prefix + "nope"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", w.Code)
	}
	if w := do(key); w.Code != http.StatusUnauthorized {
		t.Fatalf("missing Bearer: %d", w.Code)
	}
	if w := do("Bearer " + key); w.Code != http.StatusOK || w.Body.String() != "club-service" {
		t.Fatalf("valid key: %d %s", w.Code, w.Body.String())
	}
	// 12 a minute gives a burst of 2; the first call above used one.
	if w := do("Bearer " + key); w.Code != http.StatusOK {
		t.Fatalf("second call: %d", w.Code)
	}
	w := do("Bearer " + key)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d", w.Code)
	}
}

// TestStore needs a Postgres with sql/08-api-keys.sql applied (TEST_DATABASE_URL).
func TestStore(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM api_keys WHERE name LIKE 'apikeytest-%'`) })
	add := func(name string) (string, int) {
		key, hash, _ := Generate()
		var id int
		if err := db.QueryRow(`INSERT INTO api_keys (name, key_hash, rate_per_min) VALUES ($1, $2, 60) RETURNING id`, name, hash).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return key, id
	}
	ctx := context.Background()
	a, aID := add("apikeytest-a")
	s := NewStore(db, time.Hour)
	s.missReload = 0
	if k, ok, err := s.Lookup(ctx, Hash(a)); err != nil || !ok || k.Name != "apikeytest-a" {
		t.Fatalf("a: %v %v %v", k, ok, err)
	}
	// A key made after the cache loaded works at once (a miss reloads).
	b, _ := add("apikeytest-b")
	if _, ok, _ := s.Lookup(ctx, Hash(b)); !ok {
		t.Fatal("new key not found")
	}
	// A revoked key stops working once the cache reloads.
	if _, err := db.Exec(`UPDATE api_keys SET revoked_at = NOW() WHERE id = $1`, aID); err != nil {
		t.Fatal(err)
	}
	s.loadedAt = time.Time{}
	if _, ok, _ := s.Lookup(ctx, Hash(a)); ok {
		t.Fatal("revoked key still works")
	}
}
