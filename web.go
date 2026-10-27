package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"time"
)

//go:embed web/*.html
var webFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"pct": func(votes, total uint64) int {
		if total == 0 {
			return 0
		}
		return int(votes * 100 / total)
	},
	"ago": func(t time.Time) string { return time.Since(t).Round(time.Second).String() },
}).ParseFS(webFS, "web/*.html"))

// Audit is published on every page so anyone can recount with a view-only wallet.
type Audit struct {
	PrimaryAddress string `json:"primary_address"`
	ViewKey        string `json:"view_key"`
	Network        string `json:"network"`
}

type Server struct {
	store   *Store
	tallier *Tallier
	audit   Audit
}

type pollView struct {
	Poll
	Tally
	Updated time.Time
	Audit   Audit
}

func (s *Server) view(p Poll) pollView {
	t, updated := s.tallier.Tally(p.ID)
	if t.Results == nil { // not tallied yet: still show the options
		for _, o := range p.Options {
			t.Results = append(t.Results, Result{Label: o.Label, Address: o.Address})
		}
	}
	return pollView{Poll: p, Tally: t, Updated: updated, Audit: s.audit}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /p/{id}", s.poll)
	mux.HandleFunc("GET /api/polls/{id}", s.apiPoll)
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	var views []pollView
	for _, p := range s.store.List() {
		views = append(views, s.view(p))
	}
	render(w, "index.html", map[string]any{"Polls": views, "Audit": s.audit})
}

func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	p, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	render(w, "poll.html", s.view(p))
}

func (s *Server) apiPoll(w http.ResponseWriter, r *http.Request) {
	p, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.view(p))
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}
