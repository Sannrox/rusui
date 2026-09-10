package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

type Item struct {
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
	DefaultBranch       string   `json:"default_branch"`
	MainSHA             string   `json:"main_sha"`
	CreatedAt           string   `json:"created_at"`
	LastNonBotCommentAt string   `json:"last_non_bot_comment_at"`
	MergedIntoDefault   bool     `json:"merged_into_default"`
	MergeCommitSHA      string   `json:"merge_commit_sha"`
	BaseRef             string   `json:"base_ref"`
}

func ItemHash(it Item) string {
	labels := append([]string(nil), it.Labels...)
	sort.Strings(labels)
	linked := append([]int(nil), it.LinkedSameRepoItems...)
	sort.Ints(linked)
	type hashable struct {
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
	h := hashable{
		Repo: it.Repo, Item: it.Item, ItemKind: it.ItemKind, State: it.State,
		Title: it.Title, Body: it.Body, Labels: labels, Draft: it.Draft, Merged: it.Merged,
		HeadSHA: it.HeadSHA, BaseSHA: it.BaseSHA, UpdatedAt: it.UpdatedAt,
		NonBotCommentCount: it.NonBotCommentCount, LinkedSameRepoItems: linked,
	}
	if it.ItemKind != "pull" {
		h.HeadSHA = ""
		h.BaseSHA = ""
	}
	b, _ := json.Marshal(h)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func SnapshotHash(it Item) string {
	return ItemHash(it)
}
