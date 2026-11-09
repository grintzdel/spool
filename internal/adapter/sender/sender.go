package sender

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/grintzdel/spool/internal/usecase"
)

type Client struct {
	http *http.Client
	sem  chan struct{}
}

var _ usecase.Sender = (*Client)(nil)

func New(timeout time.Duration, maxConcurrent int) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &Client{
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		sem: make(chan struct{}, max(maxConcurrent, 1)),
	}
}

func (c *Client) Send(ctx context.Context, out usecase.Outgoing) (usecase.Response, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return usecase.Response{}, ctx.Err()
	}

	req, err := http.NewRequestWithContext(ctx, out.Method, out.URL, bytes.NewReader(out.Body))
	if err != nil {
		return usecase.Response{}, err
	}
	for _, h := range out.Headers {
		req.Header[h.Name] = append(req.Header[h.Name], h.Value)
	}
	if !out.Headers.Has("User-Agent") {
		req.Header.Set("User-Agent", "")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return usecase.Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, usecase.MaxStoredResponse))
	_, _ = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return usecase.Response{}, err
	}
	return usecase.Response{StatusCode: resp.StatusCode, Body: body}, nil
}
