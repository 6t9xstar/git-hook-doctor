package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// FixKind is the type of change.
type FixKind string

const (
	FixExec       FixKind = "chmod"
	FixCRLF       FixKind = "crlf-to-lf"
	FixBOM        FixKind = "remove-bom"
	FixShebang    FixKind = "add-shebang"
	FixHuskyLines FixKind = "remove-husky-sh"
	FixRename     FixKind = "rename"
	FixAppend     FixKind = "append-line"
	FixCommand    FixKind = "git-config"
	FixManual     FixKind = "manual"
)

// Fix is one change GitHookDoctor can make (or tell you to make).
type Fix struct {
	RuleID      string   `json:"ruleId"`
	Kind        FixKind  `json:"kind"`
	File        string   `json:"file,omitempty"`
	Description string   `json:"description"`
	Command     []string `json:"command,omitempty"`
	Manual      string   `json:"manual,omitempty"`
	Target      string   `json:"target,omitempty"`
	Text        string   `json:"text,omitempty"`
	Disk        bool     `json:"-"`
	Index       bool     `json:"-"`
}

// Key identifies a fix for de-duplication.
func (f Fix) Key() string {
	return string(f.Kind) + "|" + f.File + "|" + f.Text + "|" + strings.Join(f.Command, " ") + "|" + f.Manual
}

// Automatic reports whether Apply can make this change.
func (f Fix) Automatic() bool { return f.Kind != FixManual }

// Preview describes exactly what Apply would do.
func (f Fix) Preview() string {
	switch f.Kind {
	case FixCommand:
		return "run: " + strings.Join(f.Command, " ")
	case FixManual:
		return "run yourself: " + f.Manual
	case FixExec:
		var parts []string
		if f.Disk {
			parts = append(parts, "chmod +x "+f.File)
		}
		if f.Index {
			parts = append(parts, "git update-index --chmod=+x "+f.File)
		}
		return strings.Join(parts, " && ")
	case FixRename:
		return fmt.Sprintf("mv %s %s", f.File, filepath.ToSlash(filepath.Join(filepath.Dir(f.File), f.Target)))
	case FixAppend:
		return fmt.Sprintf("append %q to %s", f.Text, f.File)
	case FixShebang:
		return fmt.Sprintf("insert \"#!/bin/sh\" as line 1 of %s", f.File)
	case FixHuskyLines:
		return fmt.Sprintf("delete the #!/usr/bin/env sh and husky.sh lines in %s", f.File)
	case FixCRLF:
		return fmt.Sprintf("rewrite %s with LF line endings", f.File)
	case FixBOM:
		return fmt.Sprintf("remove the first 3 bytes (EF BB BF) of %s", f.File)
	}
	return f.Description
}

// Apply makes the change in the repository at root.
func (f Fix) Apply(root string) error {
	path := filepath.Join(root, filepath.FromSlash(f.File))
	switch f.Kind {
	case FixManual:
		return fmt.Errorf("not applied automatically; run: %s", f.Manual)
	case FixCommand:
		cmd := exec.Command(f.Command[0], f.Command[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %s", strings.Join(f.Command, " "), strings.TrimSpace(string(out)))
		}
		return nil
	case FixExec:
		if f.Disk && runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			mode := info.Mode().Perm()
			// Add x wherever r is set (like chmod +x with a normal umask).
			mode |= (mode & 0o444) >> 2
			if err := os.Chmod(path, mode); err != nil {
				return err
			}
		}
		if f.Index {
			if _, err := Git(root, "update-index", "--chmod=+x", "--", f.File); err != nil {
				return err
			}
		}
		return nil
	case FixRename:
		target := filepath.Join(filepath.Dir(path), f.Target)
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("%s already exists", target)
		}
		return os.Rename(path, target)
	case FixAppend:
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte(f.Text)) {
			return nil
		}
		var buf bytes.Buffer
		buf.Write(data)
		if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
			buf.WriteByte('\n')
		}
		buf.WriteString(f.Text + "\n")
		return writeKeepMode(path, buf.Bytes())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch f.Kind {
	case FixCRLF:
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	case FixBOM:
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	case FixShebang:
		data = append([]byte("#!/bin/sh\n"), data...)
	case FixHuskyLines:
		data = RemoveHuskyLines(data)
	}
	return writeKeepMode(path, data)
}

// RemoveHuskyLines deletes the Husky v4-v8 preamble that v9 deprecates.
func RemoveHuskyLines(data []byte) []byte {
	crlf := bytes.Contains(data, []byte("\r\n"))
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var out []string
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if i == 0 && (t == "#!/usr/bin/env sh" || t == "#!/bin/sh") && len(lines) > 1 && huskyShLine.MatchString(lines[1]) {
			continue
		}
		if huskyShLine.MatchString(line) {
			// Also drop one blank line that followed the preamble.
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				lines[i+1] = "\x00"
			}
			continue
		}
		if line == "\x00" {
			continue
		}
		out = append(out, line)
	}
	res := strings.Join(out, "\n")
	if crlf {
		res = strings.ReplaceAll(res, "\n", "\r\n")
	}
	return []byte(res)
}

func writeKeepMode(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, data, mode)
}
