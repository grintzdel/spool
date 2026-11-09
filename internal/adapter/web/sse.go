package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const heartbeat = 15 * time.Second

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	events, unsubscribe := s.Events.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(write func(io.Writer) error) bool {
		return write(w) == nil && rc.Flush() == nil
	}
	if !send(comment("connected")) {
		return
	}

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send(comment("ping")) {
				return
			}
		case e, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if !send(func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, data)
				return err
			}) {
				return
			}
		}
	}
}

func comment(text string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := fmt.Fprintf(w, ": %s\n\n", text)
		return err
	}
}
