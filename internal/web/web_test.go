package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fernando143/patro/internal/embed"
	"github.com/fernando143/patro/internal/searchindex"
	"github.com/fernando143/patro/internal/vectors"
)

type fakeWebRepresenter struct {
	err  error
	text string
}

func (f *fakeWebRepresenter) Represent(_ context.Context, doc embed.Document) (*embed.Representation, error) {
	f.text = doc.Text
	if f.err != nil {
		return nil, f.err
	}
	return &embed.Representation{DocumentID: doc.ID}, nil
}

type fakeWebMultiStore struct {
	results []embed.RankedResult
	err     error
}

func (f fakeWebMultiStore) NearestRepresentations(context.Context, embed.Representation, embed.ScoreMode, int) ([]embed.RankedResult, error) {
	return f.results, f.err
}

// setupLibrary creates a temporary knowledge library with an index, a
// topic, a meeting note and a transcript, and returns its root.
func setupLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"index.md":                 "# Knowledge library\n\n- [Roadmap](topics/roadmap.md)\n",
		"topics/roadmap.md":        "# Roadmap\n\n## 2026-07-18 — Kickoff\n\nShip the web viewer.\n",
		"meetings/2026-07-18-x.md": "# Kickoff\n\nSee [transcript](../transcripts/abc.txt).\n",
		"transcripts/abc.txt":      "Speaker A: hello <world> & goodbye\n",
	}
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestServeHTTP(t *testing.T) {
	srv := NewServer(setupLibrary(t))

	tests := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains []string
	}{
		{
			name:       "root renders retrieval overview",
			path:       "/",
			wantStatus: http.StatusOK,
			wantContains: []string{
				"<h1>Knowledge library</h1>",
				"Recent meetings",
				"Recently updated topics",
				`role="search"`,
				`aria-label="Library navigation"`,
				`class="mobile-nav"`,
				// Compact sidebar sections and full collection links.
				`<div class="section">Topics</div>`,
				`<div class="section">Meetings</div>`,
				`href="/topics/roadmap.md">Roadmap</a>`,
				`href="/meetings/2026-07-18-x.md">Kickoff</a>`,
				`href="/topics/">View all topics</a>`,
				// Overview entry active on the home page.
				`class="primary-link active"`,
			},
		},
		{
			name:         "sidebar highlights the active topic",
			path:         "/topics/roadmap.md",
			wantStatus:   http.StatusOK,
			wantContains: []string{`<a class="active" href="/topics/roadmap.md">Roadmap</a>`},
		},
		{
			name:         "markdown file rendered to html",
			path:         "/topics/roadmap.md",
			wantStatus:   http.StatusOK,
			wantContains: []string{"<h1>Roadmap</h1>", "Ship the web viewer."},
		},
		{
			name:         "transcript shown as escaped preformatted text",
			path:         "/transcripts/abc.txt",
			wantStatus:   http.StatusOK,
			wantContains: []string{"<pre>", "&lt;world&gt; &amp; goodbye"},
		},
		{
			name:       "missing file is 404",
			path:       "/topics/nope.md",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "path traversal is rejected",
			path:       "/../../etc/passwd",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range tc.wantContains {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q\n---\n%s", want, body)
				}
			}
		})
	}
}

