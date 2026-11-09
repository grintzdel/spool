package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/grintzdel/spool/internal/usecase"
)

//go:embed assets templates
var content embed.FS

type Subscriber interface {
	Subscribe() (events <-chan usecase.Event, unsubscribe func())
}

type Deps struct {
	Queries      usecase.Queries
	Replayer     usecase.Replayer
	Verifier     *usecase.Verifier
	Seen         usecase.SeenRecorder
	Events       Subscriber
	ExportTarget string
}

type server struct {
	Deps
	pages *template.Template
}

func NewHandler(d Deps) http.Handler {
	s := &server{
		Deps:  d,
		pages: template.Must(template.ParseFS(content, "templates/*.html")),
	}
	assets, err := fs.Sub(content, "assets")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.listPage)
	mux.HandleFunc("GET /r/{id}", s.detailPage)
	mux.HandleFunc("GET /api/requests", s.listRequests)
	mux.HandleFunc("GET /api/requests/{id}", s.showRequest)
	mux.HandleFunc("POST /api/requests/{id}/replay", s.replay)
	mux.HandleFunc("POST /api/requests/{id}/verify", s.verify)
	mux.HandleFunc("POST /api/requests/{id}/seen", s.seen)
	mux.HandleFunc("GET /api/events", s.events)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assets)))
	csrf := http.NewCrossOriginProtection()
	csrf.AddInsecureBypassPattern("POST /api/requests/{id}/seen")
	return csrf.Handler(mux)
}
