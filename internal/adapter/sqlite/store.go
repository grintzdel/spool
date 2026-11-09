package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *sql.DB
}

var _ usecase.Store = (*Store)(nil)

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open database file: %w", err)
	}
	f.Close()

	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(files)
	for i, name := range files {
		if i < version {
			continue
		}
		script, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveRequest(ctx context.Context, r webhook.Request) error {
	headers, err := encodeHeaders(r.Headers)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO requests
		(id, received_at, method, path, query, headers, body, body_sha256, seen_body, provider, event_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ReceivedAt.UnixMilli(), r.Method, r.Path, r.Query, headers, nonNil(r.Body), r.BodySHA256,
		r.SeenBody, nullString(r.Provider), nullString(r.EventType))
	return err
}

const requestColumns = `id, received_at, method, path, query, headers, body, body_sha256, seen_body, provider, event_type`

func (s *Store) FindRequest(ctx context.Context, idOrPrefix string) (webhook.Request, error) {
	id := strings.ToUpper(strings.TrimSpace(idOrPrefix))
	if id == "" || strings.ContainsFunc(id, func(r rune) bool { return !isIDRune(r) }) {
		return webhook.Request{}, webhook.ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+requestColumns+` FROM requests
		WHERE id = ?1 OR (id >= ?1 AND id < ?1 || '~') ORDER BY id = ?1 DESC LIMIT 2`, id)
	if err != nil {
		return webhook.Request{}, err
	}
	defer rows.Close()
	var found []webhook.Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return webhook.Request{}, err
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return webhook.Request{}, err
	}
	switch {
	case len(found) == 0:
		return webhook.Request{}, webhook.ErrNotFound
	case found[0].ID == id || len(found) == 1:
		return found[0], nil
	}
	return webhook.Request{}, fmt.Errorf("%w: %q", webhook.ErrAmbiguous, idOrPrefix)
}

func isIDRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z')
}

type scanner interface{ Scan(dest ...any) error }

func scanRequest(row scanner) (webhook.Request, error) {
	var (
		r                   webhook.Request
		receivedAt          int64
		headers             string
		provider, eventType sql.NullString
	)
	if err := row.Scan(&r.ID, &receivedAt, &r.Method, &r.Path, &r.Query, &headers, &r.Body, &r.BodySHA256,
		&r.SeenBody, &provider, &eventType); err != nil {
		return r, err
	}
	r.ReceivedAt = time.UnixMilli(receivedAt)
	r.Provider, r.EventType = provider.String, eventType.String
	if r.Body == nil {
		r.Body = []byte{}
	}
	var err error
	r.Headers, err = decodeHeaders(headers)
	return r, err
}

