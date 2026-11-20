package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func shareCLI(args []string) {
	os.Exit(shareMain(args, os.Stdout, os.Stderr))
}

// shareMain mints, lists, or revokes an operator-invited share grant.
// Mint prints the preview-origin URL. The operator token stays on the plane.
func shareMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	fs.SetOutput(errw)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	service := fs.String("service", "", "declared service name to share")
	ttl := fs.String("ttl", "1h", "share lifetime: 1h, 4h, or 24h")
	list := fs.Bool("list", false, "list live share grants")
	revoke := fs.String("revoke", "", "token hash of a live share grant to revoke")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	name := strings.TrimSpace(*service)
	hash := strings.TrimSpace(*revoke)
	switch {
	case *list:
		if fs.NArg() != 1 || name != "" || hash != "" {
			_, _ = fmt.Fprintln(errw, "usage: rusui share -list [-url URL] [-token TOKEN] SESSION_ID")
			return 2
		}
	case hash != "":
		if fs.NArg() != 1 || name != "" {
			_, _ = fmt.Fprintln(errw, "usage: rusui share -revoke HASH [-url URL] [-token TOKEN] SESSION_ID")
			return 2
		}
	default:
		if fs.NArg() != 1 || name == "" {
			_, _ = fmt.Fprintln(errw, "usage: rusui share [-url URL] [-token TOKEN] -service NAME [-ttl 1h|4h|24h] SESSION_ID")
			return 2
		}
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(errw, "share: session id")
		return 2
	}
	client, err := planeHTTP(*base)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	var req *http.Request
	switch {
	case *list:
		req, err = newShareListRequest(*base, *token, fs.Arg(0))
	case hash != "":
		req, err = newShareRevokeRequest(*base, *token, fs.Arg(0), hash)
	default:
		req, err = newShareMintRequest(*base, *token, fs.Arg(0), name, *ttl)
	}
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(errw, "share: %s %s\n", res.Status, strings.TrimSpace(string(body)))
		return 1
	}
	if *list {
		_, _ = fmt.Fprint(out, string(body))
		if len(body) > 0 && body[len(body)-1] != '\n' {
			_, _ = fmt.Fprintln(out)
		}
		return 0
	}
	if hash != "" {
		return 0
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil || strings.TrimSpace(got.URL) == "" {
		_, _ = fmt.Fprintln(errw, "share: missing url")
		return 1
	}
	_, _ = fmt.Fprintln(out, got.URL)
	return 0
}

func newShareMintRequest(planeURL, token, id, service, ttl string) (*http.Request, error) {
	payload := map[string]any{"service": service}
	if ttl != "" {
		payload["ttl"] = ttl
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/share", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func newShareListRequest(planeURL, token, id string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/share", nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func newShareRevokeRequest(planeURL, token, id, hash string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/share/"+hash, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}
