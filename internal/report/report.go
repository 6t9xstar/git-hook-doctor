// Package report renders GitHookDoctor results as text, JSON, Markdown, SARIF
// or GitHub Actions annotations.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/6t9xstar/git-hook-doctor/internal/doctor"
)

// Formats supported by Write.
var Formats = []string{"text", "json", "markdown", "sarif", "github"}

// Options for rendering.
type Options struct {
	Color   bool
	Version string
	Table   bool   // show the hook table (scan, diagnose)
	Details bool   // show every finding (diagnose)
	Hook    string // diagnose <hook>
	Command string // scan | diagnose
}

type painter bool

func (p painter) c(code, s string) string {
	if !p {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Write renders a report.
func Write(w io.Writer, format string, rep *doctor.Report, o Options) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"tool": "GitHookDoctor", "version": o.Version, "report": rep, "summary": summary(rep.Findings)})
	case "sarif":
		return sarif(w, rep, o.Version)
	case "markdown":
		return markdown(w, rep)
	case "github":
		for _, f := range rep.Findings {
			cmd := map[doctor.Severity]string{doctor.SevError: "error", doctor.SevWarning: "warning", doctor.SevInfo: "notice"}[f.Severity]
			props := "title=" + esc(f.RuleID+" "+doctor.RuleByID(f.RuleID).Name, true)
			if f.File != "" {
				props = "file=" + esc(f.File, true) + "," + props
				if f.Line > 0 {
					props = fmt.Sprintf("file=%s,line=%d,%s", esc(f.File, true), f.Line, "title="+esc(f.RuleID+" "+doctor.RuleByID(f.RuleID).Name, true))
				}
			}
			msg := f.Message
			if f.Fix != "" {
				msg += "\nFix: " + f.Fix
			}
			fmt.Fprintf(w, "::%s %s::%s\n", cmd, props, esc(msg, false))
		}
		return nil
	}
	text(w, rep, o)
	return nil
}

func summary(fs []doctor.Finding) map[string]int {
	s := map[string]int{"error": 0, "warning": 0, "info": 0}
	for _, f := range fs {
		s[string(f.Severity)]++
	}
	return s
}

func esc(s string, prop bool) string {
	r := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	s = r.Replace(s)
	if prop {
		s = strings.NewReplacer(":", "%3A", ",", "%2C").Replace(s)
	}
	return s
}

func managerLine(m *doctor.Managers) string {
	var parts []string
	if m.Husky {
		s := "Husky"
		if m.HuskyVersion > 0 {
			s += fmt.Sprintf(" v%d", m.HuskyVersion)
		}
		if m.HuskyVersionAt != "" {
			s += " (" + m.HuskyVersionAt + ")"
		}
		parts = append(parts, s)
	}
	if m.Lefthook {
		parts = append(parts, "Lefthook ("+m.LefthookConfig+")")
	}
	if m.PreCommit {
		parts = append(parts, "pre-commit ("+m.PreCommitConfig+")")
	}
	if m.SimpleGitHooks {
		parts = append(parts, "simple-git-hooks ("+m.SimpleGitHooksConfig+")")
	}
	if len(parts) == 0 {
		return "none (plain Git hooks)"
	}
	return strings.Join(parts, ", ")
}

// Filter keeps findings about one hook (plus repository-wide findings).
func Filter(fs []doctor.Finding, hook string) []doctor.Finding {
	if hook == "" {
		return fs
	}
	var out []doctor.Finding
	for _, f := range fs {
		if f.Hook == "" || f.Hook == hook || strings.HasSuffix(f.File, "/"+hook) {
			out = append(out, f)
		}
	}
	return out
}