func TestServeHTTPRejectsNonGet(t *testing.T) {
	srv := NewServer(setupLibrary(t))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

// ServeHTTP always Stat()s before dispatching, so serveMarkdown/serveText's
// own ReadFile error branch (a defensive check against a stat-then-read
// race) is unreachable through a normal request. Calling the unexported
// method directly with a path that never existed reaches it.
func TestServeMarkdownMissingFileIs404(t *testing.T) {
	srv := NewServer(t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/topics/gone.md", nil)
	rec := httptest.NewRecorder()

	srv.serveMarkdown(rec, req, filepath.Join(srv.Root, "topics", "gone.md"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServeTextMissingFileIs404(t *testing.T) {
	srv := NewServer(t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/transcripts/gone.txt", nil)
	rec := httptest.NewRecorder()

	srv.serveText(rec, req, filepath.Join(srv.Root, "transcripts", "gone.txt"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestListSectionMissingDirReturnsEmpty(t *testing.T) {
	srv := NewServer(t.TempDir())
	if got := srv.listSection("topics", false); len(got) != 0 {
		t.Errorf("listSection(missing dir) = %v, want empty", got)
	}
}

func TestListSectionOrdersNewestFirst(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"2026-07-01-a.md", "2026-07-18-b.md"} {
		full := filepath.Join(root, "meetings", name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := NewServer(root)

	got := srv.listSection("meetings", true)
	if len(got) != 2 || got[0].URL != "/meetings/2026-07-18-b.md" {
		t.Errorf("listSection(newestFirst) = %+v, want the 07-18 entry first", got)
	}
}

func TestServeDirReadDirError(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "topics")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	srv := NewServer(root)
	req := httptest.NewRequest(http.MethodGet, "/topics/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServeDirListingWithoutIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "topics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "topics", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(root)

	req := httptest.NewRequest(http.MethodGet, "/topics/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `href="/topics/a.md"`) {
		t.Errorf("listing does not link a.md\n%s", body)
	}
}

func TestCollectionRoutesRenderMetadata(t *testing.T) {
	srv := NewServer(setupLibrary(t))
	tests := []struct {
		path string
		want []string
	}{
		{path: "/topics/", want: []string{"<h1>Topics</h1>", "Roadmap", "2026-07-18"}},
		{path: "/meetings/", want: []string{"<h1>Meetings</h1>", "Kickoff", "2026-07-18"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			for _, want := range tt.want {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("body missing %q\n%s", want, rec.Body.String())
				}
			}
		})
	}
}

// setupSearchLibrary builds a library plus a BM25 index and a vector store
// over its topics/meetings, mirroring the shapes internal/searchindex and
// internal/vectors already prove in their own Rebuild tests.
func setupSearchLibrary(t *testing.T) (root string, idx *searchindex.Index, store *vectors.Store, embedder embed.Embedder) {
	t.Helper()
	root = t.TempDir()
	topicsDir := filepath.Join(root, "topics")
	meetingsDir := filepath.Join(root, "meetings")
	if err := os.MkdirAll(topicsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meetingsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	topicContent := "# Roadmap\n\nQ3 zephyr rollout planning and deployment goals.\n"
	meetingContent := "# Kickoff\n\nDiscussed the zephyr rollout timeline.\n"
	if err := os.WriteFile(filepath.Join(topicsDir, "roadmap.md"), []byte(topicContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(meetingsDir, "2026-01-05-kickoff.md"), []byte(meetingContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var err error
	idx, err = searchindex.Open(filepath.Join(root, ".state", "search-index"))
	if err != nil {
		t.Fatalf("searchindex.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.Rebuild(context.Background(), topicsDir, meetingsDir); err != nil {
		t.Fatalf("searchindex Rebuild: %v", err)
	}

	embedder = embed.NewNop(4)
	store, err = vectors.NewStore(filepath.Join(root, ".state", "vectors", "topics.json"), embedder, embedder.Name())
	if err != nil {
		t.Fatalf("vectors.NewStore: %v", err)
	}
	if err := store.Rebuild(context.Background(), topicsDir, nil); err != nil {
		t.Fatalf("vectors Rebuild: %v", err)
	}

	return root, idx, store, embedder
}

func TestSearchFusesHitsFromTopicsAndMeetings(t *testing.T) {
	root, idx, store, embedder := setupSearchLibrary(t)
	srv := NewServer(root)
	srv.SearchIndex = idx
	srv.Vectors = store
	srv.Embedder = embedder

	req := httptest.NewRequest(http.MethodGet, "/search?q=zephyr", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/topics/roadmap.md"`) {
		t.Errorf("results missing topic hit\n%s", body)
	}
	if !strings.Contains(body, `href="/meetings/2026-01-05-kickoff.md"`) {
		t.Errorf("results missing meeting hit\n%s", body)
	}
}

// TestSearchVectorOnlyFallbackWhenBM25Unavailable proves the hybrid ranking
// incorporates the cosine signal when neither lexical nor BM25 matches.
func TestSearchVectorOnlyFallbackWhenBM25Unavailable(t *testing.T) {
	root, _, store, embedder := setupSearchLibrary(t)
	srv := NewServer(root)
	srv.Vectors = store
	srv.Embedder = embedder

	req := httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape("semantic-only-query"), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/topics/roadmap.md"`) || !strings.Contains(body, "Roadmap") {
		t.Errorf("results missing vector-only topic hit with resolved title\n%s", body)
	}
}

func TestSearchUsesCompleteMultiVectorQuery(t *testing.T) {
	root := setupLibrary(t)
	representer := &fakeWebRepresenter{}
	srv := NewServer(root)
	srv.Representer = representer
	srv.MultiVectors = fakeWebMultiStore{results: []embed.RankedResult{{ID: "roadmap", Score: .9}}}
	query := strings.Repeat("long-context ", 200)
	req := httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape(query), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/topics/roadmap.md"`) {
		t.Fatalf("multi-vector search response = %d/%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(representer.text, "# Query\n\n"+strings.TrimSpace(query)) {
		t.Fatalf("semantic search did not represent the complete query: got len=%d want len=%d suffix=%q", len(representer.text), len("# Query\n\n"+query), representer.text[len(representer.text)-40:])
	}
}

func TestSearchFallsBackToAllBM25WhenMultiVectorQueryFails(t *testing.T) {
	root, idx, _, _ := setupSearchLibrary(t)
	representer := &fakeWebRepresenter{err: errors.New("query representation failed")}
	srv := NewServer(root)
	srv.SearchIndex = idx
	srv.Representer = representer
	srv.MultiVectors = fakeWebMultiStore{}
	req := httptest.NewRequest(http.MethodGet, "/search?q=zephyr", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/topics/roadmap.md"`) {
		t.Fatalf("BM25 fallback response = %d/%s", rec.Code, rec.Body.String())
	}
}

func TestSearchEmptyQueryShowsForm(t *testing.T) {
	root, idx, store, embedder := setupSearchLibrary(t)
	srv := NewServer(root)
	srv.SearchIndex = idx
	srv.Vectors = store
	srv.Embedder = embedder

	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `action="/search"`) {
		t.Errorf("expected a search form\n%s", body)
	}
}

func TestSearchWithoutIndexDegradesGracefully(t *testing.T) {
	srv := NewServer(setupLibrary(t))

	req := httptest.NewRequest(http.MethodGet, "/search?q=roadmap", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (must never 500 when index/store are unavailable)", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `href="/topics/roadmap.md"`) {
		t.Errorf("expected Markdown fallback result\n%s", body)
	}
}

// TestSearchNoMatchesShowsMessage wires BM25 only (no vector store): kNN
// search is approximate and always returns its k-nearest entries regardless
// of relevance, so a genuine "no results" case is only observable when the
// only active ranker is exact-term BM25.
func TestSearchNoMatchesShowsMessage(t *testing.T) {
	root, idx, _, _ := setupSearchLibrary(t)
	srv := NewServer(root)
	srv.SearchIndex = idx

	req := httptest.NewRequest(http.MethodGet, "/search?q=nonexistentterm", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "No results") {
		t.Errorf("expected a no-results message\n%s", body)
	}
}

func TestSearchPrioritizesExactMarkdownMatchOverSemanticFallback(t *testing.T) {
	root := setupLibrary(t)
	if err := os.WriteFile(filepath.Join(root, "topics", "cachea.md"), []byte("# Cachea hiring process\n\nTechnical interview and next steps.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(root)
	srv.MultiVectors = fakeWebMultiStore{results: []embed.RankedResult{{ID: "roadmap", Score: .99}}}
	srv.Representer = &fakeWebRepresenter{}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?q=cachea", nil))
	body := rec.Body.String()
	resultsStart := strings.Index(body, `class="search-results"`)
	if resultsStart < 0 {
		t.Fatalf("response missing search results\n%s", body)
	}
	resultsBody := body[resultsStart:]
	cachea := strings.Index(resultsBody, `href="/topics/cachea.md"`)
	roadmap := strings.Index(resultsBody, `href="/topics/roadmap.md"`)
	if cachea < 0 || roadmap < 0 || cachea > roadmap {
		t.Fatalf("exact match must rank before semantic fallback\n%s", body)
	}
	for _, want := range []string{`class="filters"`, `class="search-result"`, `class="result-kind"`, "Technical interview and next steps"} {
		if !strings.Contains(body, want) {
			t.Errorf("search result missing %q\n%s", want, body)
		}
	}
}

func TestSearchKindFilter(t *testing.T) {
	root, idx, _, _ := setupSearchLibrary(t)
	srv := NewServer(root)
	srv.SearchIndex = idx

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?q=zephyr&kind=meeting", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `href="/meetings/2026-01-05-kickoff.md"`) {
		t.Fatalf("meeting filter omitted meeting result\n%s", body)
	}
	if strings.Contains(body, `<h2><a href="/topics/roadmap.md"`) {
		t.Fatalf("meeting filter included topic result\n%s", body)
	}
	if !strings.Contains(body, `class="active" aria-current="page" href="/search?q=zephyr&amp;kind=meeting"`) {
		t.Errorf("meeting filter is not exposed as active\n%s", body)
	}
}
