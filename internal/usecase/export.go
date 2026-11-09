package usecase

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/grintzdel/spool/internal/domain/webhook"
)

type Format string

const (
	FormatCurl Format = "curl"
	FormatJSON Format = "json"
	FormatHTTP Format = "http"
)

const DefaultCaptureURL = "http://127.0.0.1:7777"

func Export(req webhook.Request, format Format, target string) (string, error) {
	switch format {
	case FormatCurl:
		return exportCurl(req, target), nil
	case FormatJSON:
		return exportJSON(req)
	case FormatHTTP:
		return exportHTTP(req), nil
	}
	return "", fmt.Errorf("unknown format %q (curl, json, http)", format)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func exportCurl(req webhook.Request, target string) string {
	if target == "" {
		target = DefaultCaptureURL
	}
	parts := []string{"curl -X " + req.Method + " " + shellQuote(req.URLOn(target))}
	for _, h := range outgoingHeaders(req.Headers) {
		parts = append(parts, "-H "+shellQuote(h.Name+": "+h.Value))
	}
	prefix := ""
	switch {
	case len(req.Body) == 0:
	case utf8.Valid(req.Body):
		parts = append(parts, "--data-binary "+shellQuote(string(req.Body)))
	default:
		prefix = "printf '%s' " + shellQuote(base64.StdEncoding.EncodeToString(req.Body)) + " | base64 -d | "
		parts = append(parts, "--data-binary @-")
	}
	return prefix + strings.Join(parts, " \\\n  ") + "\n"
}

type exportedRequest struct {
	ID           string      `json:"id"`
	ReceivedAt   string      `json:"received_at"`
	Method       string      `json:"method"`
	Path         string      `json:"path"`
	Query        string      `json:"query"`
	Headers      [][2]string `json:"headers"`
	Body         string      `json:"body"`
	BodyEncoding string      `json:"body_encoding"`
	BodySHA256   string      `json:"body_sha256"`
	Provider     string      `json:"provider,omitempty"`
	EventType    string      `json:"event_type,omitempty"`
}

func exportJSON(req webhook.Request) (string, error) {
	out := exportedRequest{
		ID:           req.ID,
		ReceivedAt:   req.ReceivedAt.UTC().Format(time.RFC3339Nano),
		Method:       req.Method,
		Path:         req.Path,
		Query:        req.Query,
		Headers:      make([][2]string, 0, len(req.Headers)),
		Body:         string(req.Body),
		BodyEncoding: "utf8",
		BodySHA256:   req.BodySHA256,
		Provider:     req.Provider,
		EventType:    req.EventType,
	}
	if !utf8.Valid(req.Body) {
		out.Body, out.BodyEncoding = base64.StdEncoding.EncodeToString(req.Body), "base64"
	}
	for _, h := range req.Headers {
		out.Headers = append(out.Headers, [2]string{h.Name, h.Value})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

func exportHTTP(req webhook.Request) string {
	var b strings.Builder
	target := req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	b.WriteString(req.Method + " " + target + " HTTP/1.1\r\n")
	for _, h := range req.Headers.Without("Content-Length", "Transfer-Encoding") {
		b.WriteString(h.Name + ": " + h.Value + "\r\n")
	}
	b.WriteString("Content-Length: " + strconv.Itoa(len(req.Body)) + "\r\n\r\n")
	b.Write(req.Body)
	return b.String()
}
