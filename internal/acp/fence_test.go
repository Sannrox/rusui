package acp

import (
	"encoding/json"
	"testing"
)

func TestFencedCommands(t *testing.T) {
	for _, cmd := range []string{
		"gh pr merge 12 --squash",
		"GH PR MERGE 12",
		"gh pr close 12",
		"gh pr review 12 --approve",
		"gh pr ready 12 --undo",
		"gh release create v1",
		"gh repo edit --default-branch x",
		"gh workflow run build",
		"gh secret set TOKEN",
		"gh variable set X",
		"gh ruleset list",
		"gh api -X DELETE repos/o/r",
		"gh api repos/o/r/pulls/1/merge --method PUT",
		"gh api repos/o/r/issues -f title=x",
		"gh api --input body.json repos/o/r/issues",
		"git push origin main",
		"git push origin HEAD:main",
		"git push origin HEAD:refs/heads/master",
		"git push --force origin feature",
		"git push -f origin feature",
		"git push --force-with-lease origin feature",
		"git push origin +feature",
		"git push origin :feature",
		"git push --delete origin feature",
		"git push --mirror origin",
		"git add . && git commit -m x && git push origin main",
		"echo ok; gh pr merge 3",
		"make test | tee log && gh release create v2",
		"/usr/local/bin/gh pr merge 12",
		"env GH_TOKEN=x gh pr merge 12",
		"command gh pr merge 12",
		"git -C /repo push origin main",
		"git -c push.default=current push --force",
		"/usr/bin/git --git-dir .git push origin main",
	} {
		if !FencedCommand(cmd) {
			t.Errorf("not fenced: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"gh pr create --fill",
		"gh pr edit 12 --body x",
		"gh pr view 12",
		"gh pr list",
		"gh pr checks 12",
		"gh pr ready 12",
		"gh api repos/o/r/pulls/12",
		"gh issue view 3",
		"git push -u origin feature",
		"git push origin feature/main-fix",
		"git push origin HEAD:rusui/5/work",
		"git commit -m 'merge main into feature'",
		"git fetch origin main",
		"git -C /repo push -u origin feature",
		"git -C /repo log main",
	} {
		if FencedCommand(cmd) {
			t.Errorf("fenced but allowed by ADR 0017: %q", cmd)
		}
	}
}

func TestFenceGateRejectsBeforePolicyAllows(t *testing.T) {
	g := FenceGate{Next: RulesGate{Rules: []Rule{{Command: "gh"}, {Command: "git"}}}}
	ask := func(cmd string) Decision {
		raw, _ := json.Marshal(map[string]string{"toolCallId": "tc", "title": "run_terminal_command", "command": cmd})
		return g.Decide(PermissionParams{ToolCall: raw})
	}
	if d := ask("gh pr merge 1"); !d.Matched || d.Allow {
		t.Fatalf("merge %+v", d)
	}
	if d := ask("gh pr create --fill"); !d.Matched || !d.Allow {
		t.Fatalf("create %+v", d)
	}
	if d := (FenceGate{}).Decide(PermissionParams{ToolCall: json.RawMessage(`{"command":"ls"}`)}); d.Matched {
		t.Fatalf("nil next %+v", d)
	}
}

func TestRejectRulesWinOverAllowRules(t *testing.T) {
	g := RulesGate{Rules: []Rule{{Command: "npm"}, {Command: "npm publish", Action: RuleReject}}}
	raw := func(cmd string) PermissionParams {
		b, _ := json.Marshal(map[string]string{"command": cmd})
		return PermissionParams{ToolCall: b}
	}
	if d := g.Decide(raw("npm publish --access public")); !d.Matched || d.Allow {
		t.Fatalf("publish %+v", d)
	}
	if d := g.Decide(raw("npm test")); !d.Matched || !d.Allow {
		t.Fatalf("test %+v", d)
	}
	if d := (RulesGate{Rules: []Rule{{Action: RuleReject}}}).Decide(raw("ls")); d.Matched {
		t.Fatalf("empty reject rule rejected everything: %+v", d)
	}
}

func TestFenceSeesThroughQuotesWrappersAndAliases(t *testing.T) {
	for _, cmd := range []string{
		`bash -c 'gh pr merge 1'`,
		`sh -c "gh pr merge 1 --squash"`,
		`/bin/zsh -lc 'cd x && gh pr close 3'`,
		`bash -c "bash -c 'gh release create v1'"`,
		`git push origin 'main'`,
		`git push origin HEAD:'main'`,
		`git push origin '+main'`,
		`git push origin "HEAD:refs/heads/main"`,
		`"/usr/bin/gh" pr merge 1`,
		`\gh pr merge 1`,
		`eval "gh pr merge 1"`,
		`g''h pr merge 1`,
		`echo "$(gh pr merge 1)"`,
		`gh pr create --body "$(gh pr merge 1)"`,
		`gh alias set m 'pr merge'`,
		`gh alias import aliases.yml`,
		`gh extension install someone/gh-merge`,
		`git -c alias.p=push p origin main`,
		`git -C /repo -c alias.p=push p origin main`,
		`git --git-dir .git -c alias.p=push p origin main`,
		`git --config-env=alias.p=ENV p origin main`,
		`git --namespace ns -c alias.p=push p origin main`,
		`git config alias.p push`,
		`FOO=1 gh pr merge 1`,
		`nohup gh pr merge 1 &`,
		`(gh pr merge 1)`,
		`{ gh pr merge 1; }`,
	} {
		if !FencedCommand(cmd) {
			t.Errorf("not fenced: %s", cmd)
		}
	}
	for _, cmd := range []string{
		`bash -c 'go test ./...'`,
		`sh -c "gh pr create --fill"`,
		`git push origin 'feature/main-fix'`,
		`git commit -m 'gh pr merge is fenced'`,
		`echo "git push origin main"`,
		`gh alias list`,
		`git config user.name rusui`,
		`git -c core.pager=less push origin feature`,
		`gh pr create --body "never run \$(gh pr merge 1) here"`,
		`gh pr create --body "use \$(make) and \` + "`" + `ls\` + "`" + ` literally"`,
	} {
		if FencedCommand(cmd) {
			t.Errorf("fenced but allowed: %s", cmd)
		}
	}
}
