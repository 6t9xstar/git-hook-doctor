package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Options control a diagnosis.
type Options struct {
	Static   bool                         // only check committed files (for CI)
	Getenv   func(string) string          // defaults to os.Getenv
	LookPath func(string) (string, error) // defaults to exec.LookPath
}

// HookStatus is one row of the scan table.
type HookStatus struct {
	Hook      string `json:"hook"`
	Manager   string `json:"manager"`
	File      string `json:"file,omitempty"`
	Installed bool   `json:"installed"`
	Status    string `json:"status"`
}

// Report is the result of a diagnosis.
type Report struct {
	Repo      *Repo        `json:"repo"`
	Managers  *Managers    `json:"managers"`
	Static    bool         `json:"static"`
	HooksDir  string       `json:"hooksDir,omitempty"` // relative when inside the repository
	HooksFrom string       `json:"hooksFrom,omitempty"`
	Hooks     []HookStatus `json:"hooks"`
	Findings  []Finding    `json:"findings"`
	Fixes     []Fix        `json:"fixes"`
	Notes     []string     `json:"notes,omitempty"`
}

type diag struct {
	opts     Options
	repo     *Repo
	m        *Managers
	rep      *Report
	hooksDir string
	files    map[string]*HookFile // absolute path -> file
}

func (d *diag) add(id string, f Finding) {
	f.RuleID = id
	f.Severity = RuleByID(id).Severity
	d.rep.Findings = append(d.rep.Findings, f)
}

func (d *diag) fix(f Fix) {
	for _, existing := range d.rep.Fixes {
		if existing.Key() == f.Key() {
			return
		}
	}
	d.rep.Fixes = append(d.rep.Fixes, f)
}

// ExpandHooksPath resolves a core.hooksPath value like Git does.
func ExpandHooksPath(value, root string) string {
	v := value
	if strings.HasPrefix(v, "~/") || v == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			v = filepath.Join(home, strings.TrimPrefix(v, "~"))
		}
	}
	if !filepath.IsAbs(v) && !(runtime.GOOS == "windows" && strings.HasPrefix(v, "/")) {
		v = filepath.Join(root, v)
	}
	return filepath.Clean(filepath.FromSlash(v))
}

// Diagnose inspects the repository at dir.
func Diagnose(dir string, opts Options) (*Report, error) {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	repo, err := OpenRepo(dir, opts.Static)
	if err != nil {
		return nil, err
	}
	rep := &Report{Repo: repo, Static: opts.Static, Hooks: []HookStatus{}, Findings: []Finding{}, Fixes: []Fix{}}
	if !repo.HasGit && !opts.Static {
		opts.Static = true
		rep.Static = true
		rep.Notes = append(rep.Notes, "not inside a Git repository (or git is not installed): only committed hook files were checked")
	}
	d := &diag{opts: opts, repo: repo, m: DetectManagers(repo.Root), rep: rep, files: map[string]*HookFile{}}
	rep.Managers = d.m

	var installed []*HookFile
	if !opts.Static {
		if hp, ok := repo.EffectiveHooksPath(); ok {
			d.hooksDir = ExpandHooksPath(hp.Value, repo.Root)
			rep.HooksFrom = fmt.Sprintf("core.hooksPath (%s: %s)", hp.Scope, strings.TrimPrefix(hp.Origin, "file:"))
		} else {
			d.hooksDir = repo.DefaultHooksDir()
			rep.HooksFrom = "default"
		}
		repo.HooksDir = d.hooksDir
		rep.HooksDir = repo.Rel(d.hooksDir)
		installed = ReadHookDir(d.hooksDir, repo)
		for _, f := range installed {
			d.files[f.Path] = f
		}
	}
	sources := d.sourceFiles()
	d.checkHooksPath(installed)
	d.checkHusky()
	d.checkLefthook(installed)
	d.checkPreCommit(installed)
	d.checkSimpleGitHooks(installed)
	d.checkGeneral(installed)
	all := append(append([]*HookFile{}, installed...), sources...)
	seen := map[string]bool{}
	for _, f := range all {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		d.checkFile(f)
	}
	d.buildTable(installed, sources)
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		return rep.Findings[i].Severity.Rank() > rep.Findings[j].Severity.Rank()
	})
	return rep, nil
}

var hookSourceDirs = []string{".githooks", ".git-hooks", "githooks", ".hooks", "git-hooks", "scripts/git-hooks", "scripts/githooks", "scripts/hooks", "tools/git-hooks", "hooks"}

