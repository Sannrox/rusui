package ops

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
)

// CheckGuestBinaries probes each configured project default in a throwaway
// container. It is a diagnose check, never a readiness request or a turn.
func CheckGuestBinaries(rt env.Runtime, policyPath string, getenv func(string) string) []Check {
	pol, err := policy.Load(policyPath)
	if err != nil {
		return nil
	}
	names := make(map[string]bool)
	for _, project := range pol.Projects {
		name := getenv("RUSUI_GUEST")
		if name == "" {
			name = guest.KindGrok
		}
		if project.Guests != nil {
			name = project.Guests.Default
		}
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	slices.Sort(ordered)
	checks := make([]Check, 0, len(ordered))
	for _, name := range ordered {
		entry := pol.Guests[name]
		image := entry.Image
		if image == "" {
			image = getenv("RUSUI_GUEST_IMAGE")
		}
		checks = append(checks, checkGuestBinary(rt, name, entry, image))
	}
	return checks
}

func checkGuestBinary(rt env.Runtime, name string, entry guest.Entry, image string) Check {
	c := Check{Name: "guest_" + name, Blocker: true, Status: StatusUnavailable}
	output, ok := rt.(outputRuntime)
	if !ok || image == "" || len(entry.Probe) == 0 {
		c.Detail = "guest image or configured probe unavailable"
		return c
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		c.Detail = "cannot reserve guest probe"
		return c
	}
	container := env.Container{RT: rt, Image: image}
	handle, err := container.Create("diagnose-guest-" + hex.EncodeToString(suffix[:]))
	if err != nil {
		c.Detail = "cannot start guest probe"
		return c
	}
	defer func() { _ = container.Destroy(handle) }()
	out, truncated, err := output.ExecOutput(handle, entry.Probe)
	if err != nil || truncated || strings.TrimSpace(string(out)) == "" {
		c.Detail = "configured guest probe failed"
		return c
	}
	c.Status = StatusReady
	c.Detail = "guest answered its configured binary probe"
	return c
}
