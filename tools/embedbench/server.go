package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/fernando143/patro/internal/embed"
)

// pageTemplate renders the embedbench form and, once a report has been
// computed, a results table. Mirrors internal/web's shape: one inlined
// template.Must page, no external assets, no JavaScript.
//
// Scope note: this was originally designed as a cross-backend agreement
// matrix (3, later 2, embedding backends side by side). Both go-sentex and
// zerfoo were dropped before shipping and cybertron is the only backend
// that made it into the registry (see the topic-reconciliation design doc,
// D9 amendment), so the report below is a single-backend quality/
// performance instrument, not a comparison matrix. It iterates purely
// through embed.Available()/embed.New(name) — nothing here hardcodes
// "cybertron" — so it becomes a real A/B comparison again automatically if
// a second backend is ever registered.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>embedbench</title>
<style>
body { font: 15px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 48rem; margin: 2rem auto; padding: 0 1rem; color: #1a1a1a; }
h1 { margin-bottom: 0.2rem; }
p.sub { color: #666; margin-top: 0; }
textarea { width: 100%; font-family: inherit; padding: 0.5rem; }
label { display: block; margin: 1rem 0 0.25rem; font-weight: 600; }
button { margin-top: 1rem; padding: 0.5rem 1.2rem; }
table { border-collapse: collapse; margin-top: 1.5rem; width: 100%; }
th, td { border: 1px solid #ddd; padding: 0.4rem 0.6rem; text-align: left; }
.err { color: #b00020; margin-top: 1rem; }
</style>
</head>
<body>
<h1>embedbench</h1>
<p class="sub">Single-backend embedding quality/performance report &mdash; {{.BackendCount}} backend(s) compiled in: {{.Backends}}.</p>
<form method="get" action="/">
<label for="a">Text A</label>
<textarea id="a" name="a" rows="3">{{.A}}</textarea>
<label for="b">Text B (optional &mdash; cosine similarity is reported when set)</label>
<textarea id="b" name="b" rows="3">{{.B}}</textarea>
<button type="submit">Embed</button>
</form>
{{if .Err}}<p class="err">{{.Err}}</p>{{end}}
{{if .Rows}}
<table>
<tr><th>Backend</th><th>Dim</th><th>A chunks (title/content)</th><th>B chunks (title/content)</th><th>A→B</th><th>B→A</th><th>Symmetric</th><th>Fingerprint</th></tr>
{{range .Rows}}<tr><td>{{.Name}}</td><td>{{.Dim}}</td><td>{{.ATitleChunks}}/{{.AContentChunks}}</td><td>{{.BTitleChunks}}/{{.BContentChunks}}</td><td>{{.DirectedAB}}</td><td>{{.DirectedBA}}</td><td>{{.Symmetric}}</td><td><code>{{.Fingerprint}}</code></td></tr>{{end}}
</table>
{{end}}
</body>
</html>`))

// pageData is the template's view model.
type pageData struct {
	A, B         string
	Backends     string
	BackendCount int
	Rows         []reportRow
	Err          string
}

// reportRow is one backend's result line in the report table.
type reportRow struct {
	Name, Fingerprint                 string
	Dim, ATitleChunks, AContentChunks int
	BTitleChunks, BContentChunks      int
	TimeA, TimeB, Cosine              string
	DirectedAB, DirectedBA, Symmetric string
}

// Server serves the embedbench form and report. It carries no state: every
// backend is constructed fresh per request via embed.New, so the tool never
// pays weight-loading cost until someone actually submits text.
type Server struct{}

// NewServer returns an embedbench Server.
func NewServer() *Server { return &Server{} }

// ServeHTTP renders the form, and — once text A is submitted — a report
// table with each compiled-in backend's dimensionality, embed wall time,
// and (when text B is also set) cosine similarity.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	a := q.Get("a")
	b := q.Get("b")

	page := pageData{
		A:            a,
		B:            b,
		Backends:     strings.Join(embed.Available(), ", "),
		BackendCount: len(embed.Available()),
	}

	if strings.TrimSpace(a) != "" {
		rows, err := report(r.Context(), a, b)
		if err != nil {
			page.Err = err.Error()
		} else {
			page.Rows = rows
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTemplate.Execute(w, page)
}

// report runs every compiled-in embedding backend against a and, when b is
// non-empty, reports their cosine similarity. Backends are iterated purely
// via embed.Available()/embed.New(name) (see the package doc scope note) —
// this function has no backend-specific logic of its own.
func report(ctx context.Context, a, b string) ([]reportRow, error) {
	names := embed.Available()
	if len(names) == 0 {
		return nil, fmt.Errorf("no embedding backends compiled in")
	}

	withB := strings.TrimSpace(b) != ""
	rows := make([]reportRow, 0, len(names))
	for _, name := range names {
		e, err := embed.New(name)
		if err != nil {
			return nil, fmt.Errorf("backend %q: %w", name, err)
		}

		start := time.Now()
		va, err := represent(e, ctx, "a", a)
		if err != nil {
			return nil, fmt.Errorf("backend %q: represent A: %w", name, err)
		}
		row := reportRow{Name: e.Name(), Dim: e.Dim(), TimeA: time.Since(start).String(), Fingerprint: va.RepresentationFingerprint}
		row.ATitleChunks, row.AContentChunks = chunkCounts(*va)

		if withB {
			start = time.Now()
			vb, err := represent(e, ctx, "b", b)
			if err != nil {
				return nil, fmt.Errorf("backend %q: represent B: %w", name, err)
			}
			row.TimeB = time.Since(start).String()
			row.BTitleChunks, row.BContentChunks = chunkCounts(*vb)
			ab, err := embed.DirectedScore(ctx, *va, *vb)
			if err != nil {
				return nil, fmt.Errorf("backend %q: score A→B: %w", name, err)
			}
			ba, err := embed.DirectedScore(ctx, *vb, *va)
			if err != nil {
				return nil, fmt.Errorf("backend %q: score B→A: %w", name, err)
			}
			row.DirectedAB, row.DirectedBA = fmt.Sprintf("%.4f", ab), fmt.Sprintf("%.4f", ba)
			row.Symmetric = fmt.Sprintf("%.4f", minFloat(ab, ba))
			row.Cosine = row.Symmetric // retained for callers of the original report shape.
		}

		rows = append(rows, row)
	}
	return rows, nil
}

func represent(e embed.Embedder, ctx context.Context, side, text string) (*embed.Representation, error) {
	return e.Represent(ctx, embed.Document{ID: side, Text: text})
}

func chunkCounts(r embed.Representation) (title, content int) {
	for _, chunk := range r.Chunks {
		if chunk.Kind == "title" {
			title++
		} else if chunk.Kind == "content" {
			content++
		}
	}
	return title, content
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
