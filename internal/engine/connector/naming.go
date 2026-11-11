package connector

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const maxToolNameLength = 64

// PiToolName is the name Pi 1.5.2 gives a server's tool
// (@earendil-works/pi-mcp createMcpToolName): mcp__<server>__<tool>, every
// character but [A-Za-z0-9_] replaced by "_", and over 64 characters cut with
// an 8-hex-digit hash of server and tool. The proxy uses it to match a
// tools/call to the permission the person gave for that name.
func PiToolName(server, tool string) string {
	// JavaScript replaces UTF-16 code units: a character outside the BMP
	// becomes two underscores.
	var b strings.Builder
	for _, r := range "mcp__" + server + "__" + tool {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	name := b.String()
	if len(name) <= maxToolNameLength {
		return name
	}
	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	return name[:maxToolNameLength-9] + "_" + hex.EncodeToString(sum[:])[:8]
}
