package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoBackslashEscapeFromRoot pins the two halves of the request-path rule
// against each other. Cleaning with the slash-based path package is what makes
// the viewer's routes work on Windows, but filepath.Join below it does treat a
// backslash as a separator, so a backslash-bearing request would otherwise pass
// the traversal guard as a single component and still escape Root. Only the
// combination is safe.
func TestNoBackslashEscapeFromRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "a", "b", "lib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "secret.txt"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(NewServer(root))
	t.Cleanup(ts.Close)

	targets := []string{
		"/../../secret.txt",
		"/..%5C..%5C..%5Csecret.txt",
		"/%2e%2e%5c%2e%2e%5c%2e%2e%5csecret.txt",
		`/..\..\..\secret.txt`,
	}
	for _, target := range targets {
		resp, err := http.Get(ts.URL + target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "TOPSECRET") {
			t.Errorf("%s: served a file outside Root (status %d)", target, resp.StatusCode)
		}
	}
}
