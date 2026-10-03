package env

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// A guest link carries TCP streams between a network-less guest and the
// plane host over one `docker exec -i` stdio pair (ADR 0047). The guest
// side listens on the loopback addresses the link names and opens a
// stream per connection; the host side dials the fixed target for it.
// The host side may also open a stream to a guest port for preview. The
// link forwards bytes: TLS stays end to end.

// Link frame types.
const (
	linkOpen  = 1 // guest→host: target index; host→guest: guest port
	linkData  = 2
	linkEnd   = 3 // sender finished writing (half close)
	linkReset = 4 // stream gone
	linkReady = 5 // guest listeners are bound
)

const (
	linkChunk      = 32 << 10
	linkQueue      = 16
	linkStartLimit = 15 * time.Second
	linkDialLimit  = 10 * time.Second
	// linkMaxStreams bounds the streams one link holds at once, so a
	// guest cannot exhaust host memory or descriptors.
	linkMaxStreams = 64
)

// LinkTarget is one destination a guest may reach: inside the guest it is
// GuestIP:Port, on the host the link dials Dial.
type LinkTarget struct {
	GuestIP string
	Port    int
	Dial    string
}

// linkJS is the guest side. argv[1] is a JSON list of listeners
// {ip, port, t}; t indexes the host's target list.
const linkJS = `const net=require("net");const L=JSON.parse(process.argv[1]);const out=process.stdout;const socks=new Map();let next=1;
function send(t,id,p){p=p||Buffer.alloc(0);const h=Buffer.alloc(9);h[0]=t;h.writeUInt32BE(id,1);h.writeUInt32BE(p.length,5);out.write(Buffer.concat([h,p]));}
function attach(id,s){socks.set(id,s);s.on("data",d=>{for(let i=0;i<d.length;i+=32768)send(2,id,d.subarray(i,i+32768));});s.on("end",()=>{if(socks.get(id)===s)send(3,id);});s.on("error",()=>{});s.on("close",()=>{if(socks.get(id)===s){socks.delete(id);send(4,id);}});}
let pending=L.length;const ready=()=>{if(--pending<=0)send(5,0);};if(pending===0)send(5,0);
for(const l of L){net.createServer({allowHalfOpen:true},c=>{const id=next;next+=2;send(1,id,Buffer.from([l.t]));attach(id,c);}).on("error",e=>{process.stderr.write("rusui-link: listen "+l.ip+":"+l.port+": "+e.message+"\n");process.exit(1);}).listen(l.port,l.ip,ready);}
let buf=Buffer.alloc(0);process.stdin.on("data",d=>{buf=Buffer.concat([buf,d]);while(buf.length>=9){const n=buf.readUInt32BE(5);if(buf.length<9+n)break;const t=buf[0],id=buf.readUInt32BE(1),p=buf.subarray(9,9+n);buf=buf.subarray(9+n);const s=socks.get(id);
if(t===1){const c=net.connect({port:p.readUInt16BE(0),host:"127.0.0.1",allowHalfOpen:true});attach(id,c);}else if(t===2){if(s){s.write(p);if(s.writableLength>8388608)s.destroy();}}else if(t===3){if(s)s.end();}else if(t===4){if(s){socks.delete(id);s.destroy();}}}});
process.stdin.on("end",()=>process.exit(0));`

// IsLinkArgv reports whether argv starts a guest link's guest side.
func IsLinkArgv(argv []string) bool {
	return len(argv) >= 3 && argv[0] == "node" && argv[1] == "-e" && argv[2] == linkJS
}

// Link is the host side of one guest link.
type Link struct {
	targets []LinkTarget
	w       io.WriteCloser
	stop    func()
	wmu     sync.Mutex

	// mu guards streams, next, and closed. A stream's channel is sent on
	// and closed only while mu is held. lastGuest belongs to read.
	mu        sync.Mutex
	streams   map[uint32]*linkStream
	next      uint32
	lastGuest uint32
	closed    bool

	ready chan struct{}
	done  chan struct{}
	err   error
}

type linkFrame struct {
	typ  byte
	data []byte
}

type linkStream struct {
	in chan linkFrame
}

// StartLink execs the guest side in handle and waits until its listeners
// are bound. Each target is reachable in the guest at GuestIP:Port.
func StartLink(x StdioExecutor, handle string, targets []LinkTarget) (*Link, error) {
	if x == nil || handle == "" {
		return nil, fmt.Errorf("env: guest link requires a container")
	}
	type listen struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
		T    int    `json:"t"`
	}
	ls := make([]listen, 0, len(targets))
	for i, t := range targets {
		if net.ParseIP(t.GuestIP) == nil || t.Port < 1 || t.Port > 65535 || t.Dial == "" {
			return nil, fmt.Errorf("env: invalid link target %d", i)
		}
		ls = append(ls, listen{IP: t.GuestIP, Port: t.Port, T: i})
	}
	arg, err := json.Marshal(ls)
	if err != nil {
		return nil, err
	}
	stdin, stdout, stop, err := x.ExecStdio(handle, []string{"node", "-e", linkJS, string(arg)}, nil)
	if err != nil {
		return nil, fmt.Errorf("env: start guest link: %w", err)
	}
	l := &Link{targets: targets, w: stdin, stop: stop, streams: map[uint32]*linkStream{}, ready: make(chan struct{}), done: make(chan struct{})}
	go l.read(stdout)
	select {
	case <-l.ready:
		return l, nil
	case <-l.done:
		stop()
		return nil, fmt.Errorf("env: guest link exited before ready: %w", l.err)
	case <-time.After(linkStartLimit):
		l.Close()
		return nil, fmt.Errorf("env: guest link not ready after %s", linkStartLimit)
	}
}

