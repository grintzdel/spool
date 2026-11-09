package web

import (
	"bytes"
	"net/http"

	"github.com/grintzdel/spool/internal/usecase"
)

func isPartial(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.pages.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *server) renderError(w http.ResponseWriter, err error) {
	status := statusOf(err)
	s.render(w, status, "error", errorView{Status: http.StatusText(status), Message: err.Error()})
}

func (s *server) listPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "HX-Request")
	f, err := parseFilter(r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	items, next, err := s.page(r, f)
	if err != nil {
		s.renderError(w, err)
		return
	}
	view := newListView(items, f, next, r.URL.Query())
	if isPartial(r) {
		s.render(w, http.StatusOK, "rows", view)
		return
	}
	s.render(w, http.StatusOK, "list", view)
}

func (s *server) detailPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "HX-Request")
	d, err := s.Queries.Show(r.Context(), r.PathValue("id"))
	if err != nil {
		s.renderError(w, err)
		return
	}
	if isPartial(r) {
		s.render(w, http.StatusOK, "live", newLiveView(d))
		return
	}
	curl, err := usecase.Export(d.Request, usecase.FormatCurl, s.ExportTarget)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, http.StatusOK, "detail", newDetailView(d, curl))
}
