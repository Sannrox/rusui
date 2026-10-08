package runner

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Shikigami's HTTP adapter reads its URL and model from TOML, not the
// OpenAI environment variables. Keep that configuration in the guest home.
func prepareShikigamiConfig(x StdioExec, a *Assignment, argv []string, home string) ([]string, error) {
	origin, err := url.Parse(a.ModelBaseURL)
	if err != nil || origin.Hostname() == "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, fmt.Errorf("shikigami: plane model URL required")
	}
	base := strings.TrimRight(a.ModelBaseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	model := a.GuestModel
	if model == "" {
		model = "auto"
	}
	config := fmt.Sprintf(`version = 1
[governance]
adapter = "local"
fail_closed = false
[model]
adapter = "http"
base_url = %s
model = %s
api_key_env = "OPENAI_API_KEY"
[network]
egress = "allowlist"
allow_hosts = [%s]
[tools]
mode = "workspace_exec"
`, strconv.Quote(base), strconv.Quote(model), strconv.Quote(origin.Hostname()))
	path := filepath.Join(home, "guest.toml")
	if a.Driver == "container" && a.Handle != "" {
		out, err := guestOutput(x, a, "", "sh", "-c", `umask 077; printf '%s' "$2" | base64 -d > "$1" && printf configured`, "rusui-shikigami-config", path, base64.StdEncoding.EncodeToString([]byte(config)))
		if err != nil || string(out) != "configured" {
			return nil, fmt.Errorf("shikigami: configuration write unconfirmed")
		}
	} else if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return nil, err
	}
	argv = append([]string(nil), argv...)
	for i, arg := range argv {
		if arg == "--config" && i+1 < len(argv) {
			argv[i+1] = path
			return argv, nil
		}
		if strings.HasPrefix(arg, "--config=") {
			argv[i] = "--config=" + path
			return argv, nil
		}
	}
	return append([]string{argv[0], "--config", path}, argv[1:]...), nil
}
