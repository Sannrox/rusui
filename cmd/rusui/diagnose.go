package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/sannrox/rusui/internal/ops"
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

func diagnoseWithModel(opt ops.Options, getenv func(string) string) ops.Report {
	rep := ops.Diagnose(opt)
	check := checkModelUpstream(getenv)
	rep.Checks = append(rep.Checks, check)
	if check.Blocker && check.Status != ops.StatusReady {
		rep.Ready = false
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
	if model.Key != "" {
		if model.Provider == server.ProviderAnthropic {
			req.Header.Set("x-api-key", model.Key)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+model.Key)
		}
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

func modelProviderName(provider string) string {
	switch provider {
	case server.ProviderXAI:
		return "xAI"
	case server.ProviderAnthropic:
		return "Anthropic"
	default:
		return "model provider"
	}
}
