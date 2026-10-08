package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	envpkg "github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
)

func diagnoseCLI(args []string) {
	os.Exit(diagnoseMain(args, os.Stdout))
}

func diagnoseMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pol := fs.String("policy", "policy.yaml", "policy file")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address to check")
	url := fs.String("url", "", "plane base URL (GET /healthz); defaults to https when TLS env is set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	getenv := os.Getenv
	planeURL := *url
	if planeURL == "" && ops.TLSEnabled(getenv) {
		planeURL = ops.PlaneBaseURL(*addr, getenv)
	}
	rep := diagnoseWithModel(ops.Options{
		PolicyPath: *pol,
		Addr:       *addr,
		PlaneURL:   planeURL,
		CAFile:     getenv("RUSUI_PLANE_CA"),
		Env:        getenv,
	}, getenv)
	if rt, err := envpkg.LookRuntime(); err == nil && planeReady(rep) {
		addCheck(&rep, ops.CheckGuestLink(rt, getenv, planeURL))
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !rep.Ready {
		return 1
	}
	return 0
}

// planeReady is whether the plane answered; the guest link check needs it.
func planeReady(rep ops.Report) bool {
	for _, c := range rep.Checks {
		if c.Name == "plane" {
			return c.Status == ops.StatusReady && c.Blocker
		}
	}
	return false
}

func addCheck(rep *ops.Report, check ops.Check) {
	rep.Checks = append(rep.Checks, check)
	if check.Blocker && check.Status != ops.StatusReady {
		rep.Ready = false
	}
}

func diagnoseWithModel(opt ops.Options, getenv func(string) string) ops.Report {
	rep := ops.Diagnose(opt)
	look := opt.LookRuntime
	if look == nil {
		look = envpkg.LookRuntime
	}
	if rt, err := look(); err == nil {
		for _, check := range ops.CheckGuestBinaries(rt, opt.PolicyPath, getenv) {
			addCheck(&rep, check)
		}
	}
	modelEnv := getenv
	if pol, err := policy.Load(opt.PolicyPath); err == nil {
		modelEnv = server.PolicyModelEnv(getenv, pol)
	}
	for _, check := range []ops.Check{checkModelUpstream(modelEnv), checkModelGuest(modelEnv)} {
		rep.Checks = append(rep.Checks, check)
		if check.Blocker && check.Status != ops.StatusReady {
			rep.Ready = false
		}
	}
	return rep
}

func checkModelUpstream(getenv func(string) string) ops.Check {
	check := ops.Check{Name: "model_upstream", Blocker: true}
	if getenv == nil {
		getenv = os.Getenv
	}
	model, err := server.ModelConfigFromEnv(getenv)
	provider := modelProviderName(model.Provider)
	if err != nil {
		check.Status = ops.StatusMisconfigured
		check.Detail = provider + " upstream configuration is invalid"
		return check
	}

	origin := model.Origin
	if origin == nil && model.Key == "" {
		check.Status = ops.StatusMisconfigured
		check.Detail = provider + " upstream needs a model key or RUSUI_MODEL_UPSTREAM"
		return check
	}
	if origin == nil {
		// Keep these provider defaults aligned with the plane model proxy.
		defaultOrigin := map[string]string{
			server.ProviderXAI:       "https://api.x.ai",
			server.ProviderAnthropic: "https://api.anthropic.com",
			server.ProviderOpenAI:    "https://api.openai.com",
		}[model.Provider]
		if defaultOrigin == "" {
			check.Status = ops.StatusMisconfigured
			check.Detail = "model provider has no upstream"
			return check
		}
		origin, err = url.Parse(defaultOrigin)
		if err != nil {
			check.Status = ops.StatusMisconfigured
			check.Detail = "model provider has no upstream"
			return check
		}
	}

	endpoint := *origin
	endpoint.Path = path.Join(endpoint.Path, "v1", "models")
	endpoint.RawPath = ""
	endpoint.Fragment = ""
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		check.Status = ops.StatusMisconfigured
		check.Detail = provider + " upstream URL is invalid"
		return check
	}
	// Anthropic gateways still require the version header when they auth locally.
	if model.Provider == server.ProviderAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		if model.Key != "" {
			req.Header.Set("x-api-key", model.Key)
		}
	} else if model.Key != "" {
		req.Header.Set("Authorization", "Bearer "+model.Key)
	}

	// Never forward provider credentials to a redirect target.
	probeClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := probeClient.Do(req)
	if err != nil {
		check.Status = ops.StatusUnavailable
		check.Detail = provider + " upstream is unreachable"
		return check
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusMultipleChoices {
		check.Status = ops.StatusReady
		check.Detail = provider + " upstream accepted the no-op model list request"
		return check
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		check.Status = ops.StatusMisconfigured
		check.Detail = fmt.Sprintf("%s upstream rejected authentication (HTTP %d)", provider, res.StatusCode)
		return check
	}
	if res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusBadRequest {
		check.Status = ops.StatusMisconfigured
		check.Detail = fmt.Sprintf("%s upstream redirected the model list request (HTTP %d)", provider, res.StatusCode)
		return check
	}
	if res.StatusCode < http.StatusInternalServerError {
		check.Status = ops.StatusMisconfigured
		check.Detail = fmt.Sprintf("%s upstream rejected the model list request (HTTP %d)", provider, res.StatusCode)
		return check
	}
	check.Status = ops.StatusUnavailable
	check.Detail = fmt.Sprintf("%s upstream returned HTTP %d", provider, res.StatusCode)
	return check
}