// sourceFiles returns committed hook scripts: .husky/*, the hooksPath
// directory when it is inside the repository, and common hook directories.
func (d *diag) sourceFiles() []*HookFile {
	var out []*HookFile
	add := func(files []*HookFile, owner string) {
		for _, f := range files {
			if _, dup := d.files[f.Path]; dup {
				continue
			}
			if owner != "" {
				f.Owner = owner
			}
			d.files[f.Path] = f
			out = append(out, f)
		}
	}
	if d.m.HuskyDir != "" {
		add(ReadHookDir(d.m.HuskyDir, d.repo), "husky")
	}
	for _, rel := range hookSourceDirs {
		dir := filepath.Join(d.repo.Root, filepath.FromSlash(rel))
		files := ReadHookDir(dir, d.repo)
		hasHook := false
		for _, f := range files {
			if IsGitHook(f.Name) || SuggestHookName(f.Name) != "" {
				hasHook = true
			}
		}
		if hasHook && (rel != "hooks" || SameDir(dir, d.hooksDir)) {
			add(files, "custom")
		}
	}
	return out
}

// huskyDirect reports whether Husky runs this file through `sh` (Husky v9),
// so it needs neither an executable bit nor a shebang.
func (d *diag) huskyDirect(f *HookFile) bool {
	if d.m.HuskyDir == "" || filepath.Dir(f.Path) != d.m.HuskyDir {
		return false
	}
	if d.m.HuskyVersion >= 9 {
		return true
	}
	if d.m.HuskyVersion == 0 {
		if _, err := os.Stat(filepath.Join(d.m.HuskyDir, "_", "h")); err == nil {
			return true
		}
		if hp, ok := d.repo.EffectiveHooksPath(); ok && strings.TrimSuffix(filepath.ToSlash(hp.Value), "/") == ".husky/_" {
			return true
		}
	}
	return false
}

func (d *diag) inHuskyWrapperDir(f *HookFile) bool {
	return d.m.HuskyDir != "" && filepath.Dir(f.Path) == filepath.Join(d.m.HuskyDir, "_")
}

var huskyShLine = regexp.MustCompile(`(?m)^\s*\.\s+"?\$\(dirname\s+(--\s+)?"?\$0"?\)/_/husky\.sh"?\s*$`)

