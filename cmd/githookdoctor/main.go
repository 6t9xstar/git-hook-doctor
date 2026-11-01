// Command githookdoctor explains why Git hooks do not run: core.hooksPath,
// Husky, Lefthook, pre-commit, simple-git-hooks, executable bits, CRLF line
// endings, shebangs and missing tools. It never changes anything unless you
// approve each change.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/6t9xstar/git-hook-doctor/internal/doctor"
	"github.com/6t9xstar/git-hook-doctor/internal/report"
)

var version = "1.0.0"

const usage = `GitHookDoctor %s — find out why your Git hooks do not run.

Usage:
  githookdoctor scan                  List hooks, managers and their status
  githookdoctor diagnose [hook]       Explain every problem (optionally for one hook, e.g. pre-commit)
  githookdoctor fix [--dry-run]       Preview the fixes (default: nothing is changed)
  githookdoctor fix --apply           Apply fixes, asking before each change
  githookdoctor rules                 List all checks
  githookdoctor version

Options:
  -C, --dir <path>        Repository to inspect (default: current directory)
  --static                Only check committed files (for CI, where hooks are not installed)
  --format <fmt>          text | json | markdown | sarif | github (default text)
  -o, --output <file>     Write the report to a file
  --fail-on <level>       error | warning | info | none (default error)
  --no-color              Disable colors

Fix options:
  --dry-run               Show what would change (default)
  --apply                 Apply changes; asks for confirmation before each one
  --yes                   With --apply: approve every change without asking
  --only <ids>            Only fixes for these rules, e.g. --only GH009,GH010

Exit codes: 0 = no findings at --fail-on, 1 = findings, 2 = usage error.
Docs: https://github.com/6t9xstar/git-hook-doctor
`

type opts struct {
	dir, format, output, failOn, only string
	static, noColor                   bool
	dryRun, apply, yes                bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "scan"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "help":
		fmt.Fprintf(stdout, usage, version)
		return 0
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "rules":
		for _, r := range doctor.Rules {
			mode := ""
			if r.Static {
				mode = " (static)"
			}
			fmt.Fprintf(stdout, "%s  %-8s %-31s %s%s\n", r.ID, r.Severity, r.Name, r.Description, mode)
		}
		return 0
	case "scan", "diagnose", "fix":
	default:
		fmt.Fprintf(stderr, "githookdoctor: unknown command %q (scan, diagnose, fix, rules, version)\n", cmd)
		return 2
	}
	var o opts
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, usage, version) }
	fs.StringVar(&o.dir, "dir", ".", "")
	fs.StringVar(&o.dir, "C", ".", "")
	fs.StringVar(&o.format, "format", "text", "")
	fs.StringVar(&o.output, "output", "", "")
	fs.StringVar(&o.output, "o", "", "")
	fs.StringVar(&o.failOn, "fail-on", "error", "")
	fs.BoolVar(&o.static, "static", false, "")
	fs.BoolVar(&o.noColor, "no-color", false, "")
	showVersion := fs.Bool("version", false, "")
	if cmd == "fix" {
		fs.BoolVar(&o.dryRun, "dry-run", false, "")
		fs.BoolVar(&o.apply, "apply", false, "")
		fs.BoolVar(&o.yes, "yes", false, "")
		fs.BoolVar(&o.yes, "y", false, "")
		fs.StringVar(&o.only, "only", "", "")
	}
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	hook := ""
	if fs.NArg() > 0 {
		if cmd != "diagnose" || fs.NArg() > 1 {
			fmt.Fprintf(stderr, "githookdoctor %s: unexpected argument %q\n", cmd, fs.Arg(0))
			return 2
		}
		hook = fs.Arg(0)
		if !doctor.IsGitHook(hook) {
			msg := fmt.Sprintf("githookdoctor diagnose: %q is not a Git hook", hook)
			if s := doctor.SuggestHookName(hook); s != "" {
				msg += fmt.Sprintf(" (did you mean %s?)", s)
			}
			fmt.Fprintln(stderr, msg)
			return 2
		}
	}
	if !contains(report.Formats, o.format) {
		fmt.Fprintf(stderr, "githookdoctor: unknown --format %q (%s)\n", o.format, strings.Join(report.Formats, ", "))
		return 2
	}
	failRank, ok := map[string]int{"error": 3, "warning": 2, "info": 1, "none": 99}[o.failOn]
	if !ok {
		fmt.Fprintf(stderr, "githookdoctor: unknown --fail-on %q (error, warning, info, none)\n", o.failOn)
		return 2
	}
	if o.apply && o.dryRun {
		fmt.Fprintln(stderr, "githookdoctor fix: use either --dry-run or --apply")
		return 2
	}
	if st, err := os.Stat(o.dir); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "githookdoctor: %s is not a directory\n", o.dir)
		return 2
	}

	rep, err := doctor.Diagnose(o.dir, doctor.Options{Static: o.static})
	if err != nil {
		fmt.Fprintln(stderr, "githookdoctor:", err)
		return 2
	}
	if cmd == "fix" {
		return fixCmd(rep, o, stdin, stdout, stderr)
	}

	var w io.Writer = stdout
	color := !o.noColor && os.Getenv("NO_COLOR") == "" && isTerminal(stdout)
	if o.output != "" {
		f, err := os.Create(o.output)
		if err != nil {
			fmt.Fprintln(stderr, "githookdoctor:", err)
			return 2
		}
		defer f.Close()
		w, color = f, false
	}
	if hook != "" && o.format != "text" {
		rep.Findings = report.Filter(rep.Findings, hook)
	}
	ro := report.Options{Color: color, Version: version, Table: true, Details: cmd == "diagnose", Hook: hook, Command: cmd}
	if err := report.Write(w, o.format, rep, ro); err != nil {
		fmt.Fprintln(stderr, "githookdoctor:", err)
		return 2
	}
	for _, f := range report.Filter(rep.Findings, hook) {
		if f.Severity.Rank() >= failRank {
			return 1
		}
	}
	return 0
}

