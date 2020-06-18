package testdata

import (
	_ "crypto/sha256"
	"fmt"
	"math/rand"
	"time"

	"github.com/opencontainers/go-digest"
)

var (
	// generatorRand is an intentionally deterministic random generator. It is
	// used for producing pseudo-random data for generating digests suitable for
	// *testing only*.
	generatorRand = rand.New(rand.NewSource(1))
)

const (
	// InsignificantDigest is an arbitrary digest that should be used by tests utilizing a
	// valid digest string.
	InsignificantDigest digest.Digest = "sha256:9d2b264e346ccee1a96820dc5c3bd8cc2f5fa6c69c2dca4ed8be2173422779c7"
	// LayerDigest is used for layer digests in tests.
	LayerDigest = InsignificantDigest
	// ImageDigest is used for image digests in tests.
	ImageDigest = InsignificantDigest
)

// GenerateDigest returns a psuedo-random digest for use as a distinct digest in
// tests. The produced value does not use a secure random number generator and
// will give out deterministic values.
func GenerateDigest() digest.Digest {
	d := digest.SHA256.Digester()
	fmt.Fprintf(d.Hash(), "%s", time.Now().UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(d.Hash(), "%d", generatorRand.Int())
	return d.Digest()
}
