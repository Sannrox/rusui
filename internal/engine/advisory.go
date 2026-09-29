package engine

import (
	"sort"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
)

// AdvisoryEligible is the first maintenance profile (ADR 0038 D2):
// open items on a bound repository; pull requests target the default
// branch and are not drafts. Closed, merged, and other-base items are
// out of scope, and so is a pull request whose base is unknown. Protected labels still get advisory; they only block
// live classes that stay off.
func AdvisoryEligible(it snapshot.Item) bool {
	switch it.ItemKind {
	case "issue":
		return it.State == "open"
	case "pull":
		// An unknown base or default branch is refused, not assumed.
		return it.State == "open" && !it.Draft && it.BaseRef != "" && it.BaseRef == it.DefaultBranch
	default:
		return false
	}
}

// CatchUpAdvisoryBatch is the GitHub-open slice one catch-up pass may
// queue: eligible items, oldest first per repository, at most
// max_concurrent_leases×2 each (ADR 0038 D5).
func CatchUpAdvisoryBatch(open []snapshot.Item, pol *policy.Effective) []snapshot.Item {
	if pol == nil {
		return nil
	}
	byRepo := map[string][]snapshot.Item{}
	for _, it := range open {
		if !AdvisoryEligible(it) {
			continue
		}
		if _, ok := pol.Repo(it.Repo); !ok {
			continue
		}
		byRepo[it.Repo] = append(byRepo[it.Repo], it)
	}
	var out []snapshot.Item
	repos := make([]string, 0, len(byRepo))
	for repo := range byRepo {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		items := byRepo[repo]
		sort.Slice(items, func(i, j int) bool {
			if items[i].CreatedAt != items[j].CreatedAt {
				return items[i].CreatedAt < items[j].CreatedAt
			}
			return items[i].Item < items[j].Item
		})
		rp, _ := pol.Repo(repo)
		proj := pol.Projects[rp.Project]
		cap := proj.MaxConcurrentLeases() * 2
		if len(items) > cap {
			items = items[:cap]
		}
		out = append(out, items...)
	}
	return out
}
