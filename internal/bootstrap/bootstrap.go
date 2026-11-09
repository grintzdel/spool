package bootstrap

import (
	"github.com/grintzdel/spool/internal/adapter/bus"
	"github.com/grintzdel/spool/internal/adapter/config"
	"github.com/grintzdel/spool/internal/adapter/sender"
	"github.com/grintzdel/spool/internal/adapter/sqlite"
	"github.com/grintzdel/spool/internal/adapter/system"
	"github.com/grintzdel/spool/internal/domain/signature"
	_ "github.com/grintzdel/spool/internal/domain/signature/github"
	_ "github.com/grintzdel/spool/internal/domain/signature/stripe"
	"github.com/grintzdel/spool/internal/usecase"
)

type App struct {
	Config    config.Config
	Store     *sqlite.Store
	Registry  *signature.Registry
	Bus       *bus.Bus
	Forwarder *usecase.Forwarder
	Capturer  usecase.Capturer
	Queries   usecase.Queries
	Verifier  *usecase.Verifier
	Replayer  usecase.Replayer
	Seen      usecase.SeenRecorder
	Pruner    usecase.Pruner
}

func New(cfg config.Config) (*App, error) {
	store, err := sqlite.Open(cfg.DB)
	if err != nil {
		return nil, err
	}
	generics, err := cfg.GenericProviders()
	if err != nil {
		store.Close()
		return nil, err
	}
	registry := signature.Default
	for _, p := range generics {
		registry.Register(p)
	}

	var (
		events  = bus.New(64)
		clock   = system.Clock{}
		ids     = system.NewULIDs()
		secrets = cfg.Secrets()
	)
	forwarder := &usecase.Forwarder{
		Store: store, Sender: sender.New(cfg.Serve.Timeout.D(), cfg.Serve.Concurrency), Publisher: events,
		Clock: clock, IDs: ids, Registry: registry, Secrets: secrets,
	}
	verifier := &usecase.Verifier{
		Store: store, Registry: registry, Secrets: secrets, Clock: clock, IDs: ids, Publisher: events,
		Tolerance: cfg.Verify.Tolerance.D(),
	}
	return &App{
		Config:    cfg,
		Store:     store,
		Registry:  registry,
		Bus:       events,
		Forwarder: forwarder,
		Capturer:  usecase.Capturer{Store: store, Registry: registry, Publisher: events, Clock: clock, IDs: ids},
		Queries:   usecase.Queries{Store: store},
		Verifier:  verifier,
		Replayer: usecase.Replayer{
			Store: store, Forwarder: forwarder, Registry: registry, Clock: clock,
			DefaultTarget: cfg.Serve.Forward, Tolerance: cfg.Verify.Tolerance.D(),
		},
		Seen:   usecase.SeenRecorder{Store: store, Verifier: verifier, Publisher: events},
		Pruner: usecase.Pruner{Store: store, Clock: clock},
	}, nil
}

func (a *App) Close() error { return a.Store.Close() }
