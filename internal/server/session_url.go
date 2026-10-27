package server

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// sessionPlaceholder is where a session URL template takes the session id.
const sessionPlaceholder = "{session}"

// sessionMarker stands in for the placeholder while a template is parsed.
const sessionMarker = "rusui-session-marker"

// SessionURL is a validated RUSUI_SESSION_URL template (#497): an absolute
// https URL with exactly one {session} in its path and no userinfo, query or
// fragment, so the only variable part of a published trailer is the id.
type SessionURL struct{ template string }

// ParseSessionURL validates a RUSUI_SESSION_URL template. Empty means unset.
func ParseSessionURL(template string) (*SessionURL, error) {
	if template == "" {
		return nil, nil
	}
	if strings.Count(template, sessionPlaceholder) != 1 {
		return nil, fmt.Errorf("RUSUI_SESSION_URL must hold exactly one %s", sessionPlaceholder)
	}
	// Parsed with a marker in place of the placeholder, so the parsed
	// components, not the template's spelling, say where it sits.
	parsed, err := url.Parse(strings.Replace(template, sessionPlaceholder, sessionMarker, 1))
	if err != nil {
		return nil, fmt.Errorf("RUSUI_SESSION_URL is not a URL: %w", err)
	}
	switch {
	case parsed.Scheme != "https" || parsed.Host == "":
		return nil, fmt.Errorf("RUSUI_SESSION_URL must be an absolute https URL")
	case parsed.User != nil:
		return nil, fmt.Errorf("RUSUI_SESSION_URL must not carry userinfo")
	case parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(template, "?"):
		return nil, fmt.Errorf("RUSUI_SESSION_URL must not carry a query")
	case parsed.Fragment != "" || strings.Contains(template, "#"):
		return nil, fmt.Errorf("RUSUI_SESSION_URL must not carry a fragment")
	case strings.Contains(parsed.Host, sessionMarker) || !strings.Contains(parsed.Path, sessionMarker):
		return nil, fmt.Errorf("RUSUI_SESSION_URL must hold %s in its path", sessionPlaceholder)
	}
	return &SessionURL{template: template}, nil
}

// For is the URL of session id.
func (u *SessionURL) For(id int64) string {
	return strings.Replace(u.template, sessionPlaceholder, strconv.FormatInt(id, 10), 1)
}
