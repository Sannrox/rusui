package server

import (
	"bytes"
	"fmt"
	"strings"
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

func sessionRefAllowed(sessionID int64, ref string) bool {
	prefix := fmt.Sprintf("refs/heads/rusui/%d", sessionID)
	if ref == prefix {
		return true
	}
	return strings.HasPrefix(ref, prefix+"/")
}
