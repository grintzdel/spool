package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/grintzdel/spool/internal/adapter/capture"
	"github.com/grintzdel/spool/internal/adapter/config"
	"github.com/grintzdel/spool/internal/adapter/web"
	"github.com/grintzdel/spool/internal/bootstrap"
	"github.com/grintzdel/spool/internal/usecase"
)

func newServeCommand(g *globals) *cobra.Command {
	var (
		port, uiPort           int
		bind, forward, respond string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Capture webhooks, forward them to your app and serve the UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.loadConfig()
			if err != nil {
				return err
			}
			flags := cmd.Flags()
			if flags.Changed("port") {
				cfg.Serve.Port = port
			}
			if flags.Changed("ui-port") {
				cfg.Serve.UIPort = uiPort
			}
			if flags.Changed("bind") {
				cfg.Serve.Bind = bind
			}
			if flags.Changed("forward") {
				cfg.Serve.Forward = forward
			}
			if flags.Changed("respond") {
				cfg.Serve.Respond = respond
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			app, err := bootstrap.New(cfg)
			if err != nil {
				return err
			}
			defer app.Close()
			return serve(cmd.Context(), app, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := cmd.Flags()
	f.IntVar(&port, "port", 7777, "capture port")
	f.StringVar(&bind, "bind", "127.0.0.1", "capture bind address (use 0.0.0.0 to expose it, at your own risk)")
	f.StringVar(&forward, "forward", "", "forward every captured request to this base URL (e.g. http://localhost:3000)")
	f.StringVar(&respond, "respond", "ack", "ack: answer 200 at once; passthrough: wait for the app and relay its status")
	f.IntVar(&uiPort, "ui-port", 7778, "UI port, always bound to 127.0.0.1")
	return cmd
}

func serve(parent context.Context, app *bootstrap.App, stdout, stderr io.Writer) error {
	cfg := app.Config
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if n, err := app.Pruner.Prune(ctx, cfg.Retention.MaxAge.D(), cfg.Retention.MaxRequests); err != nil {
		logger.Warn("retention failed", "err", err)
	} else if n > 0 {
		logger.Info("retention applied", "deleted", n)
	}

	captureAddr := net.JoinHostPort(cfg.Serve.Bind, strconv.Itoa(cfg.Serve.Port))
	var lc net.ListenConfig
	captureLn, err := lc.Listen(ctx, "tcp", captureAddr)
	if err != nil {
		return fmt.Errorf("capture port: %w", err)
	}
	uiAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Serve.UIPort))
	uiLn, err := lc.Listen(ctx, "tcp", uiAddr)
	if err != nil {
		captureLn.Close()
		return fmt.Errorf("ui port: %w", err)
	}

	handler := capture.Handler{Capturer: app.Capturer, MaxBody: cfg.Serve.MaxBody, Logger: logger}
	var dispatcher *usecase.Dispatcher
	switch {
	case cfg.Serve.Forward == "":
	case cfg.Serve.Respond == "passthrough":
		handler.Passthrough, handler.Target = app.Forwarder, cfg.Serve.Forward
	default:
		dispatcher = usecase.NewDispatcher(app.Forwarder, cfg.Serve.Forward, cfg.Serve.Concurrency)
		handler.Queue = dispatcher
	}

	captureURL := "http://" + captureAddr
	exportTarget := cfg.Serve.Forward
	if exportTarget == "" {
		exportTarget = captureURL
	}
	baseCtx := func(net.Listener) context.Context { return ctx }
	captureSrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: baseCtx}
	uiSrv := &http.Server{
		Handler: web.NewHandler(web.Deps{
			Queries: app.Queries, Replayer: app.Replayer, Verifier: app.Verifier, Seen: app.Seen,
			Events: app.Bus, ExportTarget: exportTarget,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       baseCtx,
	}

	printBanner(stdout, cfg, captureURL, "http://"+uiAddr)
	if !isLoopback(cfg.Serve.Bind) {
		logger.Warn("capture port is reachable from the network: anyone on it can write to your history", "bind", cfg.Serve.Bind)
	}
	if cfg.Serve.Forward != "" && cfg.Serve.Respond == "passthrough" {
		logger.Warn("passthrough mode: a slow or failing app makes the provider retry, which creates duplicates")
	}

	errs := make(chan error, 2)
	go func() { errs <- captureSrv.Serve(captureLn) }()
	go func() { errs <- uiSrv.Serve(uiLn) }()

	select {
	case <-ctx.Done():
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "err", err)
		}
	}
	stop()
	fmt.Fprintln(stderr, "\nshutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = errors.Join(captureSrv.Shutdown(shutdownCtx), uiSrv.Shutdown(shutdownCtx))
	if dispatcher != nil {
		err = errors.Join(err, dispatcher.Close(shutdownCtx))
	}
	return err
}

func printBanner(w io.Writer, cfg config.Config, captureURL, uiURL string) {
	fmt.Fprintln(w, "Spool is listening")
	fmt.Fprintf(w, "  capture  %s  (any path)\n", captureURL)
	fmt.Fprintf(w, "  ui       %s\n", uiURL)
	if cfg.Serve.Forward != "" {
		fmt.Fprintf(w, "  forward  %s  (%s)\n", cfg.Serve.Forward, cfg.Serve.Respond)
	} else {
		fmt.Fprintln(w, "  forward  off  (use --forward http://localhost:3000)")
	}
	fmt.Fprintf(w, "  history  %s\n", cfg.DB)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