func text(w io.Writer, rep *doctor.Report, o Options) {
	p := painter(o.Color)
	fmt.Fprintln(w, p.c("1", "GIT HOOKS REPORT"))
	repoLine := rep.Repo.Root
	if rep.Repo.GitVersion != "" {
		repoLine += "  ·  git " + rep.Repo.GitVersion
	}
	fmt.Fprintln(w, p.c("90", "Repository: "+repoLine))
	if rep.Static {
		fmt.Fprintln(w, p.c("90", "Mode:       static (committed files only)"))
	} else {
		hd := rep.HooksDir
		if rep.HooksFrom == "default" {
			hd += "  (default)"
		} else if rep.HooksFrom != "" {
			hd += "  (" + rep.HooksFrom + ")"
		}
		fmt.Fprintln(w, p.c("90", "Hooks dir:  "+hd))
	}
	fmt.Fprintln(w, p.c("90", "Managers:   "+managerLine(rep.Managers)))
	fmt.Fprintln(w)

	hooks := rep.Hooks
	if o.Hook != "" {
		var keep []doctor.HookStatus
		for _, h := range hooks {
			if h.Hook == o.Hook {
				keep = append(keep, h)
			}
		}
		hooks = keep
		if len(hooks) == 0 {
			fmt.Fprintf(w, "No %s hook is installed or configured.\n\n", o.Hook)
		}
	}
	if o.Table && len(hooks) > 0 {
		wHook, wMgr, wFile := 4, 7, 4
		for _, h := range hooks {
			wHook = max(wHook, len(h.Hook))
			wMgr = max(wMgr, len(h.Manager))
			wFile = max(wFile, len(h.File))
		}
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", wHook, "HOOK", wMgr, "MANAGER", wFile, "FILE", "STATUS")
		for _, h := range hooks {
			st := h.Status
			switch {
			case st == "ok":
				st = p.c("32", "✔ ok")
			case st == "not installed":
				st = p.c("33", "✖ not installed")
			default:
				st = p.c("31", "✖ "+st)
			}
			fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", wHook, h.Hook, wMgr, h.Manager, wFile, h.File, st)
		}
		fmt.Fprintln(w)
	} else if o.Table && o.Hook == "" {
		fmt.Fprintln(w, "No hooks found.")
		fmt.Fprintln(w)
	}

	findings := Filter(rep.Findings, o.Hook)
	if o.Details {
		for _, f := range findings {
			label := map[doctor.Severity]string{doctor.SevError: p.c("31", "[ERROR]  "), doctor.SevWarning: p.c("33", "[WARNING]"), doctor.SevInfo: p.c("34", "[INFO]   ")}[f.Severity]
			fmt.Fprintf(w, "%s %s\n", label, f.Message)
			loc := f.RuleID
			if f.File != "" {
				loc += " " + f.File
				if f.Line > 0 {
					loc += fmt.Sprintf(":%d", f.Line)
				}
			}
			line := "          " + p.c("90", loc)
			if f.Fix != "" {
				line += "\n          " + p.c("36", "Fix:") + " " + f.Fix
			}
			fmt.Fprintln(w, line)
		}
		if len(findings) > 0 {
			fmt.Fprintln(w)
		}
	}
	for _, n := range rep.Notes {
		fmt.Fprintln(w, p.c("90", "Note: "+n))
	}
	s := summary(findings)
	if len(findings) == 0 {
		if o.Hook != "" {
			fmt.Fprintln(w, p.c("32", "✔ Nothing stops the "+o.Hook+" hook from running."))
		} else {
			fmt.Fprintln(w, p.c("32", "✔ Git hooks look healthy."))
		}
		return
	}
	fmt.Fprintf(w, "%d error(s), %d warning(s), %d info.", s["error"], s["warning"], s["info"])
	auto := 0
	for _, fx := range rep.Fixes {
		if fx.Automatic() {
			auto++
		}
	}
	switch {
	case !o.Details:
		fmt.Fprint(w, " Run `githookdoctor diagnose` for details.")
	case auto > 0:
		fmt.Fprintf(w, " Run `githookdoctor fix --dry-run` to preview %d fix(es).", auto)
	}
	fmt.Fprintln(w)
}

func markdown(w io.Writer, rep *doctor.Report) error {
	fmt.Fprintln(w, "## Git hooks report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**Managers:** %s", managerLine(rep.Managers))
	if !rep.Static {
		fmt.Fprintf(w, " · **Hooks dir:** `%s`", rep.HooksDir)
	} else {
		fmt.Fprint(w, " · static mode")
	}
	fmt.Fprintln(w)
	if len(rep.Hooks) > 0 {
		fmt.Fprintln(w, "\n| Hook | Manager | File | Status |\n| --- | --- | --- | --- |")
		for _, h := range rep.Hooks {
			icon := ":white_check_mark:"
			if h.Status != "ok" {
				icon = ":x:"
			}
			fmt.Fprintf(w, "| `%s` | %s | `%s` | %s %s |\n", h.Hook, h.Manager, h.File, icon, h.Status)
		}
	}
	if len(rep.Findings) == 0 {
		fmt.Fprintln(w, "\n:white_check_mark: Git hooks look healthy.")
		return nil
	}
	fmt.Fprintln(w, "\n| Severity | Rule | Location | Message |\n| --- | --- | --- | --- |")
	for _, f := range rep.Findings {
		loc := ""
		if f.File != "" {
			loc = "`" + f.File
			if f.Line > 0 {
				loc += fmt.Sprintf(":%d", f.Line)
			}
			loc += "`"
		}
		msg := f.Message
		if f.Fix != "" {
			msg += "<br>**Fix:** " + f.Fix
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", f.Severity, f.RuleID, loc, strings.ReplaceAll(msg, "|", "\\|"))
	}
	return nil
}

func sarif(w io.Writer, rep *doctor.Report, version string) error {
	used := map[string]bool{}
	for _, f := range rep.Findings {
		used[f.RuleID] = true
	}
	var ids []string
	for id := range used {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	level := map[doctor.Severity]string{doctor.SevError: "error", doctor.SevWarning: "warning", doctor.SevInfo: "note"}
	rules := []map[string]any{}
	for _, id := range ids {
		r := doctor.RuleByID(id)
		rules = append(rules, map[string]any{
			"id": id, "name": r.Name,
			"shortDescription":     map[string]string{"text": r.Description},
			"helpUri":              "https://github.com/6t9xstar/git-hook-doctor#rules",
			"defaultConfiguration": map[string]string{"level": level[r.Severity]},
		})
	}
	results := []map[string]any{}
	for _, f := range rep.Findings {
		file := f.File
		if file == "" {
			file = ".git/config"
			if rep.Static {
				file = "."
			}
		}
		region := map[string]int{"startLine": max(f.Line, 1)}
		msg := f.Message
		if f.Fix != "" {
			msg += " Fix: " + f.Fix
		}
		results = append(results, map[string]any{
			"ruleId": f.RuleID, "level": level[f.Severity],
			"message": map[string]string{"text": msg},
			"locations": []map[string]any{{"physicalLocation": map[string]any{
				"artifactLocation": map[string]string{"uri": file, "uriBaseId": "%SRCROOT%"},
				"region":           region,
			}}},
		})
	}
	doc := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []map[string]any{{
			"tool": map[string]any{"driver": map[string]any{
				"name": "GitHookDoctor", "version": version, "semanticVersion": version,
				"informationUri": "https://github.com/6t9xstar/git-hook-doctor", "rules": rules,
			}},
			"results": results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// WriteJSON writes v as indented JSON.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
