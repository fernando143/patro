package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListenAddrIsAlwaysLoopback guards the threat-matrix requirement for
// this dev tool (network exposure row): the address embedbench binds to
// must always resolve to the loopback host, regardless of the requested
// port, never "0.0.0.0" or an empty host that would bind every interface.
func TestListenAddrIsAlwaysLoopback(t *testing.T) {
	for _, port := range []int{0, 1, defaultPort, 65535} {
		addr := listenAddr(port)
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("SplitHostPort(%q) error = %v", addr, err)
		}
		if host != "127.0.0.1" {
			t.Errorf("listenAddr(%d) host = %q, want 127.0.0.1", port, host)
		}
	}
}

// TestServerListensLoopbackOnly binds a real listener on the address run()
// uses and asserts the OS-reported bound IP is loopback. This is the
// end-to-end companion to TestListenAddrIsAlwaysLoopback: it catches a
// regression where listenAddr is correct but something else (e.g. a
// hardcoded "" host passed straight to http.ListenAndServe) binds all
// interfaces instead.
func TestServerListensLoopbackOnly(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	addr := listenAddr(port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Listen(%q) error = %v", addr, err)
	}
	defer ln.Close()

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is not a *net.TCPAddr: %v", ln.Addr())
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Fatalf("listener bound to %v, want a loopback address", tcpAddr.IP)
	}
}

func TestServeHTTPRejectsNonGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	NewServer().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestServeHTTPRendersFormWithoutQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	NewServer().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "embedbench") {
		t.Errorf("body missing title, got %q", body)
	}
	if strings.Contains(body, "<table>") {
		t.Errorf("body should not render a report table without text A")
	}
}

func TestServeHTTPRendersReportForA(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?a=hello+world", nil)
	rec := httptest.NewRecorder()
	NewServer().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<table>") {
		t.Errorf("body should render a report table when text A is set, got %q", body)
	}
}

func TestReportWithoutBOmitsCosine(t *testing.T) {
	rows, err := report(context.Background(), "hello world", "")
	if err != nil {
		t.Fatalf("report error = %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("report returned no rows")
	}
	for _, row := range rows {
		if row.Dim <= 0 {
			t.Errorf("row %+v has non-positive Dim", row)
		}
		if row.TimeA == "" {
			t.Errorf("row %+v has empty TimeA", row)
		}
		if row.Cosine != "" {
			t.Errorf("row %+v has Cosine set despite no text B", row)
		}
	}
}

func TestReportWithBComputesCosine(t *testing.T) {
	rows, err := report(context.Background(), "The cat sat on the mat.", "A feline rested on the rug.")
	if err != nil {
		t.Fatalf("report error = %v", err)
	}
	for _, row := range rows {
		if row.TimeB == "" {
			t.Errorf("row %+v has empty TimeB despite text B being set", row)
		}
		if row.Cosine == "" {
			t.Errorf("row %+v missing Cosine despite text B being set", row)
		}
	}
}