func (s *Store) ListRequests(ctx context.Context, f usecase.ListFilter) ([]usecase.RequestSummary, error) {
	failed := 0
	if f.FailedOnly {
		failed = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.received_at, r.method, r.path, r.query, r.headers, r.body_sha256, r.provider, r.event_type,
		       a.id, a.kind, a.target_url, a.resigned, a.started_at, a.duration_ms, a.status_code, a.error,
		       v.id, v.provider, v.ok, v.reason, v.details, v.checked_at
		FROM requests r
		LEFT JOIN attempts a ON a.id = (
			SELECT id FROM attempts WHERE request_id = r.id ORDER BY started_at DESC, id DESC LIMIT 1)
		LEFT JOIN verifications v ON v.id = (
			SELECT id FROM verifications WHERE request_id = r.id ORDER BY checked_at DESC, id DESC LIMIT 1)
		WHERE (?1 = '' OR r.provider = ?1)
		  AND (?2 = '' OR r.event_type = ?2)
		  AND (?3 = '' OR r.id < ?3)
		  AND (?4 = 0 OR coalesce(a.error, '') != '' OR coalesce(a.status_code, 0) >= 400 OR v.ok = 0)
		ORDER BY r.id DESC
		LIMIT ?5`, f.Provider, f.EventType, strings.ToUpper(f.BeforeID), failed, max(f.Limit, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []usecase.RequestSummary
	for rows.Next() {
		var (
			sum                                usecase.RequestSummary
			r                                  = &sum.Request
			receivedAt                         int64
			headers                            string
			provider, eventType                sql.NullString
			aID, aKind, aTarget, aErr          sql.NullString
			aResigned, aStarted, aDur, aStatus sql.NullInt64
			vID, vProvider, vReason, vDetails  sql.NullString
			vOK, vChecked                      sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &receivedAt, &r.Method, &r.Path, &r.Query, &headers, &r.BodySHA256, &provider, &eventType,
			&aID, &aKind, &aTarget, &aResigned, &aStarted, &aDur, &aStatus, &aErr,
			&vID, &vProvider, &vOK, &vReason, &vDetails, &vChecked); err != nil {
			return nil, err
		}
		r.ReceivedAt = time.UnixMilli(receivedAt)
		r.Provider, r.EventType = provider.String, eventType.String
		if r.Headers, err = decodeHeaders(headers); err != nil {
			return nil, err
		}
		if aID.Valid {
			sum.LastAttempt = &webhook.Attempt{
				ID: aID.String, RequestID: r.ID, Kind: webhook.AttemptKind(aKind.String), TargetURL: aTarget.String,
				Resigned: aResigned.Int64 == 1, StartedAt: time.UnixMilli(aStarted.Int64),
				Duration: time.Duration(aDur.Int64) * time.Millisecond, StatusCode: int(aStatus.Int64), Error: aErr.String,
			}
		}
		if vID.Valid {
			sum.Verification = &webhook.Verification{
				ID: vID.String, RequestID: r.ID, Provider: vProvider.String, OK: vOK.Int64 == 1,
				Reason: vReason.String, Details: vDetails.String, CheckedAt: time.UnixMilli(vChecked.Int64),
			}
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

func (s *Store) SetSeenBody(ctx context.Context, id string, body []byte) error {
	res, err := s.db.ExecContext(ctx, `UPDATE requests SET seen_body = ? WHERE id = ?`, nonNil(body), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return webhook.ErrNotFound
	}
	return nil
}

func (s *Store) SaveAttempt(ctx context.Context, a webhook.Attempt) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO attempts
		(id, request_id, kind, target_url, resigned, started_at, duration_ms, status_code, response_body, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.RequestID, string(a.Kind), a.TargetURL, boolInt(a.Resigned), a.StartedAt.UnixMilli(),
		a.Duration.Milliseconds(), nullInt(a.StatusCode), a.ResponseBody, nullString(a.Error))
	return err
}

func (s *Store) ListAttempts(ctx context.Context, requestID string) ([]webhook.Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, request_id, kind, target_url, resigned, started_at, duration_ms,
		status_code, response_body, error FROM attempts WHERE request_id = ? ORDER BY started_at, id`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []webhook.Attempt
	for rows.Next() {
		var (
			a                 webhook.Attempt
			kind              string
			resigned, started int64
			duration, status  sql.NullInt64
			errText           sql.NullString
		)
		if err := rows.Scan(&a.ID, &a.RequestID, &kind, &a.TargetURL, &resigned, &started, &duration, &status,
			&a.ResponseBody, &errText); err != nil {
			return nil, err
		}
		a.Kind, a.Resigned, a.StartedAt = webhook.AttemptKind(kind), resigned == 1, time.UnixMilli(started)
		a.Duration = time.Duration(duration.Int64) * time.Millisecond
		a.StatusCode, a.Error = int(status.Int64), errText.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SaveVerification(ctx context.Context, v webhook.Verification) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO verifications (id, request_id, provider, ok, reason, details, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.RequestID, v.Provider, boolInt(v.OK), v.Reason, nullString(v.Details), v.CheckedAt.UnixMilli())
	return err
}

func (s *Store) LatestVerification(ctx context.Context, requestID string) (*webhook.Verification, error) {
	var (
		v           webhook.Verification
		ok, checked int64
		details     sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, request_id, provider, ok, reason, details, checked_at
		FROM verifications WHERE request_id = ? ORDER BY checked_at DESC, id DESC LIMIT 1`, requestID).
		Scan(&v.ID, &v.RequestID, &v.Provider, &ok, &v.Reason, &details, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.OK, v.Details, v.CheckedAt = ok == 1, details.String, time.UnixMilli(checked)
	return &v, nil
}

func (s *Store) Prune(ctx context.Context, before time.Time, keep int) (int64, error) {
	var total int64
	if !before.IsZero() {
		res, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE received_at < ?`, before.UnixMilli())
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	if keep > 0 {
		res, err := s.db.ExecContext(ctx, `DELETE FROM requests WHERE id NOT IN (
			SELECT id FROM requests ORDER BY id DESC LIMIT ?)`, keep)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func encodeHeaders(h webhook.Headers) (string, error) {
	pairs := make([][2]string, len(h))
	for i, kv := range h {
		pairs[i] = [2]string{kv.Name, kv.Value}
	}
	b, err := json.Marshal(pairs)
	return string(b), err
}

func decodeHeaders(s string) (webhook.Headers, error) {
	var pairs [][2]string
	if err := json.Unmarshal([]byte(s), &pairs); err != nil {
		return nil, fmt.Errorf("decode headers: %w", err)
	}
	if len(pairs) == 0 {
		return nil, nil
	}
	h := make(webhook.Headers, len(pairs))
	for i, p := range pairs {
		h[i] = webhook.Header{Name: p[0], Value: p[1]}
	}
	return h, nil
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func nullInt(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: n != 0} }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
