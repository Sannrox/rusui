package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	envpkg "github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/server"
	slackpkg "github.com/sannrox/rusui/internal/slack"
	"github.com/sannrox/rusui/internal/store"
)

var (
	Version      = "dev"
	GitCommit    = "unknown"
	BuildTime    = "unknown"
	GitMajor     = ""
	GitMinor     = ""
	PlatformName = ""
	BuildArch    = ""
	BuildOs      = ""
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "run":
			runCLI(os.Args[2:])
			return
		case "acp":
			acpCLI(os.Args[2:])
			return
		case "sessions":
			sessionsCLI(os.Args[2:])
			return
		case "attach":
			attachCLI(os.Args[2:])
			return
		case "prompt":
			promptCLI(os.Args[2:])
			return
		case "logs":
			logsCLI(os.Args[2:])
			return
		case "approve":
			approveCLI(os.Args[2:])
			return
		case "diagnose":
			diagnoseCLI(os.Args[2:])
			return
		case "drain":
			drainCLI(os.Args[2:])
			return
		case "diagnostics":
			diagnosticsCLI(os.Args[2:])
			return
		}
	}
	addr := flag.String("addr", "127.0.0.1:8080", "listen")
	db := flag.String("db", "rusui.db", "sqlite path")
	pol := flag.String("policy", "policy.yaml", "policy file")
	printVersion := flag.Bool("version", false, "print version and exit")
	allowInsecure := flag.Bool("allow-insecure", false, "start without webhook, worker, and Slack secrets")
	flag.Parse()
	if *printVersion {
		fmt.Printf("rusui %s commit=%s time=%s\n", Version, GitCommit, BuildTime)
		return
	}
	missing := missingRequiredSecrets(os.Getenv)
	if len(missing) > 0 && !*allowInsecure {
		log.Fatalf("set %s (or pass -allow-insecure)", strings.Join(missing, ", "))
	}
	if *allowInsecure && len(missing) > 0 {
		log.Printf("WARNING: insecure start; unset %s", strings.Join(missing, ", "))
	}
	st, err := store.Open(*db)
	if err != nil {
		log.Fatal(err)
	}
	p, err := policy.Load(*pol)
	if err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("RUSUI_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	var tokens gh.TokenSource
	if os.Getenv("RUSUI_GITHUB_APP_ID") != "" {
		app, err := gh.LoadAppFromEnv(os.Getenv)
		if err != nil {
			log.Fatal(err)
		}
		tokens = app
	} else if token != "" {
		tokens = gh.StaticToken(token)
	} else {
		log.Fatal("set RUSUI_GITHUB_TOKEN or GITHUB_TOKEN, or GitHub App credentials")
	}
	api := gh.NewAPI(token, gh.ParseHookIDs(os.Getenv("RUSUI_GITHUB_HOOK_IDS")))
	api.Tokens = tokens
	if u := os.Getenv("RUSUI_GITHUB_API"); u != "" {
		api.BaseURL = u
	}
	eng := engine.New(st, p, api, clock.Real{})
	eng.HookIDs = api.HookIDs
	eng.SnapshotRoot = filepath.Join(filepath.Dir(*db), "snapshots")
	tlsCert := os.Getenv("RUSUI_TLS_CERT")
	tlsKey := os.Getenv("RUSUI_TLS_KEY")
	planeCA := os.Getenv("RUSUI_PLANE_CA")
	if rt, err := envpkg.LookRuntime(); err == nil {
		if tlsCert == "" || tlsKey == "" {
			log.Printf("env: container runtime present but RUSUI_TLS_CERT and RUSUI_TLS_KEY unset; container driver disabled")
		} else {
			if d, ok := rt.(envpkg.DockerCLI); ok {
				d.CAFile = planeCA
				rt = d
			}
			eng.Container = envpkg.Container{RT: rt, Image: os.Getenv("RUSUI_GUEST_IMAGE"), CAFile: planeCA}
		}
	}
	if tlsCert != "" && tlsKey != "" && planeCA == "" {
		log.Fatal("set RUSUI_PLANE_CA with RUSUI_TLS_CERT and RUSUI_TLS_KEY")
	}
	getenv := os.Getenv
	eng.Tree = engine.GitFetcher{
		ProxyURL: ops.GitProxyURL(*addr, getenv),
		CAFile:   planeCA,
		GrantFn: func(repo string) (string, error) {
			tok, _, err := eng.IssuePrepareGrant(repo)
			return tok, err
		},
	}
	eng.ReloadPolicy(p)
	poster := &slackpkg.Poster{
		Token:   os.Getenv("RUSUI_SLACK_BOT_TOKEN"),
		Channel: os.Getenv("RUSUI_SLACK_CHANNEL"),
	}
	logNotify := eng.Notify
	eng.Notify = func(msg string) {
		logNotify(msg)
		if err := poster.Exception(msg); err != nil {
			log.Printf("slack exception: %v", err)
		}
	}
	modelKey := os.Getenv("XAI_API_KEY")
	if modelKey == "" {
		modelKey = os.Getenv("RUSUI_XAI_API_KEY")
	}
	srv := &server.Server{
		Eng:               eng,
		WebhookSec:        os.Getenv("RUSUI_WEBHOOK_SECRET"),
		WorkerSec:         os.Getenv("RUSUI_WORKER_SECRET"),
		OperatorTok:       os.Getenv("RUSUI_OPERATOR_TOKEN"),
		SlackSec:          os.Getenv("RUSUI_SLACK_SECRET"),
		SlackUsers:        slackpkg.ParseUsers(os.Getenv("RUSUI_SLACK_USERS")),
		PolicyPath:        *pol,
		Addr:              *addr,
		ModelKey:          modelKey,
		GitHubToken:       token,
		GitHubTokens:      tokens,
		AgentGitHubToken:  os.Getenv("RUSUI_AGENT_GITHUB_TOKEN"),
		NoCoauthorTrailer: os.Getenv("RUSUI_DISABLE_COAUTHOR_TRAILER") == "1",
		NoSessionTrailer:  os.Getenv("RUSUI_DISABLE_SESSION_TRAILER") == "1",
		GuestHTTPSOnly:    tlsCert != "" && tlsKey != "",
		PreviewBase:       os.Getenv("RUSUI_PREVIEW_BASE"),
	}
	if err := eng.Recover(); err != nil {
		log.Printf("recover: %v", err)
	}
	go runScheduler(eng)
	log.Printf("rusui %s listen %s api=%s reconcile=%s catch-up=%s apply=%s", Version, *addr, api.BaseURL, engine.ReconcileEvery, engine.CatchUpEvery, engine.ApplyRetryEvery)
	if srv.GuestHTTPSOnly {
		log.Fatal(http.ListenAndServeTLS(*addr, tlsCert, tlsKey, srv.Handler()))
	}
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}

