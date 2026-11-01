package doctor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolate keeps the developer's global and system Git config out of the tests.
func isolate(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	isolate(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(content, "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Git(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func lookPathExcept(missing ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, m := range missing {
			if m == name {
				return "", errors.New("not found")
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
		return "/usr/bin/" + name, nil
	}
}

func diagnose(t *testing.T, dir string, o Options) *Report {
	t.Helper()
	if o.Getenv == nil {
		o.Getenv = func(string) string { return "" }
	}
	if o.LookPath == nil {
		o.LookPath = lookPathExcept()
	}
	rep, err := Diagnose(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func ids(rep *Report) map[string]int {
	out := map[string]int{}
	for _, f := range rep.Findings {
		out[f.RuleID]++
	}
	return out
}

func expect(t *testing.T, rep *Report, want ...string) {
	t.Helper()
	got := ids(rep)
	for _, id := range want {
		if got[id] == 0 {
			t.Errorf("expected %s; got %v", id, got)
		}
	}
}

func TestBrokenHuskyLefthookPreCommitSetup(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"package.json":            `{"scripts":{"prepare":"husky install"},"devDependencies":{"husky":"^9.1.7"},"husky":{"hooks":{"pre-commit":"npm test"}}}`,
		".husky/pre-commit":       "#!/usr/bin/env sh\r\n. \"$(dirname -- \"$0\")/_/husky.sh\"\r\n\r\nnpx lint-staged\r\n",
		".husky/prepush":          "npm test\n",
		"lefthook.yml":            "precommit:\n  commands:\n    lint:\n      run: npm run lint\npre-push:\n  commands:\n    t:\n      run: npm test\n",
		".pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - id: x\n        name: x\n        entry: x\n        language: system\n        stages: [commit, push]\n",
		".git/hooks/pre-push":     "#!/bin/sh\necho old\n",
	})
	git(t, dir, "config", "core.hooksPath", ".githooks-missing")
	git(t, dir, "config", "alias.yolo", "commit --no-verify")
	rep := diagnose(t, dir, Options{})
	expect(t, rep, "GH001", "GH003", "GH004", "GH005", "GH006", "GH007", "GH010", "GH014", "GH016", "GH017", "GH021", "GH022", "GH024", "GH027")
	if rep.Findings[0].Severity != SevError {
		t.Error("findings should be sorted by severity")
	}
}

func TestHealthyHuskyV9(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"package.json":        `{"scripts":{"prepare":"husky"},"devDependencies":{"husky":"^9.1.7"}}`,
		".husky/pre-commit":   "npm test\n",
		".husky/_/pre-commit": "#!/usr/bin/env sh\n. \"$(dirname \"$0\")/h\"\n",
		".husky/_/h":          "#!/usr/bin/env sh\necho\n",
		".husky/_/.gitignore": "*\n",
		".gitattributes":      "* text=auto eol=lf\n",
	})
	git(t, dir, "config", "core.hooksPath", ".husky/_")
	git(t, dir, "add", "-A")
	rep := diagnose(t, dir, Options{})
	if len(rep.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", rep.Findings)
	}
	if len(rep.Hooks) != 1 || rep.Hooks[0].Hook != "pre-commit" || !rep.Hooks[0].Installed || rep.Hooks[0].Status != "ok" {
		t.Fatalf("unexpected hook table %+v", rep.Hooks)
	}
}

func TestHuskyNotInstalledAndEnv(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"package.json":      `{"scripts":{"prepare":"husky"},"devDependencies":{"husky":"^9.0.0"}}`,
		".husky/pre-commit": "npm test\n",
	})
	rep := diagnose(t, dir, Options{Getenv: func(k string) string {
		if k == "HUSKY" {
			return "0"
		}
		return ""
	}})
	expect(t, rep, "GH004", "GH008")
}

func TestHuskyV8NeedsExecutableAndShebang(t *testing.T) {
	dir := newRepo(t, map[string]string{
		"package.json":      `{"scripts":{"prepare":"husky install"},"devDependencies":{"husky":"^8.0.3"}}`,
		".husky/pre-commit": ". \"$(dirname -- \"$0\")/_/husky.sh\"\nnpm test\n",
	})
	git(t, dir, "config", "core.hooksPath", ".husky")
	git(t, dir, "add", "-A")
	rep := diagnose(t, dir, Options{})
	expect(t, rep, "GH009", "GH012")
	if ids(rep)["GH005"] > 0 || ids(rep)["GH006"] > 0 || ids(rep)["GH004"] > 0 {
		t.Errorf("husky v8 layout should not get v9 findings: %v", ids(rep))
	}
}

func TestPreCommitStalePythonAndMissingStage(t *testing.T) {
	dir := newRepo(t, map[string]string{
		".pre-commit-config.yaml": "default_install_hook_types: [pre-commit, commit-msg]\nrepos: []\n",
		".git/hooks/pre-commit":   "#!/usr/bin/env bash\n# File generated by pre-commit: https://pre-commit.com\nINSTALL_PYTHON=/nonexistent/venv/bin/python\n",
	})
	rep := diagnose(t, dir, Options{LookPath: lookPathExcept("pre-commit")})
	expect(t, rep, "GH019", "GH020")
	if ids(rep)["GH018"] > 0 {
		t.Error("pre-commit hook is installed; GH018 should not fire")
	}
}

