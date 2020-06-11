package testdata

import "github.com/opencontainers/go-digest"

const (
	// InsignificantDigest is an arbitrary digest that should be used by tests utilizing a
	// valid digest string.
	InsignificantDigest digest.Digest = "sha256:9d2b264e346ccee1a96820dc5c3bd8cc2f5fa6c69c2dca4ed8be2173422779c7"
	// LayerDigest is used for layer digests in tests.
	LayerDigest = InsignificantDigest
	// ImageDigest is used for image digests in tests.
	ImageDigest = InsignificantDigest
)