// checkModelGuest asks the upstream whether the named guest model can
// accept one bounded model request. A successful model list is not
// that proof: the catalog can be ready while the model the guest will
// send is in quota cooldown.
func checkModelGuest(getenv func(string) string) ops.Check {
	check := ops.Check{Name: "model_guest", Blocker: true}
	if getenv == nil {
		getenv = os.Getenv
	}
	model, err := server.ModelConfigFromEnv(getenv)
	provider := modelProviderName(model.Provider)
	if err != nil {
		check.Status = ops.StatusMisconfigured
		if errors.Is(err, server.ErrInvalidGuestModel) {
			check.Detail = "RUSUI_GUEST_MODEL is invalid"
			return check
		}
		check.Detail = provider + " upstream configuration is invalid"
		return check
	}
	if model.Guest != acp.GuestClaude && model.Guest != acp.GuestShikigami {
		if model.GuestModel != "" {
			check.Status = ops.StatusMisconfigured
			check.Detail = "RUSUI_GUEST_MODEL is sent by the Claude guest; leave it unset for Grok and Codex"
			return check
		}
		check.Status = ops.StatusReady
		check.Detail = "guest does not take a named model"
		return check
	}
	if model.Guest == acp.GuestShikigami && (model.GuestModel == "" || model.GuestModel == "auto") {
		model.GuestModel = guest.DefaultShikigamiModel
	}
	if model.GuestModel == "" {
		check.Status = ops.StatusMisconfigured
		check.Detail = "set RUSUI_GUEST_MODEL; a model list is not proof the Claude guest can prompt"
		return check
	}

	origin, detail, ok := modelProbeOrigin(model)
	if !ok {
		check.Status = ops.StatusMisconfigured
		check.Detail = detail
		return check
	}
	requestBody := map[string]any{
		"model":      model.GuestModel,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
	}
	endpointPath := "messages"
	if model.Guest == acp.GuestShikigami {
		endpointPath = "chat/completions"
		delete(requestBody, "max_tokens")
		requestBody["max_completion_tokens"] = 1
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		check.Status = ops.StatusMisconfigured
		check.Detail = "guest model probe could not be built"
		return check
	}
	endpoint := *origin
	endpoint.Path = path.Join(endpoint.Path, "v1", endpointPath)
	endpoint.RawPath = ""
	endpoint.Fragment = ""
	req, err := http.NewRequest(http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		check.Status = ops.StatusMisconfigured
		check.Detail = provider + " upstream URL is invalid"
		return check
	}
	req.Header.Set("Content-Type", "application/json")
	if model.Guest == acp.GuestClaude {
		req.Header.Set("anthropic-version", "2023-06-01")
		if model.Key != "" {
			req.Header.Set("x-api-key", model.Key)
		}
	} else if model.Key != "" {
		req.Header.Set("Authorization", "Bearer "+model.Key)
	}
	res, err := modelProbeClient().Do(req)
	if err != nil {
		check.Status = ops.StatusUnavailable
		check.Detail = provider + " guest model is unreachable"
		return check
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode >= http.StatusOK && res.StatusCode < http.StatusMultipleChoices:
		check.Status = ops.StatusReady
		check.Detail = provider + " guest model " + model.GuestModel + " accepted a bounded model request"
	case res.StatusCode == http.StatusTooManyRequests:
		check.Status = ops.StatusUnavailable
		check.Detail = fmt.Sprintf("%s guest model %s is unavailable (HTTP 429); a model list is not proof the guest can prompt", provider, model.GuestModel)
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		check.Status = ops.StatusMisconfigured
		check.Detail = fmt.Sprintf("%s guest model rejected authentication (HTTP %d)", provider, res.StatusCode)
	case res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusBadRequest:
		check.Status = ops.StatusMisconfigured
		check.Detail = fmt.Sprintf("%s guest model probe was redirected (HTTP %d)", provider, res.StatusCode)
	case res.StatusCode < http.StatusInternalServerError:
		check.Status = ops.StatusMisconfigured
		check.Detail = fmt.Sprintf("%s guest model rejected the bounded model request (HTTP %d)", provider, res.StatusCode)
	default:
		check.Status = ops.StatusUnavailable
		check.Detail = fmt.Sprintf("%s guest model returned HTTP %d", provider, res.StatusCode)
	}
	return check
}

func modelProbeOrigin(model server.ModelConfig) (*url.URL, string, bool) {
	provider := modelProviderName(model.Provider)
	if model.Origin == nil && model.Key == "" {
		return nil, provider + " upstream needs a model key or RUSUI_MODEL_UPSTREAM", false
	}
	if model.Origin != nil {
		return model.Origin, "", true
	}
	defaultOrigin := map[string]string{
		server.ProviderXAI:       "https://api.x.ai",
		server.ProviderAnthropic: "https://api.anthropic.com",
		server.ProviderOpenAI:    "https://api.openai.com",
	}[model.Provider]
	if defaultOrigin == "" {
		return nil, "model provider has no upstream", false
	}
	origin, err := url.Parse(defaultOrigin)
	if err != nil {
		return nil, "model provider has no upstream", false
	}
	return origin, "", true
}

func modelProbeClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func modelProviderName(provider string) string {
	switch provider {
	case server.ProviderXAI:
		return "xAI"
	case server.ProviderAnthropic:
		return "Anthropic"
	case server.ProviderOpenAI:
		return "OpenAI"
	default:
		return "model provider"
	}
}