func TestPlainHooksPathProblems(t *testing.T) {
	dir := newRepo(t, map[string]string{
		".githooks/pre-commit": "npx lint-staged\n",
		".githooks/pre-push":   "#!/usr/bin/env python9\nprint(1)\n",
		".githooks/commit-msg": "",
		".githooks/post_merge": "#!/bin/sh\necho\n",
	})
	git(t, dir, "config", "core.hooksPath", ".githooks")
	git(t, dir, "add", "-A")
	rep := diagnose(t, dir, Options{LookPath: lookPathExcept("npx", "python9")})
	expect(t, rep, "GH009", "GH012", "GH013", "GH014", "GH026", "GH028")
	// Fix the index mode of pre-commit and check Git records it.
	for _, f := range rep.Fixes {
		if f.RuleID == "GH009" && f.File == ".githooks/pre-commit" {
			if err := f.Apply(rep.Repo.Root); err != nil {
				t.Fatal(err)
			}
		}
	}
	if out := git(t, dir, "ls-files", "-s", ".githooks/pre-commit"); !strings.HasPrefix(out, "100755") {
		t.Fatalf("index mode not updated: %s", out)
	}
}

func TestAutoCRLFAndStaticMode(t *testing.T) {
	dir := newRepo(t, map[string]string{
		".githooks/pre-commit": "#!/bin/sh\necho ok\n",
		"lefthook.yml":         "pre-comit:\n  commands: {}\n",
	})
	git(t, dir, "config", "core.autocrlf", "true")
	git(t, dir, "config", "core.hooksPath", ".githooks-nope")
	git(t, dir, "add", "-A")
	git(t, dir, "update-index", "--chmod=+x", ".githooks/pre-commit")
	rep := diagnose(t, dir, Options{})
	expect(t, rep, "GH025", "GH001", "GH017")

	static := diagnose(t, dir, Options{Static: true})
	for id := range ids(static) {
		if !RuleByID(id).Static {
			t.Errorf("static mode reported non-static rule %s", id)
		}
	}
	expect(t, static, "GH017")
}

func TestNoHooks(t *testing.T) {
	dir := newRepo(t, map[string]string{"README.md": "x"})
	rep := diagnose(t, dir, Options{})
	expect(t, rep, "GH015")
}

func TestApplyFileFixes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pre-commit")
	_ = os.WriteFile(p, []byte("\xEF\xBB\xBF#!/usr/bin/env sh\r\n. \"$(dirname -- \"$0\")/_/husky.sh\"\r\n\r\nnpm test\r\n"), 0o755)
	for _, k := range []FixKind{FixBOM, FixHuskyLines, FixCRLF} {
		if err := (Fix{Kind: k, File: "pre-commit"}).Apply(dir); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(p)
	if string(data) != "npm test\n" {
		t.Fatalf("got %q", data)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o755 {
			t.Fatalf("mode changed to %v", info.Mode())
		}
	}
	_ = os.WriteFile(p, []byte("echo hi\n"), 0o644)
	_ = (Fix{Kind: FixShebang, File: "pre-commit"}).Apply(dir)
	_ = (Fix{Kind: FixExec, File: "pre-commit", Disk: true}).Apply(dir)
	data, _ = os.ReadFile(p)
	if !strings.HasPrefix(string(data), "#!/bin/sh\n") {
		t.Fatalf("shebang not added: %q", data)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o755 {
			t.Fatalf("not executable: %v", info.Mode())
		}
	}
	ga := filepath.Join(dir, ".gitattributes")
	for i := 0; i < 2; i++ {
		if err := (Fix{Kind: FixAppend, File: ".gitattributes", Text: ".husky/** text eol=lf"}).Apply(dir); err != nil {
			t.Fatal(err)
		}
	}
	if data, _ := os.ReadFile(ga); string(data) != ".husky/** text eol=lf\n" {
		t.Fatalf("append not idempotent: %q", data)
	}
}

func TestSuggestHookName(t *testing.T) {
	cases := map[string]string{
		"precommit": "pre-commit", "pre-commit.sh": "pre-commit", "pre_push": "pre-push",
		"commitmsg": "commit-msg", "commit-msg.txt": "commit-msg", "common.sh": "", "helpers": "",
		"post-checkout.js": "post-checkout",
	}
	for in, want := range cases {
		if got := SuggestHookName(in); got != want {
			t.Errorf("SuggestHookName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInterpreterAndCommands(t *testing.T) {
	if p, abs := Interpreter("/usr/bin/env -S node --no-warnings"); p != "node" || abs != "" {
		t.Errorf("env -S: %q %q", p, abs)
	}
	if p, abs := Interpreter("/bin/bash -e"); p != "bash" || abs != "/bin/bash" {
		t.Errorf("abs: %q %q", p, abs)
	}
	got := CommandsUsed([]byte("#!/bin/sh\n# npx nope\nFOO=1 npx lint-staged && pnpm test | cat\nexec node x.js\n"))
	if got["npx"] != 3 || got["pnpm"] != 3 || got["node"] != 4 || len(got) != 3 {
		t.Errorf("CommandsUsed = %v", got)
	}
}
