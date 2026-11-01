package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/6t9xstar/git-hook-doctor/internal/doctor"
)

func setup(t *testing.T) string {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	_ = os.WriteFile(empty, nil, 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HUSKY", "")
	dir := t.TempDir()
	if _, err := doctor.Git(dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, ".husky", "_"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"prepare":"husky"},"devDependencies":{"husky":"^9.1.7"}}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".husky", "pre-commit"), []byte("npm test\r\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".husky", "_", "pre-commit"), []byte("#!/usr/bin/env sh\n. \"$(dirname \"$0\")/h\"\n"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".husky", "_", "h"), []byte("#!/usr/bin/env sh\n"), 0o755)
	if _, err := doctor.Git(dir, "config", "core.hooksPath", ".husky/_"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func cli(stdin string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestDiagnoseFormatsAndExitCodes(t *testing.T) {
	dir := setup(t)
	code, out, _ := cli("", "diagnose", "-C", dir, "--no-color")
	if code != 1 || !strings.Contains(out, "GH010") || !strings.Contains(out, "CRLF") {
		t.Fatalf("diagnose: code %d\n%s", code, out)
	}
	code, _, _ = cli("", "diagnose", "-C", dir, "--fail-on", "none")
	if code != 0 {
		t.Fatalf("--fail-on none: code %d", code)
	}
	_, out, _ = cli("", "diagnose", "pre-commit", "-C", dir, "--format", "sarif")
	var sarif struct {
		Version string
		Runs    []struct {
			Results []struct{ RuleID string }
		}
	}
	if err := json.Unmarshal([]byte(out), &sarif); err != nil || sarif.Version != "2.1.0" || sarif.Runs[0].Results[0].RuleID != "GH010" {
		t.Fatalf("sarif: %v\n%s", err, out)
	}
	_, out, _ = cli("", "scan", "-C", dir, "--format", "github")
	if !strings.Contains(out, "::error file=.husky/pre-commit,line=1,title=GH010") {
		t.Fatalf("github:\n%s", out)
	}
	_, out, _ = cli("", "-C", dir, "--format", "markdown")
	if !strings.Contains(out, "| `pre-commit` | husky |") {
		t.Fatalf("markdown:\n%s", out)
	}
	_, out, _ = cli("", "-C", dir, "--format", "json")
	var doc struct {
		Summary map[string]int
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Summary["error"] != 1 {
		t.Fatalf("json: %v\n%s", err, out)
	}
}

func TestFixNeedsApproval(t *testing.T) {
	dir := setup(t)
	hook := filepath.Join(dir, ".husky", "pre-commit")

	code, out, _ := cli("", "fix", "-C", dir)
	if code != 0 || !strings.Contains(out, "dry run") {
		t.Fatalf("dry run: %d\n%s", code, out)
	}
	if data, _ := os.ReadFile(hook); !bytes.Contains(data, []byte("\r\n")) {
		t.Fatal("dry run modified the file")
	}
	// No answer and "n" both mean no.
	for _, answer := range []string{"", "n\n"} {
		cli(answer, "fix", "--apply", "-C", dir)
		if data, _ := os.ReadFile(hook); !bytes.Contains(data, []byte("\r\n")) {
			t.Fatalf("answer %q modified the file", answer)
		}
	}
	code, out, _ = cli("y\n", "fix", "--apply", "-C", dir)
	if code != 0 || !strings.Contains(out, "1 applied") {
		t.Fatalf("apply: %d\n%s", code, out)
	}
	if data, _ := os.ReadFile(hook); string(data) != "npm test\n" {
		t.Fatalf("not fixed: %q", data)
	}
	code, out, _ = cli("", "diagnose", "-C", dir)
	if code != 0 {
		t.Fatalf("still failing after fix:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := setup(t)
	for _, args := range [][]string{
		{"diagnose", "precommit", "-C", dir},
		{"scan", "-C", dir, "--format", "xml"},
		{"scan", "-C", dir, "--fail-on", "bad"},
		{"fix", "--apply", "--dry-run", "-C", dir},
		{"scan", "-C", filepath.Join(dir, "nope")},
		{"explode"},
	} {
		code, _, errOut := cli("", args...)
		if code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
		if len(args) > 1 && args[1] == "precommit" && !strings.Contains(errOut, "did you mean pre-commit") {
			t.Errorf("missing suggestion: %s", errOut)
		}
	}
}
