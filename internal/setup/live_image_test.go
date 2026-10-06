package setup

import (
	"os"
	"testing"
	"time"

	guestimage "github.com/sannrox/rusui/build/guest-image"
	"github.com/sannrox/rusui/internal/env"
)

// TestLiveReferenceGuestImage builds the reference image with the real
// container CLI and checks the baked toolchain, headless Chromium, and
// pinned Claude Code CLI (ADR 0050). Needs Docker or Podman.
func TestLiveReferenceGuestImage(t *testing.T) {
	if os.Getenv("RUSUI_LIVE_DOCKER") == "" {
		t.Skip("set RUSUI_LIVE_DOCKER=1 to build and run the reference guest image")
	}
	rt, err := env.LookRuntime()
	if err != nil {
		t.Skip(err.Error())
	}
	d := rt.(env.DockerCLI)
	cache := guestimage.CacheTag()
	if !d.ImageExists(cache) {
		if err := d.BuildImage(cache, guestimage.Dockerfile, false); err != nil {
			t.Fatal(err)
		}
	}
	id, err := d.ImageID(cache)
	if err != nil {
		t.Fatal(err)
	}
	tag := guestimage.ContentTag(id)
	if err := d.TagImage(cache, tag); err != nil {
		t.Fatal(err)
	}
	t.Logf("image %s", tag)
	cid, err := d.CreateAndStart(env.Spec{Name: "live-image-" + time.Now().Format("150405"), Image: tag})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Remove(cid) })
	for _, tool := range [][]string{
		{"gh", "--version"},
		{"git", "--version"},
		{"node", "--version"},
		{"go", "version"},
		{"make", "--version"},
		{"gcc", "--version"},
		{"chromium", "--version"},
	} {
		if err := d.Exec(cid, tool); err != nil {
			t.Fatalf("%v: %v", tool, err)
		}
	}
	if err := d.Exec(cid, []string{"claude", "--version"}); err != nil {
		t.Fatal(err)
	}
	// Isolated module: go test must run with --network none and no module proxy.
	mod := `cd /tmp && mkdir t && cd t && go mod init t && printf '%s\n' 'package t' 'func Add(a, b int) int { return a + b }' > t.go && printf '%s\n' 'package t' 'import "testing"' 'func TestAdd(tt *testing.T) { if Add(1, 2) != 3 { tt.Fatal() } }' > t_test.go && go test`
	if err := d.Exec(cid, []string{"sh", "-c", mod}); err != nil {
		t.Fatalf("go test: %v", err)
	}
	shot := `printf '%s\n' '<html><body>ok</body></html>' > /tmp/p.html && chromium --headless --no-sandbox --disable-gpu --disable-dev-shm-usage --screenshot=/tmp/shot.png --window-size=400,300 file:///tmp/p.html && test -s /tmp/shot.png`
	if err := d.Exec(cid, []string{"sh", "-c", shot}); err != nil {
		t.Fatalf("chromium screenshot: %v", err)
	}
	if err := d.Exec(cid, []string{"curl", "--max-time", "2", "https://proxy.golang.org"}); err == nil {
		t.Fatal("guest reached proxy.golang.org; ADR 0050 adds no destinations")
	}
}
