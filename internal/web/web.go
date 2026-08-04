// Package web serves the Markdown knowledge library as a small local
// website. It renders .md files to HTML on the fly with goldmark, shows
// .txt transcripts as preformatted text, and serves everything else raw.
//
// Every page carries a sidebar listing the library's topics and meetings so
// the whole library is navigable from anywhere. The server is read-only and
// self-contained: no external assets, no CDN, no JavaScript. It is meant to
// be started on demand (patro run web) and stopped with Ctrl+C, not to run
// as a background service.
package web

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/vectors"
)

// pageTemplate wraps rendered content in a minimal, theme-aware HTML shell
// with a navigation sidebar. Everything is inlined so the page works fully
// offline.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body {
  margin: 0;
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  color: #1a1a1a; background: #fdfdfd;
}
.layout { display: flex; align-items: flex-start; max-width: 72rem; margin: 0 auto; }
aside {
  position: sticky; top: 0; align-self: flex-start;
  width: 17rem; flex: 0 0 17rem; height: 100vh; overflow-y: auto;
  padding: 1.5rem 1rem; border-right: 1px solid #e5e5e5; background: #f7f7f7;
  font-size: 0.9rem;
}
aside .home { display: block; font-weight: 600; margin-bottom: 1rem; color: #1a1a1a; text-decoration: none; }
aside .section { text-transform: uppercase; letter-spacing: 0.05em; font-size: 0.72rem; color: #888; margin: 1.2rem 0 0.4rem; }
aside ul { list-style: none; margin: 0; padding: 0; }
aside li { margin: 0.15rem 0; }
aside a { color: #444; text-decoration: none; display: block; padding: 0.15rem 0.4rem; border-radius: 4px; }
aside a:hover { background: rgba(0,0,0,0.06); }
aside a.active { background: #2563eb; color: #fff; }
main { flex: 1 1 auto; min-width: 0; padding: 2rem 2rem 4rem; max-width: 52rem; }
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
@media (max-width: 720px) {
  .layout { flex-direction: column; }
  aside { position: static; width: 100%; flex-basis: auto; height: auto; border-right: none; border-bottom: 1px solid #e5e5e5; }
  main { padding: 1.5rem 1.25rem 3rem; }
}
@media (prefers-color-scheme: dark) {
  body { color: #e4e4e4; background: #1a1a1a; }
  aside { background: #202020; border-right-color: #333; }
  aside .home { color: #e4e4e4; }
  aside a { color: #bbb; }
  aside a:hover { background: rgba(255,255,255,0.08); }
  a { color: #6ba3ff; }
  h1 { border-bottom-color: #333; }
  code { background: #2a2a2a; }
  pre { background: #222; }
  blockquote { border-left-color: #444; color: #aaa; }
  th, td { border-color: #333; }
  @media (max-width: 720px) { aside { border-bottom-color: #333; } }
}
</style>
</head>
<body>
<div class="layout">
<aside>{{.Sidebar}}</aside>
<main>{{.Body}}</main>
</div>
</body>
</html>`))

// Server renders and serves the knowledge library rooted at Root.
type Server struct {
	Root string
	md   goldmark.Markdown

	// SearchIndex, Vectors and Embedder power the read-only /search route
	// (design D3/D5). All three are optional and independently nil-able:
	// the caller (cmd/patro's `run web`) attaches whatever it managed to
	// open. When SearchIndex is nil, /search reports it is not available
	// yet; when only Vectors/Embedder are nil, results degrade to BM25-only
	// (design "Migration / Rollout") — /search never fails with a 500
	// because these are unset.
	SearchIndex  *searchindex.Index
	Vectors      *vectors.Store
	Embedder     embed.Embedder
	MultiVectors interface {
		NearestRepresentations(context.Context, embed.Representation, embed.ScoreMode, int) ([]embed.RankedResult, error)
	}
	Representer interface {
		Represent(context.Context, embed.Document) (*embed.Representation, error)
	}
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

	if rel == "search" {
		s.handleSearch(w, r)
		return
	}

	full := filepath.Join(s.Root, rel)

	// Guard against path traversal: the resolved path must stay under Root.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(full)
	if err != nil {
		s.notFound(w, r)
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
	s.render(w, r, title, template.HTML(b.String()))
}

// serveMarkdown renders a Markdown file to HTML inside the page shell.
func (s *Server) serveMarkdown(w http.ResponseWriter, r *http.Request, full string) {
	data, err := os.ReadFile(full)
	if err != nil {
		s.notFound(w, r)
		return
	}
	var buf bytes.Buffer
	if err := s.md.Convert(data, &buf); err != nil {
		logging.Errorf("web: cannot render %s: %v", full, err)
		http.Error(w, "cannot render markdown", http.StatusInternalServerError)
		return
	}
	s.render(w, r, titleFor(full), template.HTML(buf.String()))
}

// serveText shows a plain-text file (e.g. a transcript) as preformatted,
// escaped text inside the page shell.
func (s *Server) serveText(w http.ResponseWriter, r *http.Request, full string) {
	data, err := os.ReadFile(full)
	if err != nil {
		s.notFound(w, r)
		return
	}
	body := "<h1>" + template.HTMLEscapeString(titleFor(full)) + "</h1>\n<pre>" +
		template.HTMLEscapeString(string(data)) + "</pre>"
	s.render(w, r, titleFor(full), template.HTML(body))
}

// rrfK is the reciprocal-rank-fusion damping constant (design D3). 60 is
// the value from the original RRF paper and is a common default: it flattens
// the influence of rank 1 vs. rank 2 without needing per-ranker score
// normalization (BM25 and cosine scores are not on comparable scales).
const rrfK = 60

// searchResultLimit bounds how many hits are requested from each ranker
// before fusion.
const searchResultLimit = 50

// searchResult is one fused, render-ready hit.
type searchResult struct {
	ID    string // "topic:slug" or "meeting:slug"
	Kind  string
	Title string
	URL   string
}

// handleSearch serves the read-only /search route: a query form plus, once
// a query is submitted, ranked hits fusing BM25 (internal/searchindex) and
// cosine similarity (internal/vectors) via reciprocal-rank fusion. It never
// fails with a 500 — an unavailable index/store degrades to fewer results
// or an explicit message, per design "Migration / Rollout".
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var b strings.Builder
	b.WriteString("<h1>Search</h1>\n")
	b.WriteString(`<form method="get" action="/search">` +
		`<input type="text" name="q" value="` + template.HTMLEscapeString(q) +
		`" placeholder="Search topics and meetings" style="width:100%;padding:0.5rem;font-size:1rem;">` +
		`<button type="submit" style="margin-top:0.5rem;">Search</button></form>`)

	switch {
	case q == "":
		// No query yet: just the form.
	case s.SearchIndex == nil && s.Vectors == nil && s.MultiVectors == nil:
		b.WriteString(`<p>Search index is not available yet. Run "patro reconcile" (or start ` +
			`"patro serve") to build it.</p>`)
	default:
		results := s.rankedResults(r.Context(), q)
		if len(results) == 0 {
			b.WriteString("<p>No results.</p>\n")
		} else {
			b.WriteString("<ol>\n")
			for _, res := range results {
				b.WriteString(`<li><a href="` + template.HTMLEscapeString(res.URL) + `">` +
					template.HTMLEscapeString(res.Title) + `</a> <small>(` +
					template.HTMLEscapeString(res.Kind) + `)</small></li>` + "\n")
			}
			b.WriteString("</ol>\n")
		}
	}

	s.render(w, r, "Search", template.HTML(b.String()))
}

// rankedResults runs both rankers (whichever are available), fuses their
// ranked ID lists with reciprocal-rank fusion, and resolves each fused ID to
// render-ready metadata. Either ranker failing (including a nil field) is
// treated as "no hits from that ranker", never an error response.
func (s *Server) rankedResults(ctx context.Context, q string) []searchResult {
	var bm25IDs, vecIDs []string
	meta := map[string]searchResult{}

	if s.SearchIndex != nil {
		hits, err := s.SearchIndex.Query(q, searchResultLimit)
		if err != nil {
			logging.Warnf("web: search index query failed: %v", err)
		}
		for _, h := range hits {
			bm25IDs = append(bm25IDs, h.ID)
			meta[h.ID] = searchResult{ID: h.ID, Kind: h.Kind, Title: h.Title, URL: urlFor(h.Kind, h.ID)}
		}
	}

	if s.MultiVectors != nil && s.Representer != nil {
		representation, err := s.Representer.Represent(ctx, embed.Document{ID: "query", Text: "# Query\n\n" + q})
		if err != nil {
			logging.Warnf("web: representation search query failed: %v", err)
		} else {
			hits, err := s.MultiVectors.NearestRepresentations(ctx, *representation, embed.DirectedMode, searchResultLimit)
			if err != nil {
				logging.Warnf("web: multi-vector search failed: %v", err)
			} else {
				for _, h := range hits {
					id := searchindex.KindTopic + ":" + h.ID
					vecIDs = append(vecIDs, id)
					if _, ok := meta[id]; !ok {
						meta[id] = searchResult{ID: id, Kind: searchindex.KindTopic, Title: s.topicTitle(h.ID), URL: urlFor(searchindex.KindTopic, id)}
					}
				}
			}
		}
	} else if s.Vectors != nil && s.Embedder != nil {
		if vec, err := s.Embedder.Embed(ctx, q); err != nil {
			logging.Warnf("web: embedding search query failed: %v", err)
		} else {
			hits, err := s.Vectors.Nearest(vec, searchResultLimit)
			if err != nil {
				logging.Warnf("web: vector search failed: %v", err)
			}
			for _, h := range hits {
				// The vector store only ever holds topic embeddings
				// (design D2); tag its bare slugs into the same
				// "kind:slug" ID space searchindex uses so both rankers
				// can be fused by a single ID.
				id := searchindex.KindTopic + ":" + h.ID
				vecIDs = append(vecIDs, id)
				if _, ok := meta[id]; !ok {
					meta[id] = searchResult{
						ID:    id,
						Kind:  searchindex.KindTopic,
						Title: s.topicTitle(h.ID),
						URL:   urlFor(searchindex.KindTopic, id),
					}
				}
			}
		}
	}

	fused := reciprocalRankFusion(rrfK, bm25IDs, vecIDs)

	results := make([]searchResult, 0, len(fused))
	for _, id := range fused {
		results = append(results, meta[id])
	}
	return results
}

// reciprocalRankFusion fuses any number of already-ranked ID lists into one
// list ordered by descending fused score: score(id) = sum over rankers of
// 1/(k+rank), rank being 1-based (design D3). Ties break on ID for a
// deterministic order.
func reciprocalRankFusion(k int, rankings ...[]string) []string {
	scores := map[string]float64{}
	for _, ranking := range rankings {
		for i, id := range ranking {
			scores[id] += 1.0 / float64(k+i+1)
		}
	}

	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids
}

// urlFor builds the library-relative link for a "kind:slug" search hit ID.
func urlFor(kind, id string) string {
	slug := strings.TrimPrefix(id, kind+":")
	return "/" + kind + "s/" + slug + ".md"
}

// topicTitle resolves a topic slug's display title straight from its
// markdown file, mirroring headingOrStem — used when a hit reaches the
// results list only via the vector ranker and has no BM25 Hit to borrow
// Title/Kind from.
func (s *Server) topicTitle(slug string) string {
	return headingOrStem(filepath.Join(s.Root, "topics", slug+".md"))
}

// notFound renders a 404 page that still carries the navigation sidebar.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "Not found", template.HTML("<h1>Not found</h1>\n<p>No page at this address.</p>"))
}

// render writes the page shell with the given title, body and a sidebar
// whose active entry matches the current request path.
func (s *Server) render(w http.ResponseWriter, r *http.Request, title string, body template.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTemplate.Execute(w, struct {
		Title   string
		Sidebar template.HTML
		Body    template.HTML
	}{Title: title, Sidebar: s.buildSidebar(r.URL.Path), Body: body})
}

// navItem is a single sidebar link.
type navItem struct {
	URL   string
	Label string
}

// buildSidebar renders the navigation sidebar: a home link plus the Topics
// and Meetings sections read from the library. The entry whose URL matches
// active is highlighted.
func (s *Server) buildSidebar(active string) template.HTML {
	homeClass := ""
	if active == "/" || active == "/index.md" {
		homeClass = " active"
	}
	var b strings.Builder
	b.WriteString(`<a class="home` + homeClass + `" href="/">Knowledge library</a>`)
	s.writeSection(&b, "Topics", s.listSection("topics", false), active)
	s.writeSection(&b, "Meetings", s.listSection("meetings", true), active)
	return template.HTML(b.String())
}

// writeSection appends a titled list of nav items, skipping empty sections.
func (s *Server) writeSection(b *strings.Builder, title string, items []navItem, active string) {
	if len(items) == 0 {
		return
	}
	b.WriteString(`<div class="section">` + template.HTMLEscapeString(title) + `</div><ul>`)
	for _, it := range items {
		cls := ""
		if active == it.URL {
			cls = ` class="active"`
		}
		b.WriteString(`<li><a` + cls + ` href="` + template.HTMLEscapeString(it.URL) + `">` +
			template.HTMLEscapeString(it.Label) + `</a></li>`)
	}
	b.WriteString(`</ul>`)
}

// listSection lists the *.md files under dir as nav items labelled by their
// first heading (falling back to the file stem). When newestFirst is true
// the files are sorted by name descending (meetings are date-prefixed).
func (s *Server) listSection(dir string, newestFirst bool) []navItem {
	files, err := filepath.Glob(filepath.Join(s.Root, dir, "*.md"))
	if err != nil {
		return nil
	}
	sort.Slice(files, func(i, j int) bool {
		if newestFirst {
			return files[i] > files[j]
		}
		return files[i] < files[j]
	})
	items := make([]navItem, 0, len(files))
	for _, f := range files {
		items = append(items, navItem{
			URL:   "/" + dir + "/" + filepath.Base(f),
			Label: headingOrStem(f),
		})
	}
	return items
}

// headingOrStem returns a file's first "# " heading, or its base name
// without extension when there is no heading or the file is unreadable.
func headingOrStem(full string) string {
	if data, err := os.ReadFile(full); err == nil {
		line := string(data)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return titleFor(full)
}

// titleFor derives a page title from a file's base name.
func titleFor(full string) string {
	base := filepath.Base(full)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
