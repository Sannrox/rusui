// Package guestimage carries the reference guest Dockerfile (ADR 0018 D4)
// so rusui setup can build it without a source checkout.
package guestimage

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed Dockerfile
var Dockerfile []byte

// Repository is the local image name setup builds.
const Repository = "rusui-guest"

// Tag is content-addressed: a changed Dockerfile yields a new tag.
func Tag() string {
	sum := sha256.Sum256(Dockerfile)
	return Repository + ":" + hex.EncodeToString(sum[:])[:16]
}
