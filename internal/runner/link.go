package runner

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/sannrox/rusui/internal/env"
)

// startTurnLink opens the guest link of a container turn (ADR 0047): the
// guest has no network, and its plane URLs reach the plane listener the
// runner itself uses. An implement turn that talks to GitHub itself (ADR
// 0015) also reaches GitHub, and a custom egress class adds its listed
// destinations (#681), and class full adds the egress proxy (#751). The returned context ends when the link
// does, so a lost link fails the turn instead of waiting for its deadline.
func startTurnLink(ctx context.Context, c *Client, a *Assignment) (context.Context, func(), error) {
	noop := func() {}
	if a.Driver != "container" || a.Handle == "" {
		return ctx, noop, nil
	}
	x, ok := c.Exec.(env.StdioExecutor)
	if !ok {
		return ctx, noop, fmt.Errorf("guest link: container exec required")
	}
	planeDial, err := hostPort(c.Base)
	if err != nil {
		return ctx, noop, fmt.Errorf("guest link: plane url: %w", err)
	}
	guestPort := 443
	if a.ModelBaseURL != "" {
		if guestPort, err = urlPort(a.ModelBaseURL); err != nil {
			return ctx, noop, fmt.Errorf("guest link: model url: %w", err)
		}
	}
	egress, err := env.EgressLinkTargets(a.EgressAllow)
	if err != nil {
		return ctx, noop, fmt.Errorf("guest link: %w", err)
	}
	targets := append(env.PlaneLinkTargets(planeDial, guestPort, a.GitHubToken != ""), egress...)
	stopProxy := noop
	if a.proxiedEgress() {
		proxy, err := env.StartEgressProxy()
		if err != nil {
			return ctx, noop, fmt.Errorf("guest link: %w", err)
		}
		stopProxy = proxy.Close
		targets = append(targets, env.LinkTarget{GuestIP: env.GuestProxyIP, Port: env.GuestProxyPort, Dial: proxy.Addr()})
	}
	link, err := env.StartLink(x, a.Handle, targets)
	if err != nil {
		stopProxy()
		return ctx, noop, err
	}
	linkCtx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-link.Done():
			cancel(fmt.Errorf("guest link to the plane ended"))
		case <-linkCtx.Done():
		}
	}()
	return linkCtx, func() { cancel(nil); link.Close(); stopProxy() }, nil
}

// proxiedEgress reports whether the turn's guest link serves the egress
// proxy: class full, container guests only (ADR 0093).
func (a *Assignment) proxiedEgress() bool {
	return a.Egress == "full" && a.Driver == "container" && a.Handle != ""
}

func hostPort(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid url")
	}
	port, err := urlPort(raw)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(u.Hostname(), strconv.Itoa(port)), nil
}

func urlPort(raw string) (int, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return 0, fmt.Errorf("invalid url")
	}
	if p := u.Port(); p != "" {
		return strconv.Atoi(p)
	}
	if u.Scheme == "http" {
		return 80, nil
	}
	return 443, nil
}
