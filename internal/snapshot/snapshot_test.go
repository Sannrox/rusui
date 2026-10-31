package snapshot

import "testing"

func TestItemHashIncludesAssignees(t *testing.T) {
	base := Item{Repo: "example/test-repo", Item: 1, ItemKind: "issue", State: "open", Title: "t", Body: "b"}
	a := ItemHash(base)
	withAssignee := base
	withAssignee.Assignees = []string{"alice"}
	if ItemHash(withAssignee) == a {
		t.Fatal("assignee change must refresh the item hash")
	}
	withAssoc := base
	withAssoc.AuthorAssociation = "OWNER"
	if ItemHash(withAssoc) != a {
		t.Fatal("author association is stored, not hashed")
	}
}
