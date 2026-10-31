package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestItemHashIncludesAssignees(t *testing.T) {
	base := Item{Repo: "example/test-repo", Item: 1, ItemKind: "issue", State: "open", Title: "t", Body: "b"}
	a := ItemHash(base)
	empty := base
	empty.Assignees = []string{}
	if ItemHash(empty) != a {
		t.Fatal("unassigned items must keep the pre-assignee hash")
	}
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
	type legacy struct {
		Repo                string   `json:"repo"`
		Item                int      `json:"item"`
		ItemKind            string   `json:"item_kind"`
		State               string   `json:"state"`
		Title               string   `json:"title"`
		Body                string   `json:"body"`
		Labels              []string `json:"labels"`
		Draft               bool     `json:"draft"`
		Merged              bool     `json:"merged"`
		HeadSHA             string   `json:"head_sha"`
		BaseSHA             string   `json:"base_sha"`
		UpdatedAt           string   `json:"updated_at"`
		NonBotCommentCount  int      `json:"non_bot_comment_count"`
		LinkedSameRepoItems []int    `json:"linked_same_repo_items"`
	}
	b, err := json.Marshal(legacy{
		Repo: base.Repo, Item: base.Item, ItemKind: base.ItemKind, State: base.State,
		Title: base.Title, Body: base.Body,
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if a != hex.EncodeToString(sum[:]) {
		t.Fatal("unassigned hash must match snapshots stored before assignees")
	}
}