// Done is closed when the link has ended.
func (l *Link) Done() <-chan struct{} { return l.done }

// Close ends the link and every stream on it.
func (l *Link) Close() {
	if l.stop != nil {
		l.stop()
	}
	<-l.done
}

// Forward carries conn to port on the guest's loopback.
func (l *Link) Forward(conn net.Conn, port int) error {
	if err := validGuestPort(port); err != nil {
		return err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return fmt.Errorf("env: guest link closed")
	}
	if len(l.streams) >= linkMaxStreams {
		l.mu.Unlock()
		return fmt.Errorf("env: guest link busy")
	}
	l.next += 2
	id := l.next
	s := &linkStream{in: make(chan linkFrame, linkQueue)}
	l.streams[id] = s
	l.mu.Unlock()
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], uint16(port))
	if err := l.send(linkOpen, id, p[:]); err != nil {
		l.drop(id)
		return err
	}
	go l.serve(id, s, conn)
	return nil
}

func (l *Link) send(typ byte, id uint32, p []byte) error {
	b := make([]byte, 9+len(p))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:], id)
	binary.BigEndian.PutUint32(b[5:], uint32(len(p)))
	copy(b[9:], p)
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_, err := l.w.Write(b)
	return err
}

func (l *Link) read(r io.Reader) {
	defer func() {
		l.mu.Lock()
		l.closed = true
		for id, s := range l.streams {
			close(s.in)
			delete(l.streams, id)
		}
		l.mu.Unlock()
		close(l.done)
	}()
	var h [9]byte
	for {
		if _, err := io.ReadFull(r, h[:]); err != nil {
			l.err = err
			return
		}
		id := binary.BigEndian.Uint32(h[1:])
		n := binary.BigEndian.Uint32(h[5:])
		if n > linkChunk {
			l.err = fmt.Errorf("frame of %d bytes", n)
			return
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(r, p); err != nil {
			l.err = err
			return
		}
		switch h[0] {
		case linkReady:
			select {
			case <-l.ready:
			default:
				close(l.ready)
			}
		case linkOpen:
			// Guest stream ids are odd and strictly increasing, so an id
			// is never reused while its stream may still be live.
			if len(p) != 1 || int(p[0]) >= len(l.targets) || id%2 == 0 || id <= l.lastGuest {
				_ = l.send(linkReset, id, nil)
				continue
			}
			l.lastGuest = id
			s := &linkStream{in: make(chan linkFrame, linkQueue)}
			l.mu.Lock()
			full := len(l.streams) >= linkMaxStreams
			if !full {
				l.streams[id] = s
			}
			l.mu.Unlock()
			if full {
				_ = l.send(linkReset, id, nil)
				continue
			}
			go l.dialServe(id, s, l.targets[p[0]].Dial)
		default:
			reset := false
			l.mu.Lock()
			if s := l.streams[id]; s != nil {
				select {
				case s.in <- linkFrame{typ: h[0], data: p}:
				default:
					// A stream that cannot keep up is reset, so it never
					// stalls the other streams on the link.
					delete(l.streams, id)
					close(s.in)
					reset = true
				}
			}
			l.mu.Unlock()
			if reset {
				_ = l.send(linkReset, id, nil)
			}
		}
	}
}

func (l *Link) drop(id uint32) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.streams[id]; s != nil {
		delete(l.streams, id)
		close(s.in)
	}
}

func (l *Link) dialServe(id uint32, s *linkStream, addr string) {
	conn, err := net.DialTimeout("tcp", addr, linkDialLimit)
	if err != nil {
		l.drop(id)
		_ = l.send(linkReset, id, nil)
		return
	}
	l.serve(id, s, conn)
}

// serve copies conn to the link and the stream's frames to conn until
// both directions end or either side resets.
func (l *Link) serve(id uint32, s *linkStream, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	go func() {
		buf := make([]byte, linkChunk)
		for {
			n, err := conn.Read(buf)
			if n > 0 && l.send(linkData, id, buf[:n]) != nil {
				return
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					_ = l.send(linkEnd, id, nil)
				} else {
					l.drop(id)
					_ = l.send(linkReset, id, nil)
				}
				return
			}
		}
	}()
	for f := range s.in {
		switch f.typ {
		case linkData:
			if _, err := conn.Write(f.data); err != nil {
				l.drop(id)
				_ = l.send(linkReset, id, nil)
				return
			}
		case linkEnd:
			if cw, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
		case linkReset:
			l.drop(id)
			return
		}
	}
}

// PlaneLinkTargets are the outbound targets of a managed turn's link: the
// plane listener, and for an implement session that talks to GitHub
// itself (ADR 0015) the GitHub hosts the guest needs.
func PlaneLinkTargets(planeDial string, planePort int, github bool) []LinkTarget {
	out := []LinkTarget{{GuestIP: GuestPlaneIP, Port: planePort, Dial: planeDial}}
	if github {
		out = append(out,
			LinkTarget{GuestIP: GuestGitHubIP, Port: 443, Dial: net.JoinHostPort("github.com", strconv.Itoa(443))},
			LinkTarget{GuestIP: GuestGitHubAPIIP, Port: 443, Dial: net.JoinHostPort("api.github.com", strconv.Itoa(443))},
		)
	}
	return out
}
