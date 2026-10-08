package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sannrox/rusui/internal/guest"
)

func validGuests(registry map[string]guest.Entry) (map[string]guest.Entry, error) {
	builtin := guest.Builtin()
	if registry == nil {
		return builtin, nil
	}
	if len(registry) != len(builtin) {
		return nil, fmt.Errorf("policy: guest registry requires the four supported guests")
	}
	for name, entry := range registry {
		pinned, ok := builtin[name]
		if !ok || entry.Protocol != pinned.Protocol {
			return nil, fmt.Errorf("policy: unsupported guest protocol for %q", name)
		}
		if name == guest.KindClaude && entry.Pin != pinned.Pin {
			return nil, fmt.Errorf("policy: unsupported Claude pin")
		}
		if entry.Pin == "" || len(entry.Argv) == 0 || len(entry.Probe) == 0 {
			return nil, fmt.Errorf("policy: guest %q requires argv, probe, and pin", name)
		}
		for _, argv := range [][]string{entry.Argv, entry.Probe} {
			if strings.TrimSpace(argv[0]) == "" {
				return nil, fmt.Errorf("policy: guest %q executable required", name)
			}
			for _, arg := range argv {
				if strings.ContainsRune(arg, 0) {
					return nil, fmt.Errorf("policy: guest %q argument contains NUL", name)
				}
			}
		}
	}
	return registry, nil
}

func validProjectGuests(slug string, choices *ProjectGuests, registry map[string]guest.Entry) error {
	if choices == nil {
		return nil
	}
	if choices.Default == "" || !slices.Contains(choices.Allowed, choices.Default) {
		return fmt.Errorf("policy: project %s guest default must be allowed", slug)
	}
	seen := map[string]bool{}
	for _, name := range choices.Allowed {
		if _, ok := registry[name]; !ok || seen[name] {
			return fmt.Errorf("policy: project %s guest %q is unknown or repeated", slug, name)
		}
		seen[name] = true
	}
	return nil
}
