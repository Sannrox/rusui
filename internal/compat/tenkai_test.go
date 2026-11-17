package compat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateTenkai = flag.Bool("update", false, "rewrite the deploy/tenkai.toml compatibility block")

var (
	tenkaiProduct  = filepath.Join("..", "..", "deploy", "tenkai.toml")
	versionFile    = filepath.Join("..", "..", "VERSION")
	operatorGolden = filepath.Join("..", "server", "testdata", "operator-api.golden.json")
)

// admissionRoutes are the operator-API golden entries of the
// `rusui.admission` contract. Every other entry is `rusui.environment`.
var admissionRoutes = map[string]bool{"POST /projects/{slug}/sessions": true}

// contractVersions maps each Tenkai contract and the digest of its pinned
// response shapes to the contract's semantic version (#692). A shape change
// fails TestTenkaiProfileIsCurrent until a row names the new digest: a
// minor bump for an added field, a major one for a removed, renamed, or
// retyped field. Keep old rows; they record what each version was.
var contractVersions = map[string]map[string]string{
	"rusui.admission": {
		"sha256:22c6c0d985699fc8dcda1dc026f479757194918bb0cabe319146ccdaea61f18f": "1.0.0",
	},
	"rusui.environment": {
		"sha256:ed9751fbf6815c015253dc8440100b0198b33e1e6d654b32fee1a09ebe4af27a": "1.0.0",
	},
}

// TestTenkaiProfileIsCurrent fails when deploy/tenkai.toml does not end
// with the profile v2 block generated from VERSION and the pinned
// operator-API shapes, or when its product version or install version
// lags VERSION. `-update` rewrites the block.
func TestTenkaiProfileIsCurrent(t *testing.T) {
	rawVersion, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(rawVersion))
	golden, err := os.ReadFile(operatorGolden)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := contractDigests(golden)
	if err != nil {
		t.Fatal(err)
	}
	provides := map[string]string{}
	for contract, digest := range digests {
		v, ok := contractVersions[contract][digest]
		if !ok {
			t.Errorf("%s shapes changed (%s): add a row to contractVersions with the new version", contract, digest)
		}
		provides[contract] = v
	}
	if t.Failed() {
		return
	}
	want := tenkaiProfile(version, provides)

	product, err := os.ReadFile(tenkaiProduct)
	if err != nil {
		t.Fatal(err)
	}
	text := string(product)
	head, _, found := strings.Cut(text, "[compatibility]\n")
	if !found {
		t.Fatalf("%s has no [compatibility] block", tenkaiProduct)
	}
	if *updateTenkai {
		if err := os.WriteFile(tenkaiProduct, []byte(head+"[compatibility]\n"+want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !strings.HasSuffix(text, want) {
		t.Errorf("%s compatibility block is stale; run go test ./internal/compat -run TestTenkaiProfileIsCurrent -update\nwant:\n%s", tenkaiProduct, want)
	}
	for _, line := range []string{fmt.Sprintf("version = %q\n", version), fmt.Sprintf("V=%s\n", version)} {
		if !strings.Contains(head, line) {
			t.Errorf("%s lacks %q for VERSION %s", tenkaiProduct, strings.TrimSpace(line), version)
		}
	}
}

// contractDigests splits the operator-API golden file into the two
// contracts and digests each part's canonical JSON.
func contractDigests(golden []byte) (map[string]string, error) {
	var routes map[string]json.RawMessage
	if err := json.Unmarshal(golden, &routes); err != nil {
		return nil, err
	}
	parts := map[string]map[string]json.RawMessage{"rusui.admission": {}, "rusui.environment": {}}
	for route, shape := range routes {
		contract := "rusui.environment"
		if admissionRoutes[route] {
			contract = "rusui.admission"
		}
		parts[contract][route] = shape
	}
	out := map[string]string{}
	for contract, part := range parts {
		canon, err := json.Marshal(part) // map keys marshal sorted
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(canon)
		out[contract] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return out, nil
}

// tenkaiProfile is the generated tail of deploy/tenkai.toml, starting at
// the [compatibility] table body.
func tenkaiProfile(version string, provides map[string]string) string {
	pin := sha256.Sum256([]byte("rusui@" + version))
	return fmt.Sprintf(`version = 2
max_evidence_age_ms = 86400000

[compatibility.components.rusui.pin]
kind = "digest"
value = "sha256:%s"

[compatibility.components.rusui.provides]
"rusui.admission" = %q
"rusui.environment" = %q
`, hex.EncodeToString(pin[:]), provides["rusui.admission"], provides["rusui.environment"])
}
