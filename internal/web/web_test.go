package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
			name:       "root renders index.md",
			path:       "/",
			wantStatus: http.StatusOK,
			wantContains: []string{
				"<h1>Knowledge library</h1>",
				`href="topics/roadmap.md"`,
				// Sidebar sections and links.
				`<div class="section">Topics</div>`,
				`<div class="section">Meetings</div>`,
				`href="/topics/roadmap.md">Roadmap</a>`,
				`href="/meetings/2026-07-18-x.md">Kickoff</a>`,
				// Home entry active on the index page.
				`class="home active"`,
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
	if body := rec.Body.String(); !strings.Contains(body, `href="a.md"`) {
		t.Errorf("listing does not link a.md\n%s", body)
	}
}
