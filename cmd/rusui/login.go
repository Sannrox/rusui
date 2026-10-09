package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/sannrox/rusui/internal/oidc"
)

func loginMain(args []string, out, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	port := fs.Int("callback-port", 9876, "registered loopback callback port")
	noBrowser := fs.Bool("no-browser", false, "print authorization URL without opening a browser")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *port < 1 || *port > 65535 {
		_, _ = fmt.Fprintln(stderr, "login: use a registered callback port (1-65535)")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	plane, err := planeHTTP(*server)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "login:", err)
		return 1
	}
	bounded := *plane
	bounded.Timeout = 15 * time.Second
	bounded.CheckRedirect = oidc.HTTPClient().CheckRedirect
	err = oidc.Login(ctx, *server, *port, &bounded, func(authURL string) error {
		_, _ = fmt.Fprintln(out, authURL)
		if *noBrowser {
			return nil
		}
		var command string
		var arguments []string
		switch runtime.GOOS {
		case "darwin":
			command = "open"
			arguments = []string{authURL}
		case "windows":
			command = "rundll32"
			arguments = []string{"url.dll,FileProtocolHandler", authURL}
		default:
			command = "xdg-open"
			arguments = []string{authURL}
		}
		if err := exec.CommandContext(ctx, command, arguments...).Run(); err != nil {
			_, _ = fmt.Fprintln(stderr, "Could not open browser. Open the URL above to continue.")
		}
		return nil
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "login:", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, "Signed in.")
	return 0
}

func logoutMain(args []string, out, stderr io.Writer) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	server := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return 2
	}
	if err := oidc.Logout(*server); err != nil {
		_, _ = fmt.Fprintln(stderr, "logout:", err)
		return 1
	}
	_, _ = fmt.Fprintln(out, "Local login removed.")
	return 0
}

// resolveOperatorToken runs after flag parsing. An explicit flag, including an
// empty one, never falls through to a saved credential. Mixed commands retain
// their worker default when no operator environment token or saved login exists.
func resolveOperatorToken(fs *flag.FlagSet, server string, token *string) error {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "token" {
			explicit = true
		}
	})
	if explicit {
		return nil
	}
	if operator := os.Getenv("RUSUI_OPERATOR_TOKEN"); operator != "" {
		*token = operator
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	saved, err := oidc.SavedToken(ctx, server)
	if err != nil {
		return err
	}
	if saved != "" {
		*token = saved
	}
	return nil
}
