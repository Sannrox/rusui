package server

import (
	"bytes"
)

func refFromReceiveCmd(line []byte) string {
	if i := bytes.IndexByte(line, 0); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimSpace(line)
	parts := bytes.Fields(line)
	if len(parts) < 3 {
		return ""
	}
	return string(parts[2])
}
