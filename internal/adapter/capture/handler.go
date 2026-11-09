package capture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/grintzdel/spool/internal/domain/webhook"
	"github.com/grintzdel/spool/internal/usecase"
)

type Queue interface {
	Enqueue(req webhook.Request)
}

type Deliverer interface {
	Deliver(ctx context.Context, req webhook.Request, opts usecase.DeliverOptions) (webhook.Attempt, error)
}

type Handler struct {
	Capturer    usecase.Capturer
	Queue       Queue
	Passthrough Deliverer
	Target      string
	MaxBody     int64
	Logger      *slog.Logger
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.MaxBody))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "body exceeds the capture limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	headers := append(webhook.Headers{{Name: "Host", Value: r.Host}}, webhook.FromHTTP(r.Header)...)

	req, err := h.Capturer.Capture(r.Context(), usecase.Incoming{
		Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Headers: headers, Body: body,
	})
	if err != nil {
		h.logger().Error("capture failed", "err", err)
		http.Error(w, "spool could not store the request", http.StatusInternalServerError)
		return
	}
	h.logger().Info("captured", "id", req.ID, "method", req.Method, "path", req.Path,
		"provider", req.Provider, "event", req.EventType, "bytes", len(req.Body))
	w.Header().Set(usecase.HeaderID, req.ID)

	if h.Passthrough != nil && h.Target != "" {
		h.relay(w, r.Context(), req)
		return
	}
	if h.Queue != nil {
		h.Queue.Enqueue(req)
	}
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(map[string]any{"received": true, "id": req.ID})
	if err != nil {
		return
	}
}

func (h Handler) relay(w http.ResponseWriter, ctx context.Context, req webhook.Request) {
	a, err := h.Passthrough.Deliver(ctx, req, usecase.DeliverOptions{Kind: webhook.AttemptForward, Target: h.Target})
	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case a.Error != "":
		http.Error(w, "spool could not reach the app: "+a.Error, http.StatusBadGateway)
	default:
		w.WriteHeader(a.StatusCode)
		_, err := w.Write(a.ResponseBody)
		if err != nil {
			return
		}
	}
}

func (h Handler) logger() *slog.Logger {
	if h.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return h.Logger
}
