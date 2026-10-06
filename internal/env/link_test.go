package env

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
)

// hostNode runs the guest side on this host, standing in for `docker exec
// -i`; the guest's loopback is the host's.
type hostNode struct{}

func (hostNode) ExecStdio(_ string, argv, _ []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return in, out, func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }, nil
}

func needNode(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed; the guest side runs on node")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// serveBlob answers every connection with blob, then reads until the
// client half-closes and replies with the byte count it received.
func serveBlob(t *testing.T, blob []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				_, _ = c.Write(blob)
				n, _ := io.Copy(io.Discard, c)
				_, _ = fmt.Fprintf(c, "|%d", n)
			}()
		}
	}()
	return ln.Addr().String()
}

// roundTrip sends payload, half-closes, and returns everything read.
func roundTrip(t *testing.T, addr string, payload []byte) []byte {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Error(err)
		return nil
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	go func() {
		_, _ = c.Write(payload)
		_ = c.(*net.TCPConn).CloseWrite()
	}()
	got, err := io.ReadAll(c)
	if err != nil {
		t.Error(err)
	}
	return got
}

func TestLinkCarriesGuestStreamsToTheirTargetOnly(t *testing.T) {
	needNode(t)
	blob := make([]byte, 1<<20)
	_, _ = rand.Read(blob)
	want := append(append([]byte(nil), blob...), []byte("|5")...)
	target := serveBlob(t, blob)
	guestPort := freePort(t)
	l, err := StartLink(hostNode{}, "guest", []LinkTarget{{GuestIP: "127.0.0.1", Port: guestPort, Dial: target}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	guest := net.JoinHostPort("127.0.0.1", strconv.Itoa(guestPort))

	var wg sync.WaitGroup
	sums := make([][32]byte, 20)
	for i := range sums {
		wg.Go(func() {
			sums[i] = sha256.Sum256(roundTrip(t, guest, []byte("hello")))
		})
	}
	wg.Wait()
	for i, s := range sums {
		if s != sha256.Sum256(want) {
			t.Fatalf("stream %d differs from the target's bytes", i)
		}
	}
}

func TestLinkForwardsPlaneConnectionsIntoTheGuest(t *testing.T) {
	needNode(t)
	inside := serveBlob(t, []byte("from the guest"))
	_, portStr, _ := net.SplitHostPort(inside)
	port, _ := strconv.Atoi(portStr)
	addr, err := guestAddr(hostNode{}, "guest-in", port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeForwards("guest-in") })
	if got := string(roundTrip(t, addr, []byte("abc"))); got != "from the guest|3" {
		t.Fatalf("got %q", got)
	}
	again, err := guestAddr(hostNode{}, "guest-in", port)
	if err != nil || again != addr {
		t.Fatalf("reuse %q %q %v", again, addr, err)
	}
	closeForwards("guest-in")
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = c.Close()
		t.Fatal("forward still listening after close")
	}
}

func TestLinkResetsAnUnknownTargetAndFailsAClosedTarget(t *testing.T) {
	needNode(t)
	dead := freePort(t)
	guestPort := freePort(t)
	l, err := StartLink(hostNode{}, "guest", []LinkTarget{{GuestIP: "127.0.0.1", Port: guestPort, Dial: "127.0.0.1:" + strconv.Itoa(dead)}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// The guest connection ends, by EOF or reset, without data.
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(guestPort)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	got, rerr := io.ReadAll(c)
	_ = c.Close()
	if len(got) != 0 {
		t.Fatalf("got %q from a refused target", got)
	}
	var ne net.Error
	if errors.As(rerr, &ne) && ne.Timeout() {
		t.Fatal("refused target left the guest connection open")
	}
	// An unlisted port has no listener in the guest at all.
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t))), time.Second); err == nil {
		_ = c.Close()
		t.Fatal("guest reached an unlisted port")
	}
}

// guestFrames is a guest side written in Go, for frames node would never send.
type guestFrames struct{ frames [][]byte }

func (g guestFrames) ExecStdio(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	inR, inW := io.Pipe()
	var out bytes.Buffer
	for _, f := range g.frames {
		out.Write(f)
	}
	go func() { _, _ = io.Copy(io.Discard, inR) }()
	return inW, io.NopCloser(&out), func() { _ = inW.Close() }, nil
}

