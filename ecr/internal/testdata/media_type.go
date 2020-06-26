package testdata

import (
	"fmt"
	"strings"
	_ "crypto/sha256"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// MediaTypeSample provides a sample document for a given mediaType.
type MediaTypeSample struct {
	mediaType string
	content string
}

// MediaType is the defined sample's actual mediaType.
func (s *MediaTypeSample) MediaType() string {
	return s.mediaType
}

// Content provides the sample's JSON data as a string.
func (s *MediaTypeSample) Content() string {
	return strings.TrimSpace(s.content)
}

// Digest is the hashed digest of the sample document.
func (s *MediaTypeSample) Digest() digest.Digest {
	dgst := digest.SHA256.Digester()
	fmt.Fprint(dgst.Hash(), s.Content())
	return dgst.Digest()
}

// Descriptor provides a populated OCI Descriptor respective to the sample
// document's data.
func (s *MediaTypeSample) Descriptor() ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: s.MediaType(),
		Digest: s.Digest(),
		Size: int64(len(s.Content())),
	}
}

// EmptySample is an edge case sample, use
var EmptySample = MediaTypeSample{
	mediaType: "",
	content: `
{
  "updog": "whats"
}`,
}
