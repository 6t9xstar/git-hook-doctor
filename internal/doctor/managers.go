package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PackageJSON is the subset of package.json we need.
type PackageJSON struct {
	Path            string
	Scripts         map[string]string
	Deps            map[string]string
	Husky           *struct{ Hooks map[string]string } // Husky v4 config
	SimpleGitHooks  map[string]any
	Raw             map[string]any
	ParseError      string
	scriptLine      map[string]int
	huskyConfigLine int
	sghLine         int
}

// Managers describes the hook managers configured in the repository.
type Managers struct {
	Package *PackageJSON `json:"-"`

	Husky          bool   `json:"husky"`
	HuskyVersion   int    `json:"huskyVersion,omitempty"` // major, 0 when unknown
	HuskyVersionAt string `json:"huskyVersionSource,omitempty"`
	HuskyDir       string `json:"huskyDir,omitempty"` // absolute .husky directory
	HuskyV4Config  string `json:"huskyV4Config,omitempty"`

	Lefthook       bool     `json:"lefthook"`
	LefthookConfig string   `json:"lefthookConfig,omitempty"`
	LefthookHooks  []string `json:"lefthookHooks,omitempty"`
	lefthookKeys   map[string]int
	LefthookError  string `json:"-"`

	PreCommit       bool     `json:"preCommit"`
	PreCommitConfig string   `json:"preCommitConfig,omitempty"`
	PreCommitStages []string `json:"preCommitStages,omitempty"` // hook types that must be installed
	preCommitLegacy map[string]int
	PreCommitError  string `json:"-"`

	SimpleGitHooks       bool              `json:"simpleGitHooks"`
	SimpleGitHooksConfig string            `json:"simpleGitHooksConfig,omitempty"`
	SimpleGitHooksHooks  map[string]string `json:"simpleGitHooksHooks,omitempty"`
}

// Names lists the configured managers.
func (m *Managers) Names() []string {
	var out []string
	if m.Husky {
		out = append(out, "husky")
	}
	if m.Lefthook {
		out = append(out, "lefthook")
	}
	if m.PreCommit {
		out = append(out, "pre-commit")
	}
	if m.SimpleGitHooks {
		out = append(out, "simple-git-hooks")
	}
	return out
}

var majorRe = regexp.MustCompile(`(\d+)`)

func majorOf(spec string) int {
	spec = strings.TrimLeft(spec, "^~>=< v")
	if m := majorRe.FindString(spec); m != "" {
		n, _ := strconv.Atoi(m)
		return n
	}
	return 0
}

func lineOfKey(data []byte, key string) int {
	needle := `"` + key + `"`
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}

// ReadPackageJSON parses package.json in root.
func ReadPackageJSON(root string) *PackageJSON {
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	p := &PackageJSON{Path: path, Scripts: map[string]string{}, Deps: map[string]string{}, scriptLine: map[string]int{}}
	if err := json.Unmarshal(data, &p.Raw); err != nil {
		p.ParseError = err.Error()
		return p
	}
	if s, ok := p.Raw["scripts"].(map[string]any); ok {
		for k, v := range s {
			if str, ok := v.(string); ok {
				p.Scripts[k] = str
				p.scriptLine[k] = lineOfKey(data, k)
			}
		}
	}
	for _, field := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
		if d, ok := p.Raw[field].(map[string]any); ok {
			for k, v := range d {
				if str, ok := v.(string); ok {
					p.Deps[k] = str
				}
			}
		}
	}
	if h, ok := p.Raw["husky"].(map[string]any); ok {
		if hooks, ok := h["hooks"].(map[string]any); ok {
			p.Husky = &struct{ Hooks map[string]string }{Hooks: map[string]string{}}
			for k, v := range hooks {
				if s, ok := v.(string); ok {
					p.Husky.Hooks[k] = s
				}
			}
			p.huskyConfigLine = lineOfKey(data, "husky")
		}
	}
	if s, ok := p.Raw["simple-git-hooks"].(map[string]any); ok {
		p.SimpleGitHooks = s
		p.sghLine = lineOfKey(data, "simple-git-hooks")
	}
	return p
}

var lefthookFiles = []string{"lefthook.yml", ".lefthook.yml", "lefthook.yaml", ".lefthook.yaml", ".config/lefthook.yml", ".config/lefthook.yaml", "lefthook.toml", ".lefthook.toml", "lefthook.json", ".lefthook.json", "lefthook.jsonc", ".lefthook.jsonc"}

// lefthookOptions are top-level lefthook.yml keys that are not hooks.
var lefthookOptions = map[string]bool{
	"min_version": true, "colors": true, "no_tty": true, "assert_lefthook_installed": true,
	"skip_output": true, "output": true, "source_dir": true, "source_dir_local": true, "rc": true,
	"extends": true, "remotes": true, "remote": true, "lefthook": true, "templates": true, "skip_lfs": true,
	"glob_matcher": true,
}

var preCommitFiles = []string{".pre-commit-config.yaml", ".pre-commit-config.yml"}

// legacy pre-commit stage names and their current names.
var legacyStages = map[string]string{"commit": "pre-commit", "push": "pre-push", "merge-commit": "pre-merge-commit"}

