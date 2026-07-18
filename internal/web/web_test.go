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
			name:         "root renders index.md",
			path:         "/",
			wantStatus:   http.StatusOK,
			wantContains: []string{"<h1>Knowledge library</h1>", `href="topics/roadmap.md"`},
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
