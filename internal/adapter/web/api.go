package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

const (
	defaultLimit = 50
	maxLimit     = 500
	maxSeenBody  = 10 << 20
	maxJSONBody  = 1 << 20
	maxTimes     = 100
)

var errBadRequest = errors.New("bad request")

func badRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errBadRequest, fmt.Sprintf(format, args...))
}

func statusOf(err error) int {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, webhook.ErrNotFound):
		return http.StatusNotFound
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, webhook.ErrAmbiguous), errors.Is(err, usecase.ErrSeenMismatch), errors.Is(err, errBadRequest):
		return http.StatusBadRequest
	case errors.Is(err, usecase.ErrNoProvider), errors.Is(err, usecase.ErrUnknownProvider),
		errors.Is(err, usecase.ErrNoTarget), errors.Is(err, usecase.ErrNoSecret):
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, statusOf(err), map[string]string{"error": err.Error()})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil, errors.Is(err, io.EOF):
		return nil
	case errors.As(err, &tooLarge):
		return err
	}
	return badRequest("invalid JSON body: %v", err)
}

func parseFilter(r *http.Request) (usecase.ListFilter, error) {
	q := r.URL.Query()
	f := usecase.ListFilter{
		Provider:  q.Get("provider"),
		EventType: q.Get("event"),
		BeforeID:  q.Get("before"),
		Limit:     defaultLimit,
	}
	if v := q.Get("failed"); v != "" {
		failed, err := strconv.ParseBool(v)
		if err != nil {
			return f, badRequest("invalid failed %q", v)
		}
		f.FailedOnly = failed
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return f, badRequest("invalid limit %q", v)
		}
		f.Limit = min(n, maxLimit)
	}
	return f, nil
}

func (s *server) page(r *http.Request, f usecase.ListFilter) ([]usecase.RequestSummary, string, error) {
	limit := f.Limit
	f.Limit++
	items, err := s.Queries.List(r.Context(), f)
	if err != nil || len(items) <= limit {
		return items, "", err
	}
	items = items[:limit]
	return items, items[limit-1].Request.ID, nil
}

func (s *server) listRequests(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r)
	if err != nil {
		writeError(w, err)
		return
	}
	items, next, err := s.page(r, f)
	if err != nil {
		writeError(w, err)
		return
	}
	out := listJSON{Items: make([]summaryJSON, 0, len(items)), Next: next}
	for _, it := range items {
		out.Items = append(out.Items, toSummaryJSON(it))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) showRequest(w http.ResponseWriter, r *http.Request) {
	d, err := s.Queries.Show(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(d))
}

func (s *server) replay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Resign   bool   `json:"resign"`
		To       string `json:"to"`
		Times    int    `json:"times"`
		Provider string `json:"provider"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if in.Times < 0 || in.Times > maxTimes {
		writeError(w, badRequest("times must be between 1 and %d", maxTimes))
		return
	}
	res, err := s.Replayer.Replay(r.Context(), usecase.ReplayInput{
		ID: r.PathValue("id"), To: in.To, Times: in.Times, Resign: in.Resign, Marker: true, Provider: in.Provider,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	out := replayJSON{Attempts: make([]attemptJSON, 0, len(res.Attempts)), Warnings: nonNil(res.Warnings)}
	for _, a := range res.Attempts {
		out.Attempts = append(out.Attempts, toAttemptJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
	}
	if err := readJSON(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	rep, err := s.Verifier.Verify(r.Context(), usecase.VerifyInput{ID: r.PathValue("id"), Provider: in.Provider})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVerifyJSON(rep))
}

func (s *server) seen(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSeenBody))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.Seen.RecordSeen(r.Context(), r.PathValue("id"), body, r.Header.Get("X-Spool-Seen-Sha256")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
