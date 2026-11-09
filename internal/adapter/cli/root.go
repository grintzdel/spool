package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/grintzdel/spool/internal/adapter/config"
	"github.com/grintzdel/spool/internal/bootstrap"
	"github.com/grintzdel/spool/internal/domain/webhook"
)

type globals struct {
	configPath string
	dbPath     string
}

func NewRootCommand(version string) *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:           "spool",
		Short:         "Local webhook inbox: capture, history, replay and signature diagnosis",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.configPath, "config", config.DefaultFile, "config file")
	root.PersistentFlags().StringVar(&g.dbPath, "db", "", "SQLite database path (default $SPOOL_DB or ~/.local/share/spool/spool.db)")
	root.AddCommand(
		newServeCommand(g),
		newListCommand(g),
		newShowCommand(g),
		newVerifyCommand(g),
		newReplayCommand(g),
		newExportCommand(g),
		newPruneCommand(g),
	)
	return root
}

func Execute(ctx context.Context, version string) int {
	if err := NewRootCommand(version).ExecuteContext(ctx); err != nil {
		var exit exitError
		if !errors.As(err, &exit) {
			fmt.Fprintln(os.Stderr, "spool:", friendly(err))
		}
		return exit.code()
	}
	return 0
}

type exitError struct{ status int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.status) }

func (e exitError) code() int {
	if e.status == 0 {
		return 1
	}
	return e.status
}

func friendly(err error) string {
	if errors.Is(err, webhook.ErrNotFound) {
		return "no request matches this id (see `spool ls`)"
	}
	return err.Error()
}

func (g *globals) loadConfig() (config.Config, error) {
	cfg, err := config.Load(g.configPath)
	if err != nil {
		return cfg, err
	}
	if g.dbPath != "" {
		cfg.DB = g.dbPath
	}
	return cfg, nil
}

func (g *globals) open() (*bootstrap.App, error) {
	cfg, err := g.loadConfig()
	if err != nil {
		return nil, err
	}
	return bootstrap.New(cfg)
}

func withApp(g *globals, run func(cmd *cobra.Command, args []string, app *bootstrap.App) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		app, err := g.open()
		if err != nil {
			return err
		}
		defer app.Close()
		return run(cmd, args, app)
	}
}
