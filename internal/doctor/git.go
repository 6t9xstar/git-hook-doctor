package doctor

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ConfigValue is one value of a multi-valued git config key with its origin.
type ConfigValue struct {
	Scope  string `json:"scope"`  // system | global | local | worktree | command
	Origin string `json:"origin"` // e.g. file:.git/config
	Value  string `json:"value"`
}

// Repo is what we know about the Git repository.
type Repo struct {
	Root       string            `json:"root"`
	GitDir     string            `json:"gitDir,omitempty"`
	CommonDir  string            `json:"commonDir,omitempty"`
	HooksDir   string            `json:"hooksDir,omitempty"` // effective hooks directory (absolute)
	HooksPath  []ConfigValue     `json:"hooksPath,omitempty"`
	GitVersion string            `json:"gitVersion,omitempty"`
	AutoCRLF   string            `json:"autocrlf,omitempty"`
	Aliases    map[string]string `json:"-"`
	HasGit     bool              `json:"hasGit"`
	Bare       bool              `json:"bare,omitempty"`
	index      map[string]string // path -> mode, for tracked files
}

// Git runs git in dir and returns trimmed stdout.
func Git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimRight(out.String(), "\r\n"), errors.New(msg)
	}
	return strings.TrimRight(out.String(), "\r\n"), nil
}

var gitVersionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// OpenRepo inspects the repository that contains dir. When static is true
// (or git is unavailable) only the working tree files are used.
func OpenRepo(dir string, static bool) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: abs, Aliases: map[string]string{}, index: map[string]string{}}
	if _, err := exec.LookPath("git"); err != nil {
		return r, nil
	}
	top, err := Git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		if bare, _ := Git(abs, "rev-parse", "--is-bare-repository"); bare == "true" {
			r.Bare = true
		} else {
			return r, nil
		}
	} else {
		r.Root = filepath.Clean(filepath.FromSlash(top))
	}
	r.HasGit = true
	if v, err := Git(abs, "--version"); err == nil {
		if m := gitVersionRe.FindString(v); m != "" {
			r.GitVersion = m
		}
	}
	// Index modes are committed state, so they are useful in static mode too.
	if out, err := Git(r.Root, "ls-files", "-s"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			// <mode> <object> <stage>\t<path>
			meta, path, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			r.index[path] = strings.Fields(meta)[0]
		}
	}
	if static {
		return r, nil
	}
	if gd, err := Git(r.Root, "rev-parse", "--absolute-git-dir"); err == nil {
		r.GitDir = filepath.FromSlash(gd)
	}
	if cd, err := Git(r.Root, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		r.CommonDir = filepath.FromSlash(cd)
	} else {
		r.CommonDir = r.GitDir
	}
	if hd, err := Git(r.Root, "rev-parse", "--path-format=absolute", "--git-path", "hooks"); err == nil {
		r.HooksDir = filepath.Clean(filepath.FromSlash(hd))
	} else {
		r.HooksDir = filepath.Join(r.CommonDir, "hooks")
	}
	if out, err := Git(r.Root, "config", "--show-scope", "--show-origin", "--get-all", "core.hooksPath"); err == nil && out != "" {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) == 3 {
				r.HooksPath = append(r.HooksPath, ConfigValue{Scope: parts[0], Origin: parts[1], Value: parts[2]})
			}
		}
	}
	r.AutoCRLF, _ = Git(r.Root, "config", "--get", "core.autocrlf")
	if out, err := Git(r.Root, "config", "--get-regexp", `^alias\.`); err == nil {
		for _, line := range strings.Split(out, "\n") {
			k, v, _ := strings.Cut(line, " ")
			r.Aliases[strings.TrimPrefix(k, "alias.")] = v
		}
	}
	return r, nil
}

// EffectiveHooksPath returns the core.hooksPath value Git uses (the last one), if any.
func (r *Repo) EffectiveHooksPath() (ConfigValue, bool) {
	if len(r.HooksPath) == 0 {
		return ConfigValue{}, false
	}
	return r.HooksPath[len(r.HooksPath)-1], true
}

// DefaultHooksDir is $GIT_COMMON_DIR/hooks, used when core.hooksPath is unset.
func (r *Repo) DefaultHooksDir() string {
	if r.CommonDir == "" {
		return filepath.Join(r.Root, ".git", "hooks")
	}
	return filepath.Join(r.CommonDir, "hooks")
}

// Rel returns path relative to the repository root with forward slashes.
func (r *Repo) Rel(path string) string {
	if rel, err := filepath.Rel(r.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// IndexMode returns the Git index mode of a tracked file ("100644", "100755").
func (r *Repo) IndexMode(rel string) (string, bool) {
	m, ok := r.index[rel]
	return m, ok
}

// SameDir reports whether two paths name the same directory.
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ia, ib)
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
