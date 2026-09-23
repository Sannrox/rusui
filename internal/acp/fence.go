package acp

import (
	"path"
	"slices"
	"strings"
)

// FenceGate rejects GitHub commands an implement session must not run
// (ADR 0017 D3) before consulting Next. It sees only permission requests
// the guest makes and matches command text, so it is workflow control, not
// a security boundary.
type FenceGate struct {
	Next PermissionGate
}

func (g FenceGate) Decide(p PermissionParams) Decision {
	title, _, cmd := toolCallFields(p.ToolCall)
	if FencedCommand(cmd) || (title != cmd && FencedCommand(title)) {
		return Decision{Matched: true, Allow: false}
	}
	if g.Next == nil {
		return DenyUnmatched{}.Decide(p)
	}
	return g.Next.Decide(p)
}

// fencedGH are gh subcommand prefixes (after "gh") that change repository
// state beyond pushing a branch and opening or editing a pull request.
var fencedGH = [][]string{
	{"pr", "merge"}, {"pr", "close"}, {"pr", "review"},
	{"release"}, {"repo"}, {"workflow"}, {"secret"}, {"variable"}, {"ruleset"},
	{"alias", "set"}, {"alias", "import"}, {"extension"},
}

var protectedBranches = map[string]bool{"main": true, "master": true}

// FencedCommand reports whether any command in a shell line is fenced. The
// line is lexed like a shell (quotes, escapes, separators, substitutions),
// and `sh -c` / `eval` payloads are checked recursively.
func FencedCommand(line string) bool {
	return fencedLine(line, 0)
}

func fencedLine(line string, depth int) bool {
	if depth > 4 {
		return true // refuse deeply nested wrappers rather than guess
	}
	for _, seg := range lexCommands(strings.ToLower(line)) {
		if fencedSegment(seg, depth) {
			return true
		}
	}
	return false
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

func fencedSegment(f []string, depth int) bool {
	for i := range f {
		switch base := path.Base(f[i]); {
		case shells[base]:
			for j := i + 1; j < len(f); j++ {
				if strings.HasPrefix(f[j], "-") && !strings.HasPrefix(f[j], "--") && strings.Contains(f[j], "c") && j+1 < len(f) {
					if fencedLine(f[j+1], depth+1) {
						return true
					}
					break
				}
			}
		case base == "eval":
			if fencedLine(strings.Join(f[i+1:], " "), depth+1) {
				return true
			}
		case base == "gh":
			if fencedGHArgs(f[i+1:]) {
				return true
			}
		case base == "git":
			rest := skipGitGlobals(f[i+1:])
			if gitAliasGlobal(f[i+1:]) {
				return true
			}
			if len(rest) > 0 && rest[0] == "push" && fencedPush(rest[1:]) {
				return true
			}
			if len(rest) > 1 && rest[0] == "config" && gitAliasKey(rest[1:]) {
				return true
			}
		}
	}
	return false
}

// lexCommands splits a shell line into commands of unquoted words. Quotes
// and backslashes are removed; separators, pipes, and command substitutions
// start a new command.
func lexCommands(line string) [][]string {
	var cmds [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	flushCmd := func() {
		flushWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	r := []rune(line)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r):
			i++
			if r[i] != '\n' {
				cur.WriteRune(r[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			for i++; i < len(r) && r[i] != '\''; i++ {
				cur.WriteRune(r[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(r) && r[i] != '"'; i++ {
				if r[i] == '\\' && i+1 < len(r) {
					i++
					cur.WriteRune(r[i]) // escaped: literal, never a substitution
					continue
				}
				// Command substitutions inside double quotes still run.
				if r[i] == '$' && i+1 < len(r) && r[i+1] == '(' {
					end := matchParen(r, i+1)
					cmds = append(cmds, lexCommands(string(r[i+2:end]))...)
					i = end
					continue
				}
				if r[i] == '`' {
					end := i + 1
					for end < len(r) && r[end] != '`' {
						end++
					}
					cmds = append(cmds, lexCommands(string(r[i+1:end]))...)
					i = end
					continue
				}
				cur.WriteRune(r[i])
			}
		case c == ' ' || c == '\t':
			flushWord()
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '`' || c == '(' || c == ')' || c == '{' || c == '}':
			flushCmd()
		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			flushCmd()
			i++
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	flushCmd()
	return cmds
}

// matchParen returns the index of the ')' closing the '(' at open, or the
// end of the line.
func matchParen(r []rune, open int) int {
	depth := 0
	for i := open; i < len(r); i++ {
		switch r[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(r)
}

// gitAliasGlobal is `git -c alias.x=…`, which can rename push or any
// fenced subcommand.
func gitAliasGlobal(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if gitGlobalsWithValue[args[0]] {
			if len(args) < 2 {
				return false
			}
			if strings.HasPrefix(args[1], "alias.") {
				return true
			}
			args = args[2:]
			continue
		}
		if strings.HasPrefix(args[0], "--config-env=alias.") {
			return true
		}
		args = args[1:]
	}
	return false
}

func gitAliasKey(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "alias.") {
			return true
		}
	}
	return false
}

func fencedGHArgs(args []string) bool {
	for _, p := range fencedGH {
		if len(args) >= len(p) && equalPrefix(args, p) {
			return true
		}
	}
	if len(args) >= 3 && args[0] == "pr" && args[1] == "ready" && slices.Contains(args[2:], "--undo") {
		return true
	}
	if len(args) >= 1 && args[0] == "api" {
		for _, a := range args[1:] {
			switch {
			case a == "-x" || a == "--method" || a == "-f" || a == "--field" || a == "--raw-field" || a == "--input",
				strings.HasPrefix(a, "-x") || strings.HasPrefix(a, "--method=") || strings.HasPrefix(a, "--field=") ||
					strings.HasPrefix(a, "--raw-field=") || strings.HasPrefix(a, "--input="):
				return true
			}
		}
	}
	return false
}

func fencedPush(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-f" || a == "--force" || a == "--delete" || a == "-d" || a == "--mirror" ||
			strings.HasPrefix(a, "--force") || a == "--all" || a == "--prune":
			return true
		case strings.HasPrefix(a, "+"):
			return true
		case strings.HasPrefix(a, "-"):
			continue
		}
		dst := a
		if i := strings.LastIndex(a, ":"); i >= 0 {
			if i == 0 {
				return true // ":branch" deletes the remote branch
			}
			dst = a[i+1:]
		}
		dst = strings.TrimPrefix(dst, "refs/heads/")
		if protectedBranches[dst] {
			return true
		}
	}
	return false
}

// gitGlobalsWithValue are git global options that take the next argument
// (lowercased: `-C dir` arrives as `-c dir`).
var gitGlobalsWithValue = map[string]bool{
	"-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--exec-path": true, "--config-env": true,
}

// skipGitGlobals drops git's global options (`-C dir`, `-c k=v`,
// `--git-dir=…`, …) so the subcommand is found.
func skipGitGlobals(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if gitGlobalsWithValue[args[0]] {
			if len(args) < 2 {
				return nil
			}
			args = args[2:]
			continue
		}
		args = args[1:]
	}
	return args
}

func equalPrefix(args, p []string) bool {
	for i := range p {
		if args[i] != p[i] {
			return false
		}
	}
	return true
}
