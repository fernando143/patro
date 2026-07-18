// Package web serves the Markdown knowledge library as a small local
// website. It renders .md files to HTML on the fly with goldmark, shows
// .txt transcripts as preformatted text, and serves everything else raw.
//
// The server is read-only and self-contained: no external assets, no CDN,
// no JavaScript. It is meant to be started on demand (patro run web) and
// stopped with Ctrl+C, not to run as a background service.
package web

import (
	"bytes"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/fernando143/patro/internal/logging"
)

// pageTemplate wraps rendered content in a minimal, theme-aware HTML shell.
// Everything is inlined so the page works fully offline.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; }
body {
  max-width: 52rem; margin: 0 auto; padding: 2rem 1.25rem 4rem;
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  color: #1a1a1a; background: #fdfdfd;
}
nav { margin-bottom: 2rem; font-size: 0.9rem; }
nav a { color: #555; text-decoration: none; }
nav a:hover { text-decoration: underline; }
a { color: #2563eb; }
h1, h2, h3 { line-height: 1.25; }
h1 { border-bottom: 1px solid #e5e5e5; padding-bottom: 0.3rem; }
code { background: #f0f0f0; padding: 0.1em 0.3em; border-radius: 3px; font-size: 0.9em; }
pre {
  background: #f6f6f6; padding: 1rem; border-radius: 6px; overflow-x: auto;
  white-space: pre-wrap; word-wrap: break-word;
}
pre code { background: none; padding: 0; }
blockquote { border-left: 3px solid #ddd; margin-left: 0; padding-left: 1rem; color: #555; }
table { border-collapse: collapse; }
th, td { border: 1px solid #ddd; padding: 0.4rem 0.6rem; }
@media (prefers-color-scheme: dark) {
  body { color: #e4e4e4; background: #1a1a1a; }
  nav a { color: #aaa; }
  a { color: #6ba3ff; }
  h1 { border-bottom-color: #333; }
  code { background: #2a2a2a; }
  pre { background: #222; }
  blockquote { border-left-color: #444; color: #aaa; }
  th, td { border-color: #333; }
}
</style>
</head>
<body>
<nav><a href="/">&larr; Knowledge library home</a></nav>
{{.Body}}
</body>
</html>`))

// Server renders and serves the knowledge library rooted at Root.
type Server struct {
	Root string
	md   goldmark.Markdown
}

// NewServer returns a Server that serves the library under root. Root is
// expected to be an absolute path.
func NewServer(root string) *Server {
	return &Server{
		Root: root,
		md:   goldmark.New(goldmark.WithExtensions(extension.GFM)),
	}
}

// ServeHTTP resolves the request path within Root and dispatches on file
// type: directories serve their index.md (or a listing), .md files are
// rendered, .txt files are shown as preformatted text, everything else is
// served raw. Requests that escape Root are rejected.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rel := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	full := filepath.Join(s.Root, rel)

	// Guard against path traversal: the resolved path must stay under Root.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if info.IsDir() {
		s.serveDir(w, r, full, rel)
		return
	}

	switch strings.ToLower(filepath.Ext(full)) {
	case ".md", ".markdown":
		s.serveMarkdown(w, r, full)
	case ".txt":
		s.serveText(w, r, full)
	default:
		http.ServeFile(w, r, full)
	}
}

// serveDir renders the directory's index.md when present, otherwise a
// simple listing of the entries.
func (s *Server) serveDir(w http.ResponseWriter, r *http.Request, full, rel string) {
	index := filepath.Join(full, "index.md")
	if _, err := os.Stat(index); err == nil {
		s.serveMarkdown(w, r, index)
		return
	}

	entries, err := os.ReadDir(full)
	if err != nil {
		http.Error(w, "cannot read directory", http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	title := rel
	if title == "" {
		title = "Knowledge library"
	}
	b.WriteString("<h1>" + template.HTMLEscapeString(title) + "</h1>\n<ul>\n")
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(`<li><a href="` + template.HTMLEscapeString(name) + `">` +
			template.HTMLEscapeString(name) + "</a></li>\n")
	}
	b.WriteString("</ul>\n")
	s.render(w, title, template.HTML(b.String()))
}

// serveMarkdown renders a Markdown file to HTML inside the page shell.
func (s *Server) serveMarkdown(w http.ResponseWriter, r *http.Request, full string) {
	data, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	if err := s.md.Convert(data, &buf); err != nil {
		logging.Errorf("web: cannot render %s: %v", full, err)
		http.Error(w, "cannot render markdown", http.StatusInternalServerError)
		return
	}
	s.render(w, titleFor(full), template.HTML(buf.String()))
}

// serveText shows a plain-text file (e.g. a transcript) as preformatted,
// escaped text inside the page shell.
func (s *Server) serveText(w http.ResponseWriter, r *http.Request, full string) {
	data, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body := "<h1>" + template.HTMLEscapeString(titleFor(full)) + "</h1>\n<pre>" +
		template.HTMLEscapeString(string(data)) + "</pre>"
	s.render(w, titleFor(full), template.HTML(body))
}

// render writes the page shell with the given title and body.
func (s *Server) render(w http.ResponseWriter, title string, body template.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTemplate.Execute(w, struct {
		Title string
		Body  template.HTML
	}{Title: title, Body: body})
}

// titleFor derives a page title from a file's base name.
func titleFor(full string) string {
	base := filepath.Base(full)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
