package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/sannrox/rusui/internal/engine"
)

const (
	// modelUsageJSONBytes bounds a buffered JSON response; past it the
	// response passes through and its usage is unknown.
	modelUsageJSONBytes = 16 << 20
	// modelUsageLineBytes bounds one event-stream line.
	modelUsageLineBytes = 1 << 20
)

// meteredModelPath reports whether a response on path bills tokens: message,
// chat completion, and response calls. Token counting and model listings
// do not.
func meteredModelPath(path string) bool {
	return strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/responses")
}

// meterModelResponse reports res's token usage through report. A response
// that bills nothing reports zero now; a metered one gets a body that passes
// every byte through unchanged and reports once the body ends or closes.
func meterModelResponse(res *http.Response, path string, report func(engine.ModelUsage, bool)) {
	if res.StatusCode/100 != 2 || !meteredModelPath(path) {
		report(engine.ModelUsage{}, true)
		return
	}
	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	enc := res.Header.Get("Content-Encoding")
	r := &usageReader{body: res.Body, report: report, stream: mt == "text/event-stream", length: res.ContentLength}
	// A compressed body or one in another shape cannot be read here.
	r.failed = enc != "" && enc != "identity" || !r.stream && mt != "application/json"
	res.Body = r
}

// usageReader passes a model response body through and reads the usage it
// reports. An event stream is read line by line, so an event split across
// reads still parses; a JSON body is buffered up to a bound. The reverse
// proxy reads and closes it from one goroutine.
type usageReader struct {
	body   io.ReadCloser
	report func(engine.ModelUsage, bool)
	stream bool
	length int64 // a JSON body's declared length, or -1
	failed bool
	done   bool
	buf    []byte
	usage  engine.ModelUsage
	seen   bool // some usage was reported
}

func (r *usageReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if !r.done && !r.failed && n > 0 {
		r.feed(p[:n])
	}
	switch {
	case err == io.EOF:
		// A stream is complete only at its final event.
		r.finish(!r.stream)
	case err != nil:
		r.finish(false)
	}
	return n, err
}

// Close before the usage was complete leaves it unknown.
func (r *usageReader) Close() error {
	r.finish(false)
	return r.body.Close()
}

func (r *usageReader) feed(b []byte) {
	if !r.stream {
		if len(r.buf)+len(b) > modelUsageJSONBytes {
			r.failed, r.buf = true, nil
			return
		}
		r.buf = append(r.buf, b...)
		// Report as the last byte arrives, not on a later read, so the
		// turn cannot finish while the usage is still open.
		if int64(len(r.buf)) == r.length {
			r.finish(true)
		}
		return
	}
	for len(b) > 0 && !r.done {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			r.buf = append(r.buf, b...)
			break
		}
		r.buf = append(r.buf, b[:i]...)
		if r.line(r.buf) {
			r.finish(true)
		}
		r.buf, b = r.buf[:0], b[i+1:]
	}
	if len(r.buf) > modelUsageLineBytes {
		r.failed, r.buf = true, nil
	}
}

// line reads one event-stream line and reports whether it ends the stream.
// Only data lines that can carry usage or end the stream are decoded.
func (r *usageReader) line(l []byte) bool {
	data, ok := bytes.CutPrefix(bytes.TrimSuffix(l, []byte("\r")), []byte("data:"))
	if !ok {
		return false
	}
	data = bytes.TrimPrefix(data, []byte(" "))
	if string(data) == "[DONE]" {
		return true
	}
	if !bytes.Contains(data, []byte(`"usage"`)) && !bytes.Contains(data, []byte(`"message_stop"`)) {
		return false
	}
	return r.event(data)
}

// usageEvent covers the response and event shapes that carry usage.
type usageEvent struct {
	Type    string    `json:"type"`
	Usage   *apiUsage `json:"usage"`
	Message struct {
		Usage *apiUsage `json:"usage"`
	} `json:"message"`
	Response struct {
		Usage *apiUsage `json:"usage"`
	} `json:"response"`
}

// apiUsage holds both dialects. In one, input_tokens excludes cache reads
// and writes; in the other, prompt_tokens (or input_tokens with
// input_tokens_details) includes the cached part.
type apiUsage struct {
	InputTokens      int64       `json:"input_tokens"`
	CacheWrite       int64       `json:"cache_creation_input_tokens"`
	CacheRead        int64       `json:"cache_read_input_tokens"`
	OutputTokens     int64       `json:"output_tokens"`
	PromptTokens     int64       `json:"prompt_tokens"`
	CompletionTokens int64       `json:"completion_tokens"`
	PromptDetails    *cachedPart `json:"prompt_tokens_details"`
	InputDetails     *cachedPart `json:"input_tokens_details"`
}

type cachedPart struct {
	CachedTokens int64 `json:"cached_tokens"`
}

// event merges the usage in one decoded event or body and reports whether
// it is a stream's final event.
func (r *usageReader) event(data []byte) bool {
	var ev usageEvent
	if json.Unmarshal(data, &ev) != nil {
		return false
	}
	for _, u := range []*apiUsage{ev.Usage, ev.Message.Usage, ev.Response.Usage} {
		if u != nil {
			r.merge(*u)
		}
	}
	return ev.Type == "message_stop" || ev.Type == "response.completed"
}

// merge overwrites each count the event reports above zero. A stream's
// first event carries a placeholder output count that a later one replaces.
func (r *usageReader) merge(u apiUsage) {
	r.seen = true
	in, cacheRead := u.InputTokens, u.CacheRead
	if d := u.InputDetails; d != nil {
		in -= d.CachedTokens
		cacheRead = d.CachedTokens
	}
	if u.PromptTokens > 0 {
		in = u.PromptTokens
		if d := u.PromptDetails; d != nil {
			in -= d.CachedTokens
			cacheRead = d.CachedTokens
		}
	}
	set := func(dst *int64, v int64) {
		if v > 0 {
			*dst = v
		}
	}
	set(&r.usage.Input, in)
	set(&r.usage.CacheWrite, u.CacheWrite)
	set(&r.usage.CacheRead, cacheRead)
	set(&r.usage.Output, u.OutputTokens)
	set(&r.usage.Output, u.CompletionTokens)
}

// finish reports once; complete is false when the body ended before all
// of its usage was seen.
func (r *usageReader) finish(complete bool) {
	if r.done {
		return
	}
	r.done = true
	complete = complete && !r.failed
	if complete && !r.stream {
		r.event(r.buf)
	}
	r.buf = nil
	r.report(r.usage, complete && r.seen)
}
