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
}

var protectedBranches = map[string]bool{"main": true, "master": true}

// FencedCommand reports whether any command in a shell line is fenced.
func FencedCommand(line string) bool {
	for _, seg := range splitCommands(line) {
		f := strings.Fields(seg)
		for i := range f {
			switch path.Base(f[i]) {
			case "gh":
				if fencedGHArgs(f[i+1:]) {
					return true
				}
			case "git":
				rest := skipGitGlobals(f[i+1:])
				if len(rest) > 0 && rest[0] == "push" && fencedPush(rest[1:]) {
					return true
				}
			}
		}
	}
	return false
}

func splitCommands(line string) []string {
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "`", "\n", "$(", "\n", ")", "\n")
	return strings.Split(r.Replace(strings.ToLower(line)), "\n")
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

// skipGitGlobals drops git's global options (`-C dir`, `-c k=v`,
// `--git-dir=…`, …) so the subcommand is found.
func skipGitGlobals(args []string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env":
			if len(args) < 2 {
				return nil
			}
			args = args[2:]
		default:
			args = args[1:]
		}
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