func (d *diag) checkFile(f *HookFile) {
	fileOnly := Finding{File: f.Rel, Hook: f.Name}
	if !IsGitHook(f.Name) {
		if s := SuggestHookName(f.Name); s != "" && !d.inHuskyWrapperDir(f) {
			msg := fmt.Sprintf("%s is not a Git hook name, so Git never runs it. Did you mean %s?", f.Rel, s)
			fx := fileOnly
			fx.Message, fx.Fix = msg, fmt.Sprintf("rename it to %s", filepath.ToSlash(filepath.Join(filepath.Dir(f.Rel), s)))
			d.add("GH014", fx)
			if _, err := os.Stat(filepath.Join(filepath.Dir(f.Path), s)); err != nil {
				d.fix(Fix{RuleID: "GH014", File: f.Rel, Kind: FixRename, Target: s, Description: fmt.Sprintf("rename %s to %s", f.Rel, s)})
			}
		}
		return
	}
	if f.Size == 0 {
		fx := fileOnly
		fx.Message = fmt.Sprintf("%s is empty, so the hook does nothing.", f.Rel)
		d.add("GH028", fx)
		return
	}
	direct := d.huskyDirect(f)
	if f.BOM {
		fx := fileOnly
		fx.Line = 1
		fx.Message = fmt.Sprintf("%s starts with a UTF-8 byte order mark, so the #! line is not recognised (\"exec format error\" or \"cannot execute binary file\").", f.Rel)
		fx.Fix = "save the file as UTF-8 without BOM"
		d.add("GH011", fx)
		d.fix(Fix{RuleID: "GH011", File: f.Rel, Kind: FixBOM, Description: fmt.Sprintf("remove the UTF-8 BOM from %s", f.Rel)})
	}
	if f.CRLF {
		fx := fileOnly
		fx.Line = 1
		fx.Message = fmt.Sprintf("%s has Windows (CRLF) line endings. The hook fails with errors like \"/usr/bin/env: 'sh\\r': No such file or directory\" or \"$'\\r': command not found\".", f.Rel)
		fx.Fix = "convert the file to LF and add `" + gitattrPattern(f.Rel) + " text eol=lf` to .gitattributes"
		d.add("GH010", fx)
		d.fix(Fix{RuleID: "GH010", File: f.Rel, Kind: FixCRLF, Description: fmt.Sprintf("convert %s to LF line endings", f.Rel)})
	}
	if direct {
		if loc := huskyShLine.FindIndex(f.Content); loc != nil {
			fx := fileOnly
			fx.Line = strings.Count(string(f.Content[:loc[0]]), "\n") + 1
			fx.Message = fmt.Sprintf("%s still sources husky.sh. Husky v9 prints \"DEPRECATED\" for this line and Husky v10 will fail.", f.Rel)
			fx.Fix = "remove the `#!/usr/bin/env sh` and `. \"$(dirname -- \"$0\")/_/husky.sh\"` lines"
			d.add("GH005", fx)
			d.fix(Fix{RuleID: "GH005", File: f.Rel, Kind: FixHuskyLines, Description: fmt.Sprintf("remove the deprecated husky.sh lines from %s", f.Rel)})
		}
	} else {
		diskBad := runtime.GOOS != "windows" && !f.Exec
		mode, tracked := d.repo.IndexMode(f.Rel)
		indexBad := tracked && mode == "100644"
		if diskBad || indexBad {
			fx := fileOnly
			switch {
			case diskBad && indexBad:
				fx.Message = fmt.Sprintf("%s is not executable, on disk or in Git. Git skips it with \"hint: The '%s' hook was ignored because it's not set as executable.\"", f.Rel, f.Name)
			case diskBad:
				fx.Message = fmt.Sprintf("%s is not executable. Git skips it with \"hint: The '%s' hook was ignored because it's not set as executable.\"", f.Rel, f.Name)
			default:
				fx.Message = fmt.Sprintf("%s is committed without the executable bit (mode 100644), so the hook is ignored for everyone on macOS and Linux.", f.Rel)
			}
			cmds := []string{}
			if diskBad {
				cmds = append(cmds, "chmod +x "+f.Rel)
			}
			if indexBad {
				cmds = append(cmds, "git update-index --chmod=+x "+f.Rel)
			}
			fx.Fix = strings.Join(cmds, " && ")
			d.add("GH009", fx)
			d.fix(Fix{RuleID: "GH009", File: f.Rel, Kind: FixExec, Disk: diskBad, Index: indexBad, Description: fmt.Sprintf("make %s executable (%s)", f.Rel, strings.Join(cmds, ", "))})
		}
		if f.Shebang == "" {
			fx := fileOnly
			fx.Line = 1
			fx.Message = fmt.Sprintf("%s has no #! line. Git for Windows cannot run it, and other systems fall back to /bin/sh.", f.Rel)
			fx.Fix = "start the file with `#!/bin/sh` (or the interpreter it needs)"
			d.add("GH012", fx)
			if looksLikeShell(f.Content) {
				d.fix(Fix{RuleID: "GH012", File: f.Rel, Kind: FixShebang, Description: fmt.Sprintf("add #!/bin/sh to %s", f.Rel)})
			}
		}
		if !d.opts.Static && f.Shebang != "" {
			d.checkInterpreter(f)
		}
	}
	if !d.opts.Static && (f.Owner == "custom" || f.Owner == "husky" || f.Owner == "simple-git-hooks") && !d.inHuskyWrapperDir(f) {
		for tool, line := range CommandsUsed(f.Content) {
			if _, err := d.opts.LookPath(tool); err != nil {
				fx := fileOnly
				fx.Line = line
				fx.Message = fmt.Sprintf("%s runs `%s`, which is not on PATH here. The hook fails with \"%s: command not found\".", f.Rel, tool, tool)
				fx.Fix = "install it, or (for nvm, fnm, asdf, pyenv users) make it available to non-interactive shells"
				if d.m.Husky && (tool == "npx" || tool == "node" || tool == "npm") {
					fx.Fix += ", e.g. in ~/.config/husky/init.sh"
				}
				d.add("GH026", fx)
			}
		}
	}
}

func looksLikeShell(content []byte) bool {
	s := string(content)
	return !strings.Contains(s, "require(") && !strings.Contains(s, "import ") && !strings.Contains(s, "def ")
}

func gitattrPattern(rel string) string {
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		return rel
	}
	return dir + "/**"
}

func (d *diag) checkInterpreter(f *HookFile) {
	prog, abs := Interpreter(f.Shebang)
	if prog == "" {
		return
	}
	if runtime.GOOS == "windows" {
		// Git for Windows ships sh/bash/perl and resolves other interpreters by name on PATH.
		switch prog {
		case "sh", "bash", "perl", "env":
			return
		}
		if _, err := d.opts.LookPath(prog); err != nil {
			d.add("GH013", Finding{File: f.Rel, Hook: f.Name, Line: 1, Message: fmt.Sprintf("%s runs with `%s` (#!%s), which is not on PATH.", f.Rel, prog, f.Shebang), Fix: "install " + prog + " or change the #! line"})
		}
		return
	}
	if abs != "" {
		if _, err := os.Stat(abs); err != nil {
			msg := fmt.Sprintf("%s runs with %s, which does not exist on this machine (\"bad interpreter: No such file or directory\").", f.Rel, abs)
			fix := "install " + prog + " or change the #! line"
			if _, err := d.opts.LookPath(prog); err == nil {
				fix = fmt.Sprintf("use `#!/usr/bin/env %s` so the interpreter is found on PATH", prog)
			}
			d.add("GH013", Finding{File: f.Rel, Hook: f.Name, Line: 1, Message: msg, Fix: fix})
		}
		return
	}
	if _, err := d.opts.LookPath(prog); err != nil {
		d.add("GH013", Finding{File: f.Rel, Hook: f.Name, Line: 1, Message: fmt.Sprintf("%s runs with `%s` (#!%s), which is not on PATH.", f.Rel, prog, f.Shebang), Fix: "install " + prog + " or change the #! line"})
	}
}

var windowsPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func (d *diag) checkHooksPath(installed []*HookFile) {
	if d.opts.Static {
		return
	}
	hp, ok := d.repo.EffectiveHooksPath()
	if !ok {
		return
	}
	origin := strings.TrimPrefix(hp.Origin, "file:")
	if st, err := os.Stat(d.hooksDir); err != nil || !st.IsDir() {
		msg := fmt.Sprintf("core.hooksPath is %q (%s config, %s), but %s does not exist. Git silently runs no hooks at all.", hp.Value, hp.Scope, origin, d.hooksDir)
		fix := fmt.Sprintf("create the directory, or remove the setting: git config --%s --unset core.hooksPath", hp.Scope)
		hv := strings.TrimSuffix(filepath.ToSlash(hp.Value), "/")
		switch {
		case hv == ".husky/_" || hv == ".husky":
			fix = "run `npx husky` (or `npm install`, which runs the prepare script) to generate .husky/_"
			if hv == ".husky" || (d.m.HuskyVersion >= 5 && d.m.HuskyVersion < 9) {
				fix = "run `npx husky install` (Husky v5-v8) or `npx husky` (v9)"
			}
		case runtime.GOOS != "windows" && windowsPath.MatchString(hp.Value):
			msg += " The value is a Windows path, but Git is running on " + runtime.GOOS + " (WSL?)."
			fix = "use a relative path (e.g. .githooks) so the setting works on Windows, WSL, macOS and Linux"
		case runtime.GOOS == "windows" && strings.HasPrefix(hp.Value, "/mnt/"):
			msg += " The value is a WSL path, but Git is running on Windows."
			fix = "use a relative path (e.g. .githooks) so the setting works on Windows, WSL, macOS and Linux"
		}
		d.add("GH001", Finding{Message: msg, Fix: fix})
		if (hp.Scope == "local" || hp.Scope == "global") && !strings.HasPrefix(hv, ".husky") {
			d.fix(Fix{RuleID: "GH001", Kind: FixCommand, Command: []string{"git", "config", "--" + hp.Scope, "--unset-all", "core.hooksPath"}, Description: fmt.Sprintf("remove the broken core.hooksPath setting (%s config)", hp.Scope)})
		}
	}
	if hp.Scope == "global" || hp.Scope == "system" {
		msg := fmt.Sprintf("core.hooksPath is set in your %s Git config (%s = %s). It overrides .git/hooks in every repository, so hooks installed by Lefthook, pre-commit or plain scripts in .git/hooks never run.", hp.Scope, origin, hp.Value)
		d.add("GH002", Finding{Message: msg, Fix: fmt.Sprintf("set it per repository instead: git config --%s --unset core.hooksPath", hp.Scope)})
		if hp.Scope == "global" {
			d.fix(Fix{RuleID: "GH002", Kind: FixCommand, Command: []string{"git", "config", "--global", "--unset-all", "core.hooksPath"}, Description: "remove core.hooksPath from your global Git config (affects every repository)"})
		}
	}
	def := d.repo.DefaultHooksDir()
	if !SameDir(def, d.hooksDir) {
		var ignored []string
		for _, f := range ReadHookDir(def, d.repo) {
			if IsGitHook(f.Name) {
				ignored = append(ignored, f.Name)
			}
		}
		if len(ignored) > 0 {
			d.add("GH003", Finding{Message: fmt.Sprintf("%s contains %s, but Git ignores that directory because core.hooksPath is %q.", d.repo.Rel(def), strings.Join(ignored, ", "), hp.Value), Fix: fmt.Sprintf("move the hooks to %s, or unset core.hooksPath (git config --%s --unset core.hooksPath)", d.repo.Rel(d.hooksDir), hp.Scope)})
		}
	}
}

