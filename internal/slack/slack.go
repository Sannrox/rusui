package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxSkew = 5 * time.Minute

func ParseUsers(s string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out[p] = true
		}
	}
	return out
}

func Verify(secret, timestamp, signature string, body []byte, now time.Time) error {
	if secret == "" {
		return fmt.Errorf("slack: missing signing secret")
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("slack: bad timestamp")
	}
	skew := now.Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(maxSkew.Seconds()) {
		return fmt.Errorf("slack: timestamp skew")
	}
	base := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(base))
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return fmt.Errorf("slack: bad signature")
	}
	return nil
}

func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + timestamp + ":" + string(body)))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

type Command struct {
	UserID  string
	Command string
	Text    string
	Arg     string
	RawText string
}

func ParseForm(body []byte) Command {
	v, _ := url.ParseQuery(string(body))
	text := strings.TrimSpace(v.Get("text"))
	cmdName := strings.TrimSpace(v.Get("command"))
	text = strings.TrimSpace(strings.TrimPrefix(text, cmdName))
	text = strings.TrimSpace(strings.TrimPrefix(text, "/rusui"))
	parts := strings.Fields(text)
	c := Command{UserID: v.Get("user_id"), RawText: text}
	if len(parts) > 0 {
		c.Command = strings.ToLower(parts[0])
		if len(parts) > 1 {
			c.Arg = parts[1]
		}
	}
	c.Text = text
	return c
}

func Challenge(body []byte) (string, bool) {
	var p struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(body, &p) != nil {
		return "", false
	}
	if p.Type != "url_verification" || p.Challenge == "" {
		return "", false
	}
	return p.Challenge, true
}

func Reply(text string) []byte {
	b, _ := json.Marshal(map[string]string{
		"response_type": "ephemeral",
		"text":          text,
	})
	return b
}

func SplitItem(s string) (repo string, item int) {
	i := strings.LastIndex(s, "#")
	if i < 0 {
		return s, 0
	}
	n, _ := strconv.Atoi(s[i+1:])
	return s[:i], n
}

type Poster struct {
	Token   string
	Channel string
	API     string
	HTTP    *http.Client
}

func (p *Poster) Exception(msg string) error {
	if p == nil || p.Token == "" || p.Channel == "" {
		return nil
	}
	api := p.API
	if api == "" {
		api = "https://slack.com/api/chat.postMessage"
	}
	form := url.Values{
		"channel": {p.Channel},
		"text":    {msg},
	}
	req, err := http.NewRequest(http.MethodPost, api, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cli := p.HTTP
	if cli == nil {
		cli = http.DefaultClient
	}
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("slack post: %s", res.Status)
	}
	return nil
}