func fixCmd(rep *doctor.Report, o opts, stdin io.Reader, stdout, stderr io.Writer) int {
	fixes := rep.Fixes
	if o.only != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(o.only, ",") {
			want[strings.ToUpper(strings.TrimSpace(id))] = true
		}
		var keep []doctor.Fix
		for _, f := range fixes {
			if want[f.RuleID] {
				keep = append(keep, f)
			}
		}
		fixes = keep
	}
	if o.format == "json" {
		if o.apply {
			fmt.Fprintln(stderr, "githookdoctor fix: --format json only previews fixes; drop --apply")
			return 2
		}
		return writeJSONPlan(stdout, fixes)
	}
	if len(fixes) == 0 {
		fmt.Fprintln(stdout, "Nothing to fix.")
		if len(rep.Findings) > 0 {
			fmt.Fprintln(stdout, "Some findings need a decision from you; run `githookdoctor diagnose` for details.")
		}
		return 0
	}
	var auto, manual []doctor.Fix
	for _, f := range fixes {
		if f.Automatic() {
			auto = append(auto, f)
		} else {
			manual = append(manual, f)
		}
	}
	if !o.apply {
		fmt.Fprintln(stdout, "Planned changes (dry run: nothing was modified)")
		fmt.Fprintln(stdout)
		for i, f := range auto {
			fmt.Fprintf(stdout, "%2d. [%s] %s\n      %s\n", i+1, f.RuleID, f.Description, f.Preview())
		}
		if len(manual) > 0 {
			if len(auto) > 0 {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintln(stdout, "Run these yourself (they install or reconfigure a hook manager):")
			for _, f := range manual {
				fmt.Fprintf(stdout, "  - [%s] %s: %s\n", f.RuleID, f.Description, f.Manual)
			}
		}
		if len(auto) > 0 {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Apply with `githookdoctor fix --apply`. You will be asked to approve each change.")
		}
		return 0
	}
	if len(auto) == 0 {
		fmt.Fprintln(stdout, "No automatic fixes. Run these yourself:")
		for _, f := range manual {
			fmt.Fprintf(stdout, "  - [%s] %s: %s\n", f.RuleID, f.Description, f.Manual)
		}
		return 0
	}
	reader := bufio.NewReader(stdin)
	applied, skipped, failed := 0, 0, 0
	for i, f := range auto {
		fmt.Fprintf(stdout, "[%d/%d] %s\n        %s\n", i+1, len(auto), f.Description, f.Preview())
		if !o.yes {
			fmt.Fprint(stdout, "        Apply this change? [y/N/q] ")
			// No answer (closed or empty stdin) means no: nothing changes without approval.
			answer, _ := reader.ReadString('\n')
			answer = strings.ToLower(strings.TrimSpace(answer))
			if !isTerminal(stdin) {
				fmt.Fprintln(stdout, answer)
			}
			if answer == "q" || answer == "quit" {
				skipped += len(auto) - i
				break
			}
			if answer != "y" && answer != "yes" {
				fmt.Fprintln(stdout, "        skipped")
				skipped++
				continue
			}
		}
		if err := f.Apply(rep.Repo.Root); err != nil {
			fmt.Fprintf(stdout, "        failed: %v\n", err)
			failed++
			continue
		}
		fmt.Fprintln(stdout, "        done")
		applied++
	}
	fmt.Fprintf(stdout, "\n%d applied, %d skipped, %d failed.\n", applied, skipped, failed)
	if len(manual) > 0 {
		fmt.Fprintln(stdout, "Still to do by hand:")
		for _, f := range manual {
			fmt.Fprintf(stdout, "  - %s: %s\n", f.Description, f.Manual)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func writeJSONPlan(w io.Writer, fixes []doctor.Fix) int {
	type item struct {
		doctor.Fix
		Preview   string `json:"preview"`
		Automatic bool   `json:"automatic"`
	}
	list := []item{}
	for _, f := range fixes {
		list = append(list, item{Fix: f, Preview: f.Preview(), Automatic: f.Automatic()})
	}
	_ = report.WriteJSON(w, map[string]any{"tool": "GitHookDoctor", "version": version, "dryRun": true, "fixes": list})
	return 0
}

// reorder moves flags in front of positional arguments ("diagnose pre-commit --format json").
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			if f := fs.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue
				}
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i++
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