func (d *diag) checkHusky() {
	m := d.m
	if !m.Husky {
		return
	}
	pkg := m.Package
	pkgRel := "package.json"
	// Husky v4 leftovers.
	if m.HuskyV4Config != "" && m.HuskyVersion != 4 && (m.HuskyVersion >= 5 || m.HuskyDir != "") {
		f := Finding{File: m.HuskyV4Config, Message: fmt.Sprintf("Husky v4 configuration found in %s, but Husky 5+ ignores it. Those hooks never run.", m.HuskyV4Config), Fix: "move each hook command to a file in .husky/ (e.g. .husky/pre-commit) and delete the old config"}
		if m.HuskyV4Config == "package.json" && pkg != nil {
			f.Line = pkg.huskyConfigLine
			var names []string
			for k := range pkg.Husky.Hooks {
				names = append(names, k)
			}
			sort.Strings(names)
			f.Message = fmt.Sprintf("package.json has a Husky v4 \"husky.hooks\" section (%s), but Husky 5+ ignores it. Those hooks never run.", strings.Join(names, ", "))
		}
		d.add("GH007", f)
	}
	if m.HuskyDir == "" || pkg == nil || pkg.ParseError != "" {
		return
	}
	// prepare script
	installs := ""
	for _, s := range []string{"prepare", "postinstall", "install", "postprepare"} {
		if strings.Contains(pkg.Scripts[s], "husky") {
			installs = s
		}
	}
	switch {
	case installs == "":
		d.add("GH006", Finding{File: pkgRel, Line: pkg.scriptLine["prepare"], Message: "package.json has no prepare script that installs Husky, so new clones never get the hooks.", Fix: huskyPrepare(m.HuskyVersion)})
	case m.HuskyVersion >= 9 && strings.Contains(pkg.Scripts[installs], "husky install"):
		d.add("GH006", Finding{File: pkgRel, Line: pkg.scriptLine[installs], Message: fmt.Sprintf("The %s script runs `husky install`, which is deprecated in Husky v9 (it prints \"install command is DEPRECATED\").", installs), Fix: `use "prepare": "husky"`})
	}
	if d.opts.Static {
		return
	}
	expected := ".husky/_"
	if m.HuskyVersion >= 5 && m.HuskyVersion < 9 {
		expected = ".husky"
	}
	if m.HuskyVersion == 4 {
		return
	}
	hp, ok := d.repo.EffectiveHooksPath()
	cur := ""
	if ok {
		cur = strings.TrimSuffix(filepath.ToSlash(hp.Value), "/")
	}
	if !ok || !SameDir(ExpandHooksPath(cur, d.repo.Root), filepath.Join(d.repo.Root, filepath.FromSlash(expected))) {
		if ok && (cur == ".husky" || cur == ".husky/_") {
			// Old/new layout mismatch (e.g. upgraded from v8 to v9 without re-running install).
			d.add("GH004", Finding{Message: fmt.Sprintf("core.hooksPath is %q, but Husky %d uses %q. Hooks in .husky/ do not run as expected.", cur, m.HuskyVersion, expected), Fix: "run `" + huskyInstallCmd(m.HuskyVersion) + "`"})
			return
		}
		msg := "Husky is set up in .husky/, but its hooks are not installed: core.hooksPath is not set."
		if ok {
			msg = fmt.Sprintf("Husky is set up in .husky/, but core.hooksPath is %q, so Git does not run the Husky hooks.", hp.Value)
		}
		if env := d.opts.Getenv("HUSKY"); env == "0" {
			msg += " (HUSKY=0 is set, which also skips installation.)"
		}
		d.add("GH004", Finding{Message: msg, Fix: "run `" + huskyInstallCmd(m.HuskyVersion) + "` (or `npm install`, which runs the prepare script)"})
		d.fix(Fix{RuleID: "GH004", Kind: FixManual, Description: "install the Husky hooks", Manual: huskyInstallCmd(m.HuskyVersion)})
	}
}

func huskyInstallCmd(v int) string {
	if v >= 5 && v < 9 {
		return "npx husky install"
	}
	return "npx husky"
}

func huskyPrepare(v int) string {
	if v >= 5 && v < 9 {
		return `add "prepare": "husky install" to the scripts in package.json`
	}
	return `add "prepare": "husky" to the scripts in package.json`
}

func ownerOf(installed []*HookFile, name string) (*HookFile, bool) {
	for _, f := range installed {
		if f.Name == name {
			return f, true
		}
	}
	return nil, false
}

