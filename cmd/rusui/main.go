package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/sannrox/rusui/internal/clock"
	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/gh"
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
	addr := flag.String("addr", "127.0.0.1:8080", "listen")
	db := flag.String("db", "rusui.db", "sqlite path")
	pol := flag.String("policy", "policy.yaml", "policy file")
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Printf("rusui %s commit=%s time=%s\n", Version, GitCommit, BuildTime)
		return
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
	if token == "" {
		log.Fatal("set RUSUI_GITHUB_TOKEN or GITHUB_TOKEN (read-only)")
	}
	api := gh.NewAPI(token, gh.ParseHookIDs(os.Getenv("RUSUI_GITHUB_HOOK_IDS")))
	if u := os.Getenv("RUSUI_GITHUB_API"); u != "" {
		api.BaseURL = u
	}
	eng := engine.New(st, p, api, clock.Real{})
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
	srv := &server.Server{
		Eng:        eng,
		WebhookSec: os.Getenv("RUSUI_WEBHOOK_SECRET"),
		WorkerSec:  os.Getenv("RUSUI_WORKER_SECRET"),
		SlackSec:   os.Getenv("RUSUI_SLACK_SECRET"),
		SlackUsers: slackpkg.ParseUsers(os.Getenv("RUSUI_SLACK_USERS")),
		PolicyPath: *pol,
	}
	go func() {
		t := time.NewTicker(time.Second)
		for range t.C {
			_ = eng.ExpireRefreshOwners()
			_, _ = eng.StepRefresh()
		}
	}()
	log.Printf("rusui %s listen %s api=%s", Version, *addr, api.BaseURL)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
