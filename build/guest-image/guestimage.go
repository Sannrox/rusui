// Package guestimage carries the reference guest Dockerfile (ADR 0018 D4)
// so rusui setup can build it without a source checkout.
package guestimage

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strings"
)

//go:embed Dockerfile
var Dockerfile []byte

// Repository is the local image name setup builds.
const Repository = "rusui-guest"

// CacheTag names a build of this Dockerfile. It is only a cache key: two
// builds of the same text can differ (base layers, apt, npm).
func CacheTag() string {
	sum := sha256.Sum256(Dockerfile)
	return Repository + ":build-" + hex.EncodeToString(sum[:])[:16]
}

// ContentTag names the built image by its image ID, so the recorded tag
// identifies the bits the guest runs.
func ContentTag(imageID string) string {
	id := strings.TrimPrefix(imageID, "sha256:")
	if len(id) > 16 {
		id = id[:16]
	}
	return Repository + ":" + id
}
