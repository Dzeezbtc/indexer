package gql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/graphql-go/graphql"
	"github.com/lib/pq"
)

// Runner executes one compiled statement and returns its decoded JSON.
type Runner func(ctx context.Context, sqlText string, args []interface{}) (interface{}, error)

// MaxBodyBytes caps a request body.
const MaxBodyBytes = 100 << 10

// DBRunner runs statements in a read-only transaction with a statement
// timeout, so a query can never write and can't run long.
func DBRunner(db *sql.DB, timeout time.Duration) Runner {
	return func(ctx context.Context, sqlText string, args []interface{}) (interface{}, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout+time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, errors.New("database unavailable")
		}
		defer tx.Rollback() //nolint:errcheck // read-only; nothing to commit
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
			return nil, errors.New("database unavailable")
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, sqlText, args...).Scan(&raw); err != nil {
			return nil, queryError(err, timeout)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var out interface{}
		if err := dec.Decode(&out); err != nil {
			return nil, errors.New("bad result from database")
		}
		return out, nil
	}
}

// queryError turns a database error into a message safe to return: the
// Postgres message (e.g. a bad timestamp) without the SQL.
func queryError(err error, timeout time.Duration) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		if pqErr.Code == "57014" { // query_canceled
			return fmt.Errorf("query took longer than %s; narrow it with where or a smaller limit", timeout)
		}
		return errors.New(pqErr.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("query took longer than %s; narrow it with where or a smaller limit", timeout)
	}
	return errors.New("query failed")
}

// Service is the GraphQL endpoint.
type Service struct {
	schema graphql.Schema
}

// NewService builds the schema for a catalog.
func NewService(cat *Catalog, lim Limits, run Runner) (*Service, error) {
	s, err := BuildSchema(cat, lim, run)
	if err != nil {
		return nil, err
	}
	return &Service{schema: s}, nil
}

type request struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
	OperationName string                 `json:"operationName"`
}

// Handle serves POST /graphql.
func (s *Service) Handle(c *gin.Context) {
	start := time.Now()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)
	var req request
	dec := json.NewDecoder(c.Request.Body)
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"errors": []gin.H{{"message": "send JSON: {\"query\": \"...\", \"variables\": {...}}"}}})
		return
	}
	res := graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  req.Query,
		VariableValues: req.Variables,
		OperationName:  req.OperationName,
		Context:        c.Request.Context(),
	})
	log.Printf("[GRAPHQL] key=%s op=%q errors=%d took=%s", c.GetString("api_key_name"), req.OperationName, len(res.Errors), time.Since(start).Round(time.Millisecond))
	c.JSON(http.StatusOK, res)
}

// Do runs a query directly (used by tests).
func (s *Service) Do(ctx context.Context, query string, vars map[string]interface{}) *graphql.Result {
	return graphql.Do(graphql.Params{Schema: s.schema, RequestString: query, VariableValues: vars, Context: ctx})
}
