package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequiredValidate(t *testing.T) {
	if err := requiredValidate("   "); err == nil {
		t.Error("requiredValidate(blank) = nil, want error")
	}
	if err := requiredValidate("value"); err != nil {
		t.Errorf("requiredValidate(value) = %v, want nil", err)
	}
}

func TestDirValidate(t *testing.T) {
	if err := dirValidate(""); err == nil {
		t.Error("dirValidate(\"\") = nil, want error")
	}

	dir := t.TempDir()
	if err := dirValidate(dir); err != nil {
		t.Errorf("dirValidate(existing dir) = %v, want nil", err)
	}

	missing := filepath.Join(dir, "not-yet-created")
	if err := dirValidate(missing); err != nil {
		t.Errorf("dirValidate(missing dir) = %v, want nil (created later)", err)
	}

	file := filepath.Join(dir, "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := dirValidate(file); err == nil {
		t.Error("dirValidate(existing file) = nil, want error")
	}
}

func TestExecutableValidate(t *testing.T) {
	if err := executableValidate(""); err == nil {
		t.Error("executableValidate(\"\") = nil, want error")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := executableValidate(script); err != nil {
		t.Errorf("executableValidate(executable) = %v, want nil", err)
	}

	notExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := executableValidate(notExec); err == nil {
		t.Error("executableValidate(non-executable) = nil, want error")
	}
}
