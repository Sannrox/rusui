package sumika

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

func TestResizeUsesSumikaProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sumika.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	received := make(chan request, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req request
		if json.NewDecoder(conn).Decode(&req) == nil {
			received <- req
			_ = json.NewEncoder(conn).Encode(response{OK: true})
		}
	}()

	client := NewSocketClient(path)
	if err := client.Resize("rusui-4", 132, 44); err != nil {
		t.Fatal(err)
	}
	got := <-received
	if got.Op != "resize" || got.Name != "rusui-4" || got.Cols != 132 || got.Rows != 44 {
		t.Fatalf("resize request %+v", got)
	}
}