func runScheduler(eng *engine.Engine) {
	refresh := time.NewTicker(engine.RefreshTick)
	reconcile := time.NewTicker(engine.ReconcileEvery)
	catchup := time.NewTicker(engine.CatchUpEvery)
	apply := time.NewTicker(engine.ApplyRetryEvery)
	sched := time.NewTicker(time.Minute)
	defer refresh.Stop()
	defer reconcile.Stop()
	defer catchup.Stop()
	defer apply.Stop()
	defer sched.Stop()
	for {
		select {
		case <-refresh.C:
			if err := eng.ExpireRefreshOwners(); err != nil {
				eng.Notify("expire owners: " + err.Error())
			}
			if _, err := eng.StepRefresh(); err != nil {
				eng.Notify("step refresh: " + err.Error())
			}
		case <-reconcile.C:
			if err := eng.ReconcileConfigured(); err != nil {
				eng.Notify("reconcile: " + err.Error())
			}
		case <-catchup.C:
			if err := eng.CatchUpConfigured(); err != nil {
				eng.Notify("catch-up: " + err.Error())
			}
		case <-apply.C:
			if err := eng.RetryApplyAttempts(); err != nil {
				eng.Notify("apply retry: " + err.Error())
			}
		case <-sched.C:
			if err := eng.StepSchedules(time.Now()); err != nil {
				eng.Notify("schedules: " + err.Error())
			}
		}
	}
}

func missingRequiredSecrets(getenv func(string) string) []string {
	var miss []string
	for _, k := range []string{"RUSUI_WEBHOOK_SECRET", "RUSUI_WORKER_SECRET", "RUSUI_SLACK_SECRET"} {
		if getenv(k) == "" {
			miss = append(miss, k)
		}
	}
	return miss
}