// DetectManagers looks for Husky, Lefthook, pre-commit and simple-git-hooks.
func DetectManagers(root string) *Managers {
	m := &Managers{Package: ReadPackageJSON(root)}
	pkg := m.Package

	// Husky
	if st, err := os.Stat(filepath.Join(root, ".husky")); err == nil && st.IsDir() {
		m.Husky = true
		m.HuskyDir = filepath.Join(root, ".husky")
	}
	if pkg != nil {
		if v, ok := pkg.Deps["husky"]; ok {
			m.Husky = true
			m.HuskyVersion = majorOf(v)
			m.HuskyVersionAt = "package.json (" + v + ")"
		}
		if pkg.Husky != nil {
			m.Husky = true
			m.HuskyV4Config = "package.json"
		}
	}
	for _, name := range []string{".huskyrc", ".huskyrc.json", ".huskyrc.js", ".huskyrc.yaml", ".huskyrc.yml", "husky.config.js", ".huskyrc.cjs", "husky.config.cjs"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			m.Husky = true
			m.HuskyV4Config = name
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "node_modules", "husky", "package.json")); err == nil {
		var p struct{ Version string }
		if json.Unmarshal(data, &p) == nil && p.Version != "" {
			m.HuskyVersion = majorOf(p.Version)
			m.HuskyVersionAt = "node_modules/husky (" + p.Version + ")"
		}
	}
	if m.Husky && m.HuskyVersion == 0 && m.HuskyDir != "" {
		// Guess from the layout: v9 hook files have no husky.sh line and .husky/_/h exists.
		if _, err := os.Stat(filepath.Join(m.HuskyDir, "_", "h")); err == nil {
			m.HuskyVersion = 9
			m.HuskyVersionAt = ".husky/_/h"
		}
	}

	// Lefthook
	for _, name := range lefthookFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		m.Lefthook = true
		m.LefthookConfig = name
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				m.LefthookError = err.Error()
			} else if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
				m.lefthookKeys = map[string]int{}
				mp := doc.Content[0]
				for i := 0; i+1 < len(mp.Content); i += 2 {
					k := mp.Content[i]
					m.lefthookKeys[k.Value] = k.Line
					if IsGitHook(k.Value) {
						m.LefthookHooks = append(m.LefthookHooks, k.Value)
					}
				}
			}
		} else if strings.HasPrefix(filepath.Ext(name), ".json") {
			var obj map[string]any
			if err := json.Unmarshal(data, &obj); err == nil {
				for k := range obj {
					if IsGitHook(k) {
						m.LefthookHooks = append(m.LefthookHooks, k)
					}
				}
			}
		}
		break
	}

	// pre-commit
	for _, name := range preCommitFiles {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		m.PreCommit = true
		m.PreCommitConfig = name
		m.preCommitLegacy = map[string]int{}
		var cfg struct {
			DefaultInstallHookTypes []string `yaml:"default_install_hook_types"`
			DefaultStages           []string `yaml:"default_stages"`
			Repos                   []struct {
				Hooks []struct {
					ID     string   `yaml:"id"`
					Stages []string `yaml:"stages"`
				} `yaml:"hooks"`
			} `yaml:"repos"`
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			m.PreCommitError = err.Error()
			break
		}
		types := cfg.DefaultInstallHookTypes
		if len(types) == 0 {
			types = []string{"pre-commit"}
		}
		need := map[string]bool{}
		for _, t := range types {
			need[t] = true
		}
		stageLine := func(stage string) int {
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "stages") && regexp.MustCompile(`\b`+regexp.QuoteMeta(stage)+`\b`).MatchString(strings.ReplaceAll(line, "-", "_")) {
					return i + 1
				}
			}
			return 0
		}
		addStage := func(s string) {
			if cur, ok := legacyStages[s]; ok {
				if _, seen := m.preCommitLegacy[s]; !seen {
					m.preCommitLegacy[s] = stageLine(strings.ReplaceAll(s, "-", "_"))
				}
				s = cur
			}
			if s == "manual" {
				return
			}
			if IsGitHook(s) {
				need[s] = true
			}
		}
		for _, s := range cfg.DefaultStages {
			addStage(s)
		}
		for _, r := range cfg.Repos {
			for _, h := range r.Hooks {
				for _, s := range h.Stages {
					addStage(s)
				}
			}
		}
		// A stage only needs installing if default_install_hook_types does not
		// already cover it; we report all hook types the config relies on.
		for _, h := range GitHooks {
			if need[h] {
				m.PreCommitStages = append(m.PreCommitStages, h)
			}
		}
		break
	}

	// simple-git-hooks
	if pkg != nil && pkg.SimpleGitHooks != nil {
		m.SimpleGitHooks = true
		m.SimpleGitHooksConfig = "package.json"
		m.SimpleGitHooksHooks = map[string]string{}
		for k, v := range pkg.SimpleGitHooks {
			if s, ok := v.(string); ok && IsGitHook(k) {
				m.SimpleGitHooksHooks[k] = s
			}
		}
	}
	for _, name := range []string{".simple-git-hooks.json", "simple-git-hooks.json", ".simple-git-hooks.js", "simple-git-hooks.js", ".simple-git-hooks.cjs", "simple-git-hooks.cjs", ".simple-git-hooks.mjs", "simple-git-hooks.mjs"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			continue
		}
		m.SimpleGitHooks = true
		m.SimpleGitHooksConfig = name
		if strings.HasSuffix(name, ".json") {
			if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
				var obj map[string]any
				if json.Unmarshal(data, &obj) == nil {
					m.SimpleGitHooksHooks = map[string]string{}
					for k, v := range obj {
						if s, ok := v.(string); ok && IsGitHook(k) {
							m.SimpleGitHooksHooks[k] = s
						}
					}
				}
			}
		}
	}
	return m
}
