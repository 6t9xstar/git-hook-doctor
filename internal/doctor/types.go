// Package doctor inspects a Git repository's hooks setup (core.hooksPath,
// Husky, Lefthook, pre-commit, simple-git-hooks and plain hook scripts) and
// explains why a hook does not run.
package doctor

// Severity of a finding.
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

// Rank orders severities for --fail-on.
func (s Severity) Rank() int {
	switch s {
	case SevError:
		return 3
	case SevWarning:
		return 2
	case SevInfo:
		return 1
	}
	return 0
}

// Finding is one problem.
type Finding struct {
	RuleID   string   `json:"ruleId"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	File     string   `json:"file,omitempty"` // relative to the repository root, forward slashes
	Line     int      `json:"line,omitempty"`
	Hook     string   `json:"hook,omitempty"`
}

// Rule describes a check.
type Rule struct {
	ID          string
	Severity    Severity
	Name        string
	Static      bool // also runs in --static mode (only looks at committed files)
	Description string
}

// Rules is the rule catalogue.
var Rules = []Rule{
	{"GH001", SevError, "hooks-path-missing", false, "core.hooksPath points to a directory that does not exist, so no hook runs"},
	{"GH002", SevWarning, "hooks-path-global", false, "core.hooksPath is set in the global or system config and overrides every repository's hooks"},
	{"GH003", SevError, "hooks-ignored", false, "Hook scripts in .git/hooks are ignored because core.hooksPath points elsewhere"},
	{"GH004", SevError, "husky-not-installed", false, "Husky is configured but core.hooksPath does not point to its directory"},
	{"GH005", SevWarning, "husky-deprecated-lines", true, "A Husky v9 hook still sources husky.sh (deprecated, fails in Husky v10)"},
	{"GH006", SevWarning, "husky-prepare-script", true, "package.json has no prepare script that installs Husky, or uses the deprecated `husky install`"},
	{"GH007", SevError, "husky-v4-config", true, "Husky v4 configuration is ignored by Husky 5 and later"},
	{"GH008", SevInfo, "hooks-disabled-env", false, "An environment variable disables the hook manager (HUSKY=0, LEFTHOOK=0, ...)"},
	{"GH009", SevError, "not-executable", true, "A hook file is not executable (on disk or in the Git index)"},
	{"GH010", SevError, "crlf-line-endings", true, "A hook file has Windows (CRLF) line endings"},
	{"GH011", SevError, "utf8-bom", true, "A hook file starts with a UTF-8 byte order mark, so the shebang is not recognised"},
	{"GH012", SevWarning, "missing-shebang", true, "A hook file has no #! line"},
	{"GH013", SevError, "interpreter-missing", false, "The interpreter in a hook's #! line is not installed"},
	{"GH014", SevWarning, "misnamed-hook", true, "A file in a hooks directory is not a Git hook name, so Git never runs it"},
	{"GH015", SevInfo, "no-hooks", false, "No hooks are installed"},
	{"GH016", SevError, "lefthook-not-installed", false, "Lefthook is configured but its hooks are not installed"},
	{"GH017", SevWarning, "lefthook-unknown-hook", true, "lefthook.yml has a key that is not a Git hook or a Lefthook option"},
	{"GH018", SevError, "pre-commit-not-installed", false, "pre-commit is configured but its Git hook is not installed"},
	{"GH019", SevError, "pre-commit-python-missing", false, "The installed pre-commit hook points to a Python that no longer exists"},
	{"GH020", SevWarning, "pre-commit-stage-not-installed", false, "pre-commit hooks use a stage whose Git hook is not installed"},
	{"GH021", SevInfo, "pre-commit-deprecated-stage", true, "pre-commit config uses deprecated stage names (commit, push, merge-commit)"},
	{"GH022", SevWarning, "multiple-managers", true, "More than one hook manager is configured; only one owns the hooks directory"},
	{"GH023", SevError, "simple-git-hooks-not-installed", false, "simple-git-hooks is configured but a hook is missing or outdated"},
	{"GH024", SevInfo, "no-verify-alias", false, "A Git alias skips hooks with --no-verify"},
	{"GH025", SevWarning, "autocrlf-without-eol", false, "core.autocrlf=true and .gitattributes does not force LF for hook scripts"},
	{"GH026", SevWarning, "command-not-found", false, "A hook calls a tool (npx, node, python, ...) that is not on PATH"},
	{"GH027", SevError, "pre-commit-hooks-path", false, "pre-commit refuses to install while core.hooksPath is set"},
	{"GH028", SevInfo, "empty-hook", true, "A hook file is empty"},
	{"GH029", SevError, "invalid-config", true, "A hook manager configuration file cannot be parsed"},
}

// RuleByID returns a rule definition.
func RuleByID(id string) Rule {
	for _, r := range Rules {
		if r.ID == id {
			return r
		}
	}
	return Rule{ID: id, Severity: SevWarning}
}

// GitHooks lists every hook name Git runs (githooks(5)).
var GitHooks = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit", "pre-merge-commit",
	"prepare-commit-msg", "commit-msg", "post-commit", "pre-rebase", "post-checkout",
	"post-merge", "pre-push", "pre-receive", "update", "proc-receive", "post-receive",
	"post-update", "reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite",
	"sendemail-validate", "fsmonitor-watchman", "p4-changelist", "p4-prepare-changelist",
	"p4-post-changelist", "p4-pre-submit", "post-index-change",
}

// IsGitHook reports whether name is a hook Git runs.
func IsGitHook(name string) bool {
	for _, h := range GitHooks {
		if h == name {
			return true
		}
	}
	return false
}
