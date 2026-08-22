// Package web serves the Markdown knowledge library as a small local
// website. It renders .md files to HTML on the fly with goldmark, shows
// .txt transcripts as preformatted text, and serves everything else raw.
//
// Every page carries compact library navigation and a global search so the
// whole library is retrievable from anywhere. The server is read-only and
// self-contained: no external assets, no CDN, no JavaScript. It is meant to
// be started on demand (patro run web) and stopped with Ctrl+C, not to run
// as a background service.
package web

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/layout"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/search"
	"github.com/fernando143/patro/internal/searchindex"
)

// pageTemplate wraps rendered content in a theme-aware, responsive shell.
// Everything is inlined so the page works fully offline.
var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root {
  color-scheme: light dark;
  --bg: #fbfbfc; --surface: #fff; --surface-subtle: #f4f5f7;
  --text: #17181a; --muted: #62666d; --border: #dedfe3;
  --accent: #245eea; --accent-strong: #1948bc; --focus: #ffbf47;
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; }
body {
  margin: 0;
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  color: var(--text); background: var(--bg);
}
.skip-link {
  position: fixed; left: 1rem; top: -5rem; z-index: 10;
  padding: .65rem 1rem; color: #fff; background: var(--accent-strong); border-radius: .5rem;
}
.skip-link:focus { top: 1rem; }
.topbar { position: sticky; top: 0; z-index: 5; border-bottom: 1px solid var(--border); background: color-mix(in srgb, var(--bg) 92%, transparent); backdrop-filter: blur(10px); }
.topbar-inner { display: flex; align-items: center; gap: 2rem; max-width: 76rem; margin: 0 auto; padding: .8rem 1.25rem; }
.brand { color: var(--text); font-size: 1.05rem; font-weight: 750; text-decoration: none; white-space: nowrap; }
.global-search { display: flex; flex: 1; max-width: 42rem; margin-left: auto; }
.global-search input { flex: 1; min-width: 0; border: 1px solid var(--border); border-radius: .55rem 0 0 .55rem; padding: .6rem .75rem; font: inherit; background: var(--surface); color: var(--text); }
.global-search button { border: 1px solid var(--accent); border-radius: 0 .55rem .55rem 0; padding: .6rem 1rem; color: #fff; background: var(--accent); font: inherit; font-weight: 650; cursor: pointer; }
.global-search button:hover { background: var(--accent-strong); }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0,0,0,0); white-space: nowrap; border: 0; }
.layout { display: grid; grid-template-columns: 17rem minmax(0, 1fr); align-items: start; max-width: 76rem; margin: 0 auto; }
.sidebar {
  position: sticky; top: 4.6rem; max-height: calc(100vh - 4.6rem); overflow-y: auto;
  padding: 1.5rem 1rem 2rem; border-right: 1px solid var(--border); font-size: .9rem;
}
.sidebar .primary-link { display: block; margin-bottom: .25rem; font-weight: 650; }
.sidebar .section { text-transform: uppercase; letter-spacing: .07em; font-size: .72rem; font-weight: 700; color: var(--muted); margin: 1.4rem .5rem .4rem; }
.sidebar ul { list-style: none; margin: 0; padding: 0; }
.sidebar li { margin: .1rem 0; }
.sidebar a { color: var(--muted); text-decoration: none; display: block; padding: .38rem .5rem; border-radius: .4rem; line-height: 1.35; }
.sidebar a:hover { color: var(--text); background: var(--surface-subtle); }
.sidebar a.active { color: #fff; background: var(--accent); }
.sidebar .view-all { margin-top: .25rem; color: var(--accent); font-weight: 650; }
.mobile-nav { display: none; }
main { min-width: 0; width: 100%; max-width: 58rem; padding: 2.4rem 2.25rem 5rem; }
a { color: var(--accent); }
a:hover { color: var(--accent-strong); }
a:focus-visible, button:focus-visible, input:focus-visible, summary:focus-visible { outline: 3px solid var(--focus); outline-offset: 2px; }
h1, h2, h3 { line-height: 1.25; }
h1 { margin-top: 0; border-bottom: 1px solid var(--border); padding-bottom: .55rem; }
.lede { max-width: 62ch; color: var(--muted); font-size: 1.05rem; }
.section-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem; margin-top: 2.3rem; }
.section-heading h2 { margin: 0; }
.section-heading a { font-size: .9rem; font-weight: 650; }
.card-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .8rem; list-style: none; margin: 1rem 0 0; padding: 0; }
.card { height: 100%; border: 1px solid var(--border); border-radius: .7rem; padding: .9rem 1rem; background: var(--surface); }
.card a { color: var(--text); font-weight: 700; text-decoration: none; }
.card a:hover { color: var(--accent); text-decoration: underline; }
.meta { display: block; margin-top: .3rem; color: var(--muted); font-size: .82rem; }
.collection-list { list-style: none; margin: 1.25rem 0 0; padding: 0; }
.collection-list li { border-bottom: 1px solid var(--border); padding: .85rem 0; }
.collection-list a { font-weight: 650; }
.filters { display: flex; flex-wrap: wrap; gap: .45rem; margin: 1rem 0 1.25rem; }
.filters a { border: 1px solid var(--border); border-radius: 999px; padding: .28rem .75rem; color: var(--muted); text-decoration: none; font-size: .88rem; }
.filters a:hover { color: var(--text); background: var(--surface-subtle); }
.filters a.active { border-color: var(--accent); color: var(--text); background: color-mix(in srgb, var(--accent) 15%, transparent); }
.search-results { display: grid; gap: .8rem; list-style: none; margin: 0; padding: 0; }
.search-result { border: 1px solid var(--border); border-radius: .7rem; padding: 1rem 1.1rem; background: var(--surface); }
.search-result h2 { margin: 0; font-size: 1.08rem; }
.search-result h2 a { color: var(--text); text-decoration: none; }
.search-result h2 a:hover { color: var(--accent); text-decoration: underline; }
.search-result p { margin: .45rem 0 0; color: var(--muted); }
.result-meta { display: flex; gap: .55rem; align-items: center; margin-top: .4rem; color: var(--muted); font-size: .8rem; }
.result-kind { border-radius: 999px; padding: .08rem .5rem; background: var(--surface-subtle); text-transform: capitalize; }
.breadcrumbs { margin: 0 0 1.1rem; color: var(--muted); font-size: .86rem; }
.breadcrumbs a { color: var(--muted); }
.breadcrumbs span { padding: 0 .3rem; }
.related-section { margin-top: 2.5rem; padding-top: 1.2rem; border-top: 1px solid var(--border); }
.related-section h2 { margin-top: 0; font-size: 1.15rem; }
.related-list { display: grid; gap: .45rem; list-style: none; margin: 0; padding: 0; }
.related-list li { display: flex; justify-content: space-between; gap: 1rem; }
.related-list a { font-weight: 650; }
.page-navigation { display: flex; justify-content: space-between; gap: 1rem; margin-top: 2rem; padding-top: 1rem; border-top: 1px solid var(--border); }
.page-navigation a { max-width: 48%; font-weight: 650; }
.page-navigation .newer { margin-left: auto; text-align: right; }
code { background: var(--surface-subtle); padding: .1em .3em; border-radius: 3px; font-size: .9em; }
pre {
  background: var(--surface-subtle); padding: 1rem; border-radius: 6px; overflow-x: auto;
  white-space: pre-wrap; word-wrap: break-word;
}
pre code { background: none; padding: 0; }
blockquote { border-left: 3px solid var(--border); margin-left: 0; padding-left: 1rem; color: var(--muted); }
table { border-collapse: collapse; }
th, td { border: 1px solid var(--border); padding: .4rem .6rem; }
@media (max-width: 760px) {
  .topbar { position: static; }
  .topbar-inner { align-items: stretch; flex-direction: column; gap: .65rem; }
  .global-search { width: 100%; max-width: none; }
  .layout { display: block; }
  .sidebar { display: none; }
  .mobile-nav { display: block; margin: 1rem 1.25rem 0; border: 1px solid var(--border); border-radius: .6rem; background: var(--surface); }
  .mobile-nav summary { cursor: pointer; padding: .7rem .9rem; font-weight: 700; }
  .mobile-nav .sidebar { display: block; position: static; max-height: min(65vh, 32rem); border: 0; border-top: 1px solid var(--border); padding: .75rem; }
  main { padding: 1.6rem 1.25rem 3rem; }
  .card-list { grid-template-columns: 1fr; }
}
@media (prefers-color-scheme: dark) {
  :root { --bg: #151617; --surface: #1d1f21; --surface-subtle: #25282b; --text: #eef0f2; --muted: #b4b8be; --border: #34373b; --accent: #75a2ff; --accent-strong: #a9c5ff; --focus: #ffca5c; }
  .global-search button { color: #101114; background: #75a2ff; }
  .sidebar a.active { color: #101114; }
}
</style>
</head>
<body>
<a class="skip-link" href="#content">Skip to content</a>
<header class="topbar">
  <div class="topbar-inner">
    <a class="brand" href="/">Patro knowledge</a>
    <form class="global-search" method="get" action="/search" role="search">
      <label class="sr-only" for="global-search">Search meetings and topics</label>
      <input id="global-search" type="search" name="q" value="{{.Query}}" placeholder="Search meetings and topics">
      <button type="submit">Search</button>
    </form>
  </div>
</header>
<div class="layout">
<nav class="sidebar" aria-label="Library navigation">{{.Sidebar}}</nav>
<details class="mobile-nav"><summary>Browse library</summary><nav class="sidebar" aria-label="Mobile library navigation">{{.Sidebar}}</nav></details>
<main id="content">{{.Body}}</main>
</div>
</body>
</html>`))

// URL prefixes for the two collections the viewer publishes. They are
// built from layout's URL segments, never from its directory names, so a
// future directory rename cannot silently change a published link.
const (
	topicsURL   = "/" + layout.TopicsURLSegment + "/"
	meetingsURL = "/" + layout.MeetingsURLSegment + "/"
)

// Server renders and serves the knowledge library rooted at Root.
type Server struct {
	Root string
	md   goldmark.Markdown

	// SearchIndex and the semantic fields power the read-only /search route.
	// SearchIndex is used by tests and embedders that already own an index
	// handle. SearchIndexPath is used by the CLI and opens the derived index for
	// the duration of each request, allowing `patro process` to rebuild it while
	// the viewer remains running. All fields are optional so a missing derived
	// store degrades to whichever ranking leg is available.
	SearchIndex     *searchindex.Index
	SearchIndexPath string
	MultiVectors    interface {
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

	switch rel {
	case "", layout.IndexFileName:
		s.handleHome(w, r)
		return
	case "search":
		s.handleSearch(w, r)
		return
	case layout.TopicsURLSegment:
		s.handleCollection(w, r, layout.TopicsDirName, "Topics", false)
		return
	case layout.MeetingsURLSegment:
		s.handleCollection(w, r, layout.MeetingsDirName, "Meetings", true)
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

// handleHome renders a retrieval-oriented overview instead of repeating the
// complete generated index. Full collections remain available under their
// own routes.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	topics := s.listSection(layout.TopicsDirName, false)
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].Meta == topics[j].Meta {
			return topics[i].Label < topics[j].Label
		}
		return topics[i].Meta > topics[j].Meta
	})
	meetings := s.listSection(layout.MeetingsDirName, true)

	var b strings.Builder
	b.WriteString(`<h1>Knowledge library</h1><p class="lede">Find decisions, action items, and context from your recorded meetings.</p>`)
	s.writeOverviewSection(&b, "Recent meetings", meetingsURL, meetings, 6)
	s.writeOverviewSection(&b, "Recently updated topics", topicsURL, topics, 6)
	s.render(w, r, "Knowledge library", template.HTML(b.String()))
}

func (s *Server) writeOverviewSection(b *strings.Builder, title, allURL string, items []navItem, limit int) {
	b.WriteString(`<div class="section-heading"><h2>` + template.HTMLEscapeString(title) + `</h2><a href="` +
		template.HTMLEscapeString(allURL) + `">View all</a></div>`)
	if len(items) == 0 {
		b.WriteString(`<p class="meta">Nothing here yet.</p>`)
		return
	}
	if len(items) > limit {
		items = items[:limit]
	}
	b.WriteString(`<ul class="card-list">`)
	for _, item := range items {
		b.WriteString(`<li><article class="card"><a href="` + template.HTMLEscapeString(item.URL) + `">` +
			template.HTMLEscapeString(item.Label) + `</a>`)
		if item.Meta != "" {
			b.WriteString(`<span class="meta">` + template.HTMLEscapeString(item.Meta) + `</span>`)
		}
		b.WriteString(`</article></li>`)
	}
	b.WriteString(`</ul>`)
}

func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request, dir, title string, newestFirst bool) {
	if _, err := os.ReadDir(filepath.Join(s.Root, dir)); err != nil {
		http.Error(w, "cannot read directory", http.StatusInternalServerError)
		return
	}
	items := s.listSection(dir, newestFirst)
	var b strings.Builder
	b.WriteString(`<h1>` + template.HTMLEscapeString(title) + `</h1>`)
	b.WriteString(`<p class="lede">Browse all ` + template.HTMLEscapeString(strings.ToLower(title)) + ` in the library.</p>`)
	if len(items) == 0 {
		b.WriteString(`<p>Nothing here yet.</p>`)
	} else {
		b.WriteString(`<ul class="collection-list">`)
		for _, item := range items {
			b.WriteString(`<li><a href="` + template.HTMLEscapeString(item.URL) + `">` +
				template.HTMLEscapeString(item.Label) + `</a>`)
			if item.Meta != "" {
				b.WriteString(`<span class="meta">` + template.HTMLEscapeString(item.Meta) + `</span>`)
			}
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ul>`)
	}
	s.render(w, r, title, template.HTML(b.String()))
}

// serveDir renders the directory's index.md when present, otherwise a
// simple listing of the entries.
func (s *Server) serveDir(w http.ResponseWriter, r *http.Request, full, rel string) {
	index := filepath.Join(full, layout.IndexFileName)
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
	body := s.decorateMarkdown(full, template.HTML(buf.String()))
	s.render(w, r, titleFor(full), body)
}

func (s *Server) decorateMarkdown(full string, rendered template.HTML) template.HTML {
	dir := filepath.Base(filepath.Dir(full))
	if dir != layout.TopicsDirName && dir != layout.MeetingsDirName {
		return rendered
	}

	var b strings.Builder
	b.WriteString(`<nav class="breadcrumbs" aria-label="Breadcrumb"><a href="/">Overview</a><span aria-hidden="true">/</span><a href="/` +
		template.HTMLEscapeString(dir) + `/">` + template.HTMLEscapeString(strings.ToUpper(dir[:1])+dir[1:]) + `</a></nav>`)
	b.WriteString(string(rendered))

	relatedDir := layout.TopicsDirName
	if dir == layout.TopicsDirName {
		relatedDir = layout.MeetingsDirName
	}
	related := s.relatedItems(full, dir, relatedDir)
	if len(related) > 0 {
		b.WriteString(`<section class="related-section"><h2>Related ` + template.HTMLEscapeString(relatedDir) + `</h2><ul class="related-list">`)
		for _, item := range related {
			b.WriteString(`<li><a href="` + template.HTMLEscapeString(item.URL) + `">` + template.HTMLEscapeString(item.Label) + `</a>`)
			if item.Meta != "" {
				b.WriteString(`<span class="meta">` + template.HTMLEscapeString(item.Meta) + `</span>`)
			}
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ul></section>`)
	}
	if dir == layout.MeetingsDirName {
		s.writeMeetingNavigation(&b, full)
	}
	return template.HTML(b.String())
}

func (s *Server) relatedItems(sourceFull, sourceDir, targetDir string) []navItem {
	items := s.listSection(targetDir, targetDir == layout.MeetingsDirName)
	sourceData, err := os.ReadFile(sourceFull)
	if err != nil {
		return nil
	}
	sourceName := filepath.Base(sourceFull)
	result := make([]navItem, 0)
	for _, item := range items {
		candidateName := filepath.Base(item.URL)
		if sourceDir == layout.TopicsDirName {
			if strings.Contains(string(sourceData), candidateName) {
				result = append(result, item)
			}
			continue
		}
		candidateData, err := os.ReadFile(filepath.Join(s.Root, targetDir, candidateName))
		if err == nil && strings.Contains(string(candidateData), sourceName) {
			result = append(result, item)
		}
	}
	return result
}

func (s *Server) writeMeetingNavigation(b *strings.Builder, full string) {
	items := s.listSection(layout.MeetingsDirName, true)
	currentURL := meetingsURL + filepath.Base(full)
	for i, item := range items {
		if item.URL != currentURL {
			continue
		}
		if i == 0 && i+1 >= len(items) {
			return
		}
		b.WriteString(`<nav class="page-navigation" aria-label="Adjacent meetings">`)
		if i+1 < len(items) {
			older := items[i+1]
			b.WriteString(`<a href="` + template.HTMLEscapeString(older.URL) + `">← Older: ` + template.HTMLEscapeString(older.Label) + `</a>`)
		}
		if i > 0 {
			newer := items[i-1]
			b.WriteString(`<a class="newer" href="` + template.HTMLEscapeString(newer.URL) + `">Newer: ` + template.HTMLEscapeString(newer.Label) + ` →</a>`)
		}
		b.WriteString(`</nav>`)
		return
	}
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

// renderedResultLimit bounds how many fused hits reach the page. Retrieval
// depth and the fusion constant belong to internal/search.
const renderedResultLimit = 20

// searchResult is one fused, render-ready hit.
type searchResult struct {
	ID      string // "topic:slug" or "meeting:slug"
	Kind    string
	Title   string
	URL     string
	Date    string
	Snippet string
	Score   float64
}

// handleSearch serves the read-only /search route: a query form plus, once
// a query is submitted, hits ranked by BM25 and semantic similarity through
// reciprocal-rank fusion. An unavailable ranking leg degrades gracefully to
// the other leg rather than failing the whole viewer.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	if kind != searchindex.KindTopic && kind != searchindex.KindMeeting {
		kind = ""
	}

	var b strings.Builder
	b.WriteString("<h1>Search</h1>\n<p class=\"lede\">Search meeting history and evolving topics from one place.</p>")

	if q != "" {
		s.writeSearchFilters(&b, q, kind)
		results := s.rankedResults(r.Context(), q, kind)
		if len(results) == 0 {
			b.WriteString(`<p>No results. Try a company, person, decision, or action item.</p>`)
		} else {
			b.WriteString(`<p class="meta">` + template.HTMLEscapeString(resultCountLabel(len(results))) + `</p>`)
			b.WriteString(`<ol class="search-results">`)
			for _, res := range results {
				b.WriteString(`<li><article class="search-result"><h2><a href="` + template.HTMLEscapeString(res.URL) + `">` +
					template.HTMLEscapeString(res.Title) + `</a></h2><div class="result-meta"><span class="result-kind">` +
					template.HTMLEscapeString(res.Kind) + `</span>`)
				if res.Date != "" {
					b.WriteString(`<time datetime="` + template.HTMLEscapeString(res.Date) + `">` + template.HTMLEscapeString(res.Date) + `</time>`)
				}
				b.WriteString(`</div>`)
				if res.Snippet != "" {
					b.WriteString(`<p>` + template.HTMLEscapeString(res.Snippet) + `</p>`)
				}
				b.WriteString(`</article></li>`)
			}
			b.WriteString(`</ol>`)
		}
	}

	s.render(w, r, "Search", template.HTML(b.String()))
}

func (s *Server) writeSearchFilters(b *strings.Builder, q, active string) {
	b.WriteString(`<nav class="filters" aria-label="Search result type">`)
	for _, filter := range []struct {
		kind, label string
	}{{"", "All"}, {searchindex.KindMeeting, "Meetings"}, {searchindex.KindTopic, "Topics"}} {
		class := ""
		if active == filter.kind {
			class = ` class="active" aria-current="page"`
		}
		href := "/search?q=" + urlQueryEscape(q)
		if filter.kind != "" {
			href += "&kind=" + filter.kind
		}
		b.WriteString(`<a` + class + ` href="` + template.HTMLEscapeString(href) + `">` + filter.label + `</a>`)
	}
	b.WriteString(`</nav>`)
}

func resultCountLabel(count int) string {
	if count == 1 {
		return "1 result"
	}
	return fmt.Sprintf("%d results", count)
}

// rankedResults retrieves both legs, fuses them, then decorates the winners
// for rendering. Fusion itself lives in internal/search; what remains here is
// delivery: reading Markdown for snippets, resolving titles and URLs, and
// applying the viewer's own recency tiebreak.
//
// The lexical source scan was deliberately removed: Markdown is read only to
// decorate indexed hits with snippets and never contributes a score.
func (s *Server) rankedResults(ctx context.Context, q, kindFilter string) []searchResult {
	var lexical []search.Candidate
	if hits, closeIndex := s.bm25Hits(q); hits != nil {
		lexical = make([]search.Candidate, 0, len(hits))
		for _, hit := range hits {
			lexical = append(lexical, search.Candidate{ID: hit.ID, Kind: hit.Kind, Title: hit.Title})
		}
		if closeIndex != nil {
			closeIndex()
		}
	}

	semanticHits := s.semanticHits(ctx, q)
	semantic := make([]search.Candidate, 0, len(semanticHits))
	for _, hit := range semanticHits {
		semantic = append(semantic, search.Candidate{
			ID:   searchindex.KindTopic + ":" + hit.ID,
			Kind: searchindex.KindTopic,
		})
	}

	fused := search.Fuse(lexical, semantic)
	normalizedQuery := strings.ToLower(strings.TrimSpace(q))

	results := make([]searchResult, 0, len(fused))
	for _, hit := range fused {
		result := s.resultForID(hit.ID, hit.Kind, hit.Title)
		if result.Title == "" || (kindFilter != "" && result.Kind != kindFilter) {
			continue
		}
		result.Score = hit.Score
		slug := strings.TrimPrefix(result.ID, result.Kind+":")
		if data, err := os.ReadFile(filepath.Join(s.Root, result.Kind+"s", slug+".md")); err == nil {
			result.Snippet = searchSnippet(string(data), normalizedQuery)
		}
		results = append(results, result)
	}

	// search.Fuse already orders by score then ID; re-sorting inserts the
	// viewer's recency preference between those two keys.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Date != results[j].Date {
			return results[i].Date > results[j].Date
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > renderedResultLimit {
		results = results[:renderedResultLimit]
	}
	return results
}

// bm25Hits opens the configured index lazily so the web viewer does not hold
// Bleve's lock while another process publishes a rebuilt index. A nil hit
// slice means that this ranking leg is unavailable or returned an error.
func (s *Server) bm25Hits(q string) ([]searchindex.Hit, func()) {
	idx := s.SearchIndex
	var closeIndex func()
	if idx == nil && s.SearchIndexPath != "" {
		if _, err := os.Stat(s.SearchIndexPath); err != nil {
			if !os.IsNotExist(err) {
				logging.Warnf("web: search index unavailable: %v", err)
			}
			return nil, nil
		}
		opened, err := searchindex.Open(s.SearchIndexPath)
		if err != nil {
			logging.Warnf("web: search index unavailable: %v", err)
			return nil, nil
		}
		idx = opened
		closeIndex = func() {
			if err := opened.Close(); err != nil {
				logging.Warnf("web: closing search index: %v", err)
			}
		}
	}
	if idx == nil {
		return nil, nil
	}
	hits, err := idx.Query(q, search.LexicalLimit)
	if err != nil {
		logging.Warnf("web: search index query failed: %v", err)
		if closeIndex != nil {
			closeIndex()
		}
		return nil, nil
	}
	return hits, closeIndex
}

func (s *Server) semanticHits(ctx context.Context, q string) []embed.RankedResult {
	if s.MultiVectors == nil || s.Representer == nil {
		return nil
	}
	representation, err := s.Representer.Represent(ctx, embed.Document{ID: "query", Text: "# Query\n\n" + q})
	if err != nil {
		logging.Warnf("web: representation search query failed: %v", err)
		return nil
	}
	if representation == nil {
		logging.Warnf("web: representation backend returned nil query representation")
		return nil
	}
	hits, err := s.MultiVectors.NearestRepresentations(ctx, *representation, embed.DirectedMode, search.SemanticLimit)
	if err != nil {
		logging.Warnf("web: multi-vector search failed: %v", err)
		return nil
	}
	return hits
}

func (s *Server) resultForID(id, kind, title string) searchResult {
	if kind == "" {
		kind, _, _ = strings.Cut(id, ":")
	}
	slug := strings.TrimPrefix(id, kind+":")
	full := filepath.Join(s.Root, kind+"s", slug+".md")
	if title == "" {
		title = headingOrStem(full)
	}
	date := latestSectionDate(full)
	if kind == searchindex.KindMeeting && len(slug) >= len("2006-01-02") {
		date = slug[:len("2006-01-02")]
	}
	return searchResult{ID: id, Kind: kind, Title: title, URL: urlFor(kind, id), Date: date}
}

func searchSnippet(markdown, normalizedQuery string) string {
	clean := strings.Join(strings.Fields(strings.NewReplacer("#", " ", "*", " ", "`", " ", "[", " ", "]", " ").Replace(markdown)), " ")
	lower := strings.ToLower(clean)
	start := strings.Index(lower, normalizedQuery)
	if start < 0 {
		start = 0
	}
	const radius = 90
	if start > radius {
		start -= radius
	} else {
		start = 0
	}
	end := start + 2*radius
	if end > len(clean) {
		end = len(clean)
	}
	for start > 0 && !utf8.RuneStart(clean[start]) {
		start--
	}
	for end < len(clean) && !utf8.RuneStart(clean[end]) {
		end--
	}
	snippet := strings.TrimSpace(clean[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(clean) {
		snippet += "…"
	}
	return snippet
}

func urlQueryEscape(value string) string {
	return strings.ReplaceAll(template.URLQueryEscaper(value), "+", "%20")
}

// urlFor builds the library-relative link for a "kind:slug" search hit ID.
func urlFor(kind, id string) string {
	slug := strings.TrimPrefix(id, kind+":")
	return "/" + kind + "s/" + slug + ".md"
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
		Query   string
	}{Title: title, Sidebar: s.buildSidebar(r.URL.Path), Body: body, Query: strings.TrimSpace(r.URL.Query().Get("q"))})
}

// navItem is a single sidebar link.
type navItem struct {
	URL   string
	Label string
	Meta  string
}

// buildSidebar renders the navigation sidebar: a home link plus the Topics
// and Meetings sections read from the library. The entry whose URL matches
// active is highlighted.
func (s *Server) buildSidebar(active string) template.HTML {
	homeClass := ` class="primary-link"`
	if active == "/" || active == "/index.md" {
		homeClass = ` class="primary-link active"`
	}
	var b strings.Builder
	b.WriteString(`<a` + homeClass + ` href="/">Overview</a>`)
	topics := s.listSection(layout.TopicsDirName, false)
	sort.SliceStable(topics, func(i, j int) bool { return topics[i].Meta > topics[j].Meta })
	s.writeSection(&b, "Topics", topicsURL, topics, active, 8)
	s.writeSection(&b, "Meetings", meetingsURL, s.listSection(layout.MeetingsDirName, true), active, 6)
	return template.HTML(b.String())
}

// writeSection appends a compact collection preview plus a route to the full
// collection, keeping navigation useful as the library grows.
func (s *Server) writeSection(b *strings.Builder, title, allURL string, items []navItem, active string, limit int) {
	if len(items) == 0 {
		return
	}
	b.WriteString(`<div class="section">` + template.HTMLEscapeString(title) + `</div><ul>`)
	visible := items
	if len(visible) > limit {
		visible = visible[:limit]
	}
	for _, it := range visible {
		cls := ""
		if active == it.URL {
			cls = ` class="active"`
		}
		b.WriteString(`<li><a` + cls + ` href="` + template.HTMLEscapeString(it.URL) + `">` +
			template.HTMLEscapeString(it.Label) + `</a></li>`)
	}
	b.WriteString(`</ul>`)
	allClass := `class="view-all"`
	if active == allURL || active == strings.TrimSuffix(allURL, "/") {
		allClass = `class="view-all active"`
	}
	b.WriteString(`<a ` + allClass + ` href="` + template.HTMLEscapeString(allURL) + `">View all ` +
		template.HTMLEscapeString(strings.ToLower(title)) + `</a>`)
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
		meta := latestSectionDate(f)
		if dir == layout.MeetingsDirName {
			base := filepath.Base(f)
			if len(base) >= len("2006-01-02") {
				meta = base[:len("2006-01-02")]
			}
		}
		items = append(items, navItem{
			URL:   "/" + dir + "/" + filepath.Base(f),
			Label: headingOrStem(f),
			Meta:  meta,
		})
	}
	return items
}

func latestSectionDate(full string) string {
	data, err := os.ReadFile(full)
	if err != nil {
		return ""
	}
	latest := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "## ") || len(line) < len("## 2006-01-02") {
			continue
		}
		date := line[3 : 3+len("2006-01-02")]
		if date[4] == '-' && date[7] == '-' && date > latest {
			latest = date
		}
	}
	return latest
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