func frame(typ byte, id uint32, p []byte) []byte {
	b := make([]byte, 9+len(p))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:], id)
	binary.BigEndian.PutUint32(b[5:], uint32(len(p)))
	copy(b[9:], p)
	return b
}

func TestLinkEndsOnAMalformedGuest(t *testing.T) {
	big := frame(linkData, 1, nil)
	binary.BigEndian.PutUint32(big[5:], linkChunk+1)
	g := guestFrames{frames: [][]byte{frame(linkReady, 0, nil), frame(linkOpen, 1, []byte{9}), big}}
	l, err := StartLink(g, "guest", []LinkTarget{{GuestIP: "127.0.0.1", Port: 1, Dial: "127.0.0.1:1"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an oversized frame did not end the link")
	}
	if _, err := StartLink(guestFrames{}, "guest", nil); err == nil {
		t.Fatal("a guest that exits before ready started a link")
	}
}

func TestPlaneLinkTargetsAddGitHubOnlyForAgentPublication(t *testing.T) {
	plain := PlaneLinkTargets("127.0.0.1:8080", 8080, false)
	if len(plain) != 1 || plain[0].GuestIP != GuestPlaneIP || plain[0].Dial != "127.0.0.1:8080" {
		t.Fatalf("plain %+v", plain)
	}
	implement := PlaneLinkTargets("127.0.0.1:8080", 8080, true)
	if len(implement) != 3 || implement[1].Dial != "github.com:443" || implement[2].Dial != "api.github.com:443" {
		t.Fatalf("implement %+v", implement)
	}
	for _, tgt := range append(plain, implement...) {
		switch tgt.Dial {
		case "127.0.0.1:8080", "github.com:443", "api.github.com:443":
		default:
			t.Fatalf("unexpected destination %q", tgt.Dial)
		}
	}
}

// scriptedGuest is a guest side the test drives frame by frame.
type scriptedGuest struct {
	out *io.PipeWriter
}

func (g *scriptedGuest) ExecStdio(string, []string, []string) (io.WriteCloser, io.ReadCloser, func(), error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	g.out = outW
	go func() { _, _ = io.Copy(io.Discard, inR) }()
	go func() { _, _ = outW.Write(frame(linkReady, 0, nil)) }()
	return inW, outR, func() { _ = inW.Close(); _ = outW.Close() }, nil
}

// resetTarget accepts and immediately resets every connection.
func resetTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.(*net.TCPConn).SetLinger(0)
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestLinkSurvivesAGuestFloodingAResetStream(t *testing.T) {
	g := &scriptedGuest{}
	l, err := StartLink(g, "guest", []LinkTarget{{GuestIP: "127.0.0.1", Port: 1, Dial: resetTarget(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	chunk := make([]byte, linkChunk)
	for id := uint32(1); id < 40; id += 2 {
		if _, err := g.out.Write(frame(linkOpen, id, []byte{0})); err != nil {
			t.Fatal(err)
		}
		for range 300 {
			if _, err := g.out.Write(frame(linkData, id, chunk)); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case <-l.Done():
		t.Fatal("link ended")
	default:
	}
}

func TestLinkRefusesReusedIdsAndTooManyStreams(t *testing.T) {
	held := serveBlob(t, nil) // holds each connection until the client ends
	g := &scriptedGuest{}
	l, err := StartLink(g, "guest", []LinkTarget{{GuestIP: "127.0.0.1", Port: 1, Dial: held}})
	if err != nil {
		t.Fatal(err)
	}
	for range 200 {
		_, _ = g.out.Write(frame(linkOpen, 1, []byte{0}))
	}
	for id := uint32(3); id < 3+2*(linkMaxStreams+50); id += 2 {
		_, _ = g.out.Write(frame(linkOpen, id, []byte{0}))
	}
	time.Sleep(200 * time.Millisecond)
	l.mu.Lock()
	n := len(l.streams)
	l.mu.Unlock()
	if n > linkMaxStreams {
		t.Fatalf("%d streams, cap %d", n, linkMaxStreams)
	}
	l.Close()
	l.mu.Lock()
	n = len(l.streams)
	l.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d streams left after close", n)
	}
}
