// Command apikey creates, lists and revokes API keys for the GraphQL endpoint
// (block52/indexer#16). It reads the same DB_* environment variables as the API.
//
//	apikey create -name club-service [-rate 120]   prints the key once
//	apikey list
//	apikey revoke -id 3
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/block52/indexer/internal/apikey"
	"github.com/block52/indexer/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	database, err := db.Connect(db.Config{
		Host:     env("DB_HOST", "localhost"),
		Port:     envInt("DB_PORT", 5432),
		User:     env("DB_USER", "poker"),
		Password: env("DB_PASSWORD", "poker_indexer_dev"),
		DBName:   env("DB_NAME", "poker_hands"),
		MaxConns: 2,
	})
	if err != nil {
		fail(err)
	}
	defer database.Close()

	switch os.Args[1] {
	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		name := fs.String("name", "", "who the key is for, e.g. club-service (required)")
		perMin := fs.Int("rate", 120, "requests a minute")
		_ = fs.Parse(os.Args[2:])
		if *name == "" || *perMin <= 0 {
			fs.Usage()
			os.Exit(2)
		}
		key, hash, err := apikey.Generate()
		if err != nil {
			fail(err)
		}
		var id int
		if err := database.QueryRow(`INSERT INTO api_keys (name, key_hash, rate_per_min) VALUES ($1, $2, $3) RETURNING id`, *name, hash, *perMin).Scan(&id); err != nil {
			fail(err)
		}
		fmt.Printf("Created key %d for %s (%d requests a minute).\n\n  %s\n\nThis is the only time the key is shown. Send it as: Authorization: Bearer <key>\n", id, *name, *perMin, key)
	case "list":
		rows, err := database.Query(`SELECT id, name, rate_per_min, created_at, revoked_at FROM api_keys ORDER BY id`)
		if err != nil {
			fail(err)
		}
		defer rows.Close()
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tPER MIN\tCREATED\tREVOKED")
		for rows.Next() {
			var id, perMin int
			var name string
			var created time.Time
			var revoked *time.Time
			if err := rows.Scan(&id, &name, &perMin, &created, &revoked); err != nil {
				fail(err)
			}
			r := "-"
			if revoked != nil {
				r = revoked.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\n", id, name, perMin, created.UTC().Format(time.RFC3339), r)
		}
		if err := rows.Err(); err != nil {
			fail(err)
		}
		w.Flush()
	case "revoke":
		fs := flag.NewFlagSet("revoke", flag.ExitOnError)
		id := fs.Int("id", 0, "key id, from apikey list (required)")
		_ = fs.Parse(os.Args[2:])
		if *id <= 0 {
			fs.Usage()
			os.Exit(2)
		}
		res, err := database.Exec(`UPDATE api_keys SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, *id)
		if err != nil {
			fail(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(fmt.Errorf("no active key with id %d", *id))
		}
		fmt.Printf("Revoked key %d. It stops working within a minute.\n", *id)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: apikey create -name NAME [-rate N] | apikey list | apikey revoke -id ID")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "apikey:", err)
	os.Exit(1)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}