func (d *diag) checkLefthook(installed []*HookFile) {
	m := d.m
	if !m.Lefthook {
		return
	}
	if m.LefthookError != "" {
		d.add("GH029", Finding{File: m.LefthookConfig, Message: fmt.Sprintf("%s is not valid YAML: %s", m.LefthookConfig, m.LefthookError), Fix: "fix the YAML syntax; `lefthook validate` shows details"})
		return
	}
	for key, line := range m.lefthookKeys {
		if IsGitHook(key) || lefthookOptions[key] || strings.HasPrefix(key, "x-") {
			continue
		}
		f := Finding{File: m.LefthookConfig, Line: line, Message: fmt.Sprintf("%s: %q is not a Git hook or a Lefthook option, so Lefthook ignores it.", m.LefthookConfig, key)}
		if s := SuggestHookName(key); s != "" {
			f.Message += fmt.Sprintf(" Did you mean %q?", s)
			f.Fix = "rename the key to " + s
		}
		d.add("GH017", f)
	}
	if d.opts.Static {
		return
	}
	cmd := "lefthook install"
	if m.Package != nil {
		if _, ok := m.Package.Deps["lefthook"]; ok {
			cmd = "npx lefthook install"
		}
	}
	var missing []string
	for _, h := range m.LefthookHooks {
		if f, ok := ownerOf(installed, h); !ok || f.Owner != "lefthook" {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		msg := fmt.Sprintf("%s defines %s, but the Lefthook hook is not installed in %s.", m.LefthookConfig, strings.Join(missing, ", "), d.repo.Rel(d.hooksDir))
		for _, h := range missing {
			if f, ok := ownerOf(installed, h); ok {
				msg += fmt.Sprintf(" %s is currently a %s hook.", h, f.Owner)
			}
		}
		d.add("GH016", Finding{Message: msg, Fix: "run `" + cmd + "`"})
		d.fix(Fix{RuleID: "GH016", Kind: FixManual, Description: "install the Lefthook hooks", Manual: cmd})
	}
	if v := strings.ToLower(d.opts.Getenv("LEFTHOOK")); v == "0" || v == "false" {
		d.add("GH008", Finding{Message: "LEFTHOOK=" + v + " is set in this environment, so Lefthook skips every hook.", Fix: "unset LEFTHOOK"})
	}
}

var installPython = regexp.MustCompile(`(?m)^INSTALL_PYTHON=['"]?([^'"\n]+)['"]?`)

func (d *diag) checkPreCommit(installed []*HookFile) {
	m := d.m
	if !m.PreCommit {
		return
	}
	if m.PreCommitError != "" {
		d.add("GH029", Finding{File: m.PreCommitConfig, Message: fmt.Sprintf("%s is not valid YAML: %s", m.PreCommitConfig, m.PreCommitError), Fix: "fix the YAML syntax; `pre-commit validate-config` shows details"})
		return
	}
	for stage, line := range m.preCommitLegacy {
		d.add("GH021", Finding{File: m.PreCommitConfig, Line: line, Message: fmt.Sprintf("%s uses the deprecated stage name %q (pre-commit 3.2+ calls it %q).", m.PreCommitConfig, stage, legacyStages[stage]), Fix: "run `pre-commit migrate-config`"})
	}
	if d.opts.Static {
		return
	}
	hp, hasHP := d.repo.EffectiveHooksPath()
	for _, f := range installed {
		if f.Owner != "pre-commit" {
			continue
		}
		mt := installPython.FindSubmatch(f.Content)
		if mt == nil {
			continue
		}
		py := string(mt[1])
		if _, err := os.Stat(py); err != nil {
			if _, err := d.opts.LookPath("pre-commit"); err != nil {
				d.add("GH019", Finding{File: f.Rel, Hook: f.Name, Message: fmt.Sprintf("The %s hook was installed with %s, which no longer exists, and `pre-commit` is not on PATH. Commits fail with \"`pre-commit` not found. Did you forget to activate your virtualenv?\"", f.Name, py), Fix: "reinstall pre-commit (pipx install pre-commit) and run `pre-commit install` again"})
			}
		}
	}
	var missing []string
	for _, stage := range m.PreCommitStages {
		f, ok := ownerOf(installed, stage)
		if ok && f.Owner == "pre-commit" {
			continue
		}
		missing = append(missing, stage)
	}
	if len(missing) == 0 {
		return
	}
	if hasHP && !SameDir(d.hooksDir, d.repo.DefaultHooksDir()) {
		d.add("GH027", Finding{Message: fmt.Sprintf("%s needs the %s hook(s), but `pre-commit install` refuses to run while core.hooksPath is set (%q): \"Cowardly refusing to install hooks with `core.hooksPath` set.\"", m.PreCommitConfig, strings.Join(missing, ", "), hp.Value), Fix: fmt.Sprintf("unset it (git config --%s --unset core.hooksPath) and run `pre-commit install`, or call `pre-commit run --hook-stage <stage>` from a hook in %s", hp.Scope, hp.Value)})
		return
	}
	for _, stage := range missing {
		cmd := "pre-commit install"
		if stage != "pre-commit" {
			cmd = "pre-commit install --hook-type " + stage
		}
		msg := fmt.Sprintf("%s is configured, but the %s hook is not installed, so it never runs.", m.PreCommitConfig, stage)
		if f, ok := ownerOf(installed, stage); ok {
			msg = fmt.Sprintf("%s is configured for %s, but %s is a %s hook.", m.PreCommitConfig, stage, f.Rel, f.Owner)
		}
		id := "GH020"
		if stage == "pre-commit" {
			id = "GH018"
		}
		d.add(id, Finding{Hook: stage, Message: msg, Fix: "run `" + cmd + "`"})
		d.fix(Fix{RuleID: id, Kind: FixManual, Description: "install the pre-commit " + stage + " hook", Manual: cmd})
	}
}

func (d *diag) checkSimpleGitHooks(installed []*HookFile) {
	m := d.m
	if !m.SimpleGitHooks || d.opts.Static {
		return
	}
	var bad []string
	names := make([]string, 0, len(m.SimpleGitHooksHooks))
	for h := range m.SimpleGitHooksHooks {
		names = append(names, h)
	}
	sort.Strings(names)
	for _, h := range names {
		f, ok := ownerOf(installed, h)
		if !ok || !strings.Contains(string(f.Content), strings.TrimSpace(m.SimpleGitHooksHooks[h])) {
			bad = append(bad, h)
		}
	}
	if len(bad) > 0 {
		d.add("GH023", Finding{File: m.SimpleGitHooksConfig, Message: fmt.Sprintf("simple-git-hooks is configured for %s, but the installed hook is missing or does not match the configuration (simple-git-hooks only updates hooks when you run it).", strings.Join(bad, ", ")), Fix: "run `npx simple-git-hooks`"})
		d.fix(Fix{RuleID: "GH023", Kind: FixManual, Description: "install the simple-git-hooks hooks", Manual: "npx simple-git-hooks"})
	}
	if d.opts.Getenv("SKIP_SIMPLE_GIT_HOOKS") == "1" {
		d.add("GH008", Finding{Message: "SKIP_SIMPLE_GIT_HOOKS=1 is set in this environment, so simple-git-hooks skips every hook.", Fix: "unset SKIP_SIMPLE_GIT_HOOKS"})
	}
}

func (d *diag) checkGeneral(installed []*HookFile) {
	m := d.m
	if names := m.Names(); len(names) > 1 {
		owner := ""
		if !d.opts.Static {
			counts := map[string]int{}
			for _, f := range installed {
				counts[f.Owner]++
			}
			for _, n := range names {
				if counts[n] > 0 && owner == "" {
					owner = n
				}
			}
			if strings.HasPrefix(d.repo.Rel(d.hooksDir), ".husky") {
				owner = "husky"
			}
		}
		msg := fmt.Sprintf("Several hook managers are configured (%s). Git has one hooks directory, so the last one installed wins and the others' hooks silently stop running.", strings.Join(names, ", "))
		if owner != "" {
			msg += " Right now " + owner + " owns the hooks."
		}
		d.add("GH022", Finding{Message: msg, Fix: "pick one manager, or call the other from it (e.g. `pre-commit run` or `lefthook run pre-commit` inside a Husky hook)"})
	}
	if d.opts.Static {
		return
	}
	if m.Husky {
		if v := d.opts.Getenv("HUSKY"); v == "0" {
			d.add("GH008", Finding{Message: "HUSKY=0 is set in this environment, so Husky skips every hook (and `husky` does not install).", Fix: "unset HUSKY (check your shell profile and ~/.config/husky/init.sh)"})
		}
		if v := d.opts.Getenv("HUSKY_SKIP_HOOKS"); v == "1" || v == "true" {
			d.add("GH008", Finding{Message: "HUSKY_SKIP_HOOKS is set in this environment, so Husky v4 skips every hook.", Fix: "unset HUSKY_SKIP_HOOKS"})
		}
	}
	if m.PreCommit {
		if v := d.opts.Getenv("SKIP"); v != "" {
			d.add("GH008", Finding{Message: fmt.Sprintf("SKIP=%s is set in this environment, so pre-commit skips those hook ids.", v), Fix: "unset SKIP"})
		}
	}
	aliases := make([]string, 0, len(d.repo.Aliases))
	for k := range d.repo.Aliases {
		aliases = append(aliases, k)
	}
	sort.Strings(aliases)
	for _, k := range aliases {
		if strings.Contains(d.repo.Aliases[k], "--no-verify") {
			d.add("GH024", Finding{Message: fmt.Sprintf("The Git alias %q runs %q, which skips pre-commit and commit-msg hooks.", k, d.repo.Aliases[k]), Fix: fmt.Sprintf("remove --no-verify from the alias, or delete it: git config --unset alias.%s", k)})
		}
	}
	if strings.EqualFold(d.repo.AutoCRLF, "true") {
		var risky []string
		for _, f := range d.files {
			if _, tracked := d.repo.IndexMode(f.Rel); !tracked {
				continue
			}
			if eol, _ := Git(d.repo.Root, "check-attr", "eol", "--", f.Rel); !strings.HasSuffix(eol, ": lf") {
				risky = append(risky, f.Rel)
			}
		}
		sort.Strings(risky)
		if len(risky) > 0 {
			patterns := map[string]bool{}
			for _, r := range risky {
				patterns[gitattrPattern(r)] = true
			}
			var list []string
			for p := range patterns {
				list = append(list, p)
			}
			sort.Strings(list)
			d.add("GH025", Finding{File: risky[0], Message: fmt.Sprintf("core.autocrlf=true converts %s to CRLF on checkout, and .gitattributes does not force LF for hook scripts.", strings.Join(risky, ", ")), Fix: "add `" + list[0] + " text eol=lf` to .gitattributes"})
			for _, p := range list {
				d.fix(Fix{RuleID: "GH025", File: ".gitattributes", Kind: FixAppend, Text: p + " text eol=lf", Description: fmt.Sprintf("add `%s text eol=lf` to .gitattributes", p)})
			}
		}
	}
	if len(installed) == 0 && len(m.Names()) == 0 {
		d.add("GH015", Finding{Message: fmt.Sprintf("No hooks are installed in %s and no hook manager is configured.", d.repo.Rel(d.hooksDir))})
	}
}

// buildTable produces the per-hook status rows for `scan`.
func (d *diag) buildTable(installed, sources []*HookFile) {
	problems := map[string][]string{}
	for _, f := range d.rep.Findings {
		if f.File != "" {
			problems[f.File] = append(problems[f.File], ruleShort(f.RuleID))
		}
	}
	status := func(rel string) string {
		if p := problems[rel]; len(p) > 0 {
			return strings.Join(unique(p), ", ")
		}
		return "ok"
	}
	rows := map[string]*HookStatus{}
	order := []string{}
	ordered := map[string]bool{}
	put := func(h HookStatus) {
		key := h.Hook + "|" + h.Manager
		if !ordered[key] {
			ordered[key] = true
			order = append(order, key)
		}
		rows[key] = &h
	}
	for _, f := range installed {
		if !IsGitHook(f.Name) {
			put(HookStatus{Hook: f.Name, Manager: f.Owner, File: f.Rel, Installed: false, Status: status(f.Rel)})
			continue
		}
		mgr := f.Owner
		if strings.HasPrefix(f.Rel, ".husky/") {
			mgr = "husky"
		}
		put(HookStatus{Hook: f.Name, Manager: mgr, File: f.Rel, Installed: true, Status: status(f.Rel)})
	}
	for _, f := range sources {
		if !IsGitHook(f.Name) {
			put(HookStatus{Hook: f.Name, Manager: f.Owner, File: f.Rel, Status: status(f.Rel)})
			continue
		}
		inst := d.opts.Static
		if !d.opts.Static {
			inst = SameDir(filepath.Dir(f.Path), d.hooksDir) || (f.Owner == "husky" && strings.HasSuffix(filepath.ToSlash(d.hooksDir), ".husky/_") && fileExists(filepath.Join(d.hooksDir, f.Name)))
		}
		st := status(f.Rel)
		if !inst && st == "ok" {
			st = "not installed"
		}
		// Replace the generated .husky/_ wrapper row with the real hook.
		if f.Owner == "husky" {
			delete(rows, f.Name+"|husky")
		}
		put(HookStatus{Hook: f.Name, Manager: f.Owner, File: f.Rel, Installed: inst, Status: st})
	}
	if !d.opts.Static {
		for _, h := range d.m.LefthookHooks {
			if f, ok := ownerOf(installed, h); !ok || f.Owner != "lefthook" {
				put(HookStatus{Hook: h, Manager: "lefthook", File: d.m.LefthookConfig, Status: "not installed"})
			}
		}
		for _, h := range d.m.PreCommitStages {
			if f, ok := ownerOf(installed, h); !ok || f.Owner != "pre-commit" {
				put(HookStatus{Hook: h, Manager: "pre-commit", File: d.m.PreCommitConfig, Status: "not installed"})
			}
		}
	}
	for _, k := range order {
		if r, ok := rows[k]; ok {
			d.rep.Hooks = append(d.rep.Hooks, *r)
		}
	}
	sort.SliceStable(d.rep.Hooks, func(i, j int) bool { return hookOrder(d.rep.Hooks[i].Hook) < hookOrder(d.rep.Hooks[j].Hook) })
}

func hookOrder(name string) int {
	for i, h := range GitHooks {
		if h == name {
			return i
		}
	}
	return len(GitHooks)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func unique(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func ruleShort(id string) string {
	switch id {
	case "GH009":
		return "not executable"
	case "GH010":
		return "CRLF line endings"
	case "GH011":
		return "UTF-8 BOM"
	case "GH012":
		return "no #! line"
	case "GH013":
		return "interpreter missing"
	case "GH014":
		return "not a hook name"
	case "GH005":
		return "deprecated husky.sh line"
	case "GH026":
		return "command not on PATH"
	case "GH028":
		return "empty"
	case "GH025":
		return "may get CRLF"
	}
	return RuleByID(id).Name
}
