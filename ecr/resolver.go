/*
 * Copyright 2017-2020 Amazon.com, Inc. or its affiliates. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"). You
 * may not use this file except in compliance with the License. A copy of
 * the License is located at
 *
 * 	http://aws.amazon.com/apache2.0/
 *
 * or in the "license" file accompanying this file. This file is
 * distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF
 * ANY KIND, either express or implied. See the License for the specific
 * language governing permissions and limitations under the License.
 */

package ecr

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	ecrsdk "github.com/aws/aws-sdk-go/service/ecr"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reference"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var unimplemented = errors.New("unimplemented")

type ecrResolver struct {
	session                  *session.Session
	clients                  map[string]ecrAPI
	clientsLock              sync.Mutex
	tracker                  docker.StatusTracker
	layerDownloadParallelism int
}

// ResolverOption represents a functional option for configuring the ECR
// Resolver
type ResolverOption func(*ResolverOptions) error

// ResolverOptions represents available options for configuring the ECR Resolver
type ResolverOptions struct {
	// Session is used for configuring the ECR client.  If not specified, a
	// generic session is used.
	Session *session.Session
	// Tracker is used to track uploads to ECR.  If not specified, an in-memory
	// tracker is used instead.
	Tracker docker.StatusTracker
	// LayerDownloadParallelism configures whether layer parts should be
	// downloaded in parallel.  If not specified, parallelism is currently
	// disabled.
	LayerDownloadParallelism int
}

// WithSession is a ResolverOption to use a specific AWS session.Session
func WithSession(session *session.Session) ResolverOption {
	return func(options *ResolverOptions) error {
		options.Session = session
		return nil
	}
}

// WithTracker is a ResolverOption to use a specific docker.Tracker
func WithTracker(tracker docker.StatusTracker) ResolverOption {
	return func(options *ResolverOptions) error {
		options.Tracker = tracker
		return nil
	}
}

// WithLayerDownloadParallelism is a ResolverOption to configure whether layer
// parts should be downloaded in parallel.  Layer parallelism is backed by the
// htcat library and can increase the speed at which layers are downloaded at
// the cost of increased memory consumption.  It is recommended to test your
// workload to determine whether the tradeoff is worthwhile.
func WithLayerDownloadParallelism(parallelism int) ResolverOption {
	return func(options *ResolverOptions) error {
		options.LayerDownloadParallelism = parallelism
		return nil
	}
}

// NewResolver creates a new remotes.Resolver capable of interacting with Amazon
// ECR.  NewResolver can be called with no arguments for default configuration,
// or can be customized by specifying ResolverOptions.  By default, NewResolver
// will allocate a new AWS session.Session and an in-memory tracker for layer
// progress.
func NewResolver(options ...ResolverOption) (remotes.Resolver, error) {
	resolverOptions := &ResolverOptions{}
	for _, option := range options {
		err := option(resolverOptions)
		if err != nil {
			return nil, err
		}
	}
	if resolverOptions.Session == nil {
		awsSession, err := session.NewSession()
		if err != nil {
			return nil, err
		}
		resolverOptions.Session = awsSession
	}
	if resolverOptions.Tracker == nil {
		resolverOptions.Tracker = docker.NewInMemoryTracker()
	}
	return &ecrResolver{
		session:                  resolverOptions.Session,
		clients:                  map[string]ecrAPI{},
		tracker:                  resolverOptions.Tracker,
		layerDownloadParallelism: resolverOptions.LayerDownloadParallelism,
	}, nil
}

// Resolve attempts to resolve the provided reference into a name and a
// descriptor.
//
// Valid references are of the form "ecr.aws/arn:aws:ecr:<region>:<account>:repository/<name>:<tag>".
func (r *ecrResolver) Resolve(ctx context.Context, ref string) (string, ocispec.Descriptor, error) {
	ecrSpec, err := ParseRef(ref)
	if err != nil {
		return "", ocispec.Descriptor{}, err
	}

	if ecrSpec.Object == "" {
		return "", ocispec.Descriptor{}, reference.ErrObjectRequired
	}

	batchGetImageInput := &ecr.BatchGetImageInput{
		RegistryId:         aws.String(ecrSpec.Registry()),
		RepositoryName:     aws.String(ecrSpec.Repository),
		ImageIds:           []*ecr.ImageIdentifier{ecrSpec.ImageID()},
		AcceptedMediaTypes: aws.StringSlice(supportedImageMediaTypes),
	}

	client := r.getClient(ecrSpec.Region())

	batchGetImageOutput, err := client.BatchGetImageWithContext(ctx, batchGetImageInput)
	if err != nil {
		log.G(ctx).
			WithField("ref", ref).
			WithError(err).
			Warn("Failed while calling BatchGetImage")
		return "", ocispec.Descriptor{}, err
	}
	log.G(ctx).
		WithField("ref", ref).
		WithField("batchGetImageOutput", batchGetImageOutput).
		Debug("ecr.resolver.resolve")

	if len(batchGetImageOutput.Images) == 0 {
		return "", ocispec.Descriptor{}, reference.ErrInvalid
	}
	ecrImage := batchGetImageOutput.Images[0]

	mediaType := parseImageManifestMediaType(ctx, aws.StringValue(ecrImage.ImageManifest))
	log.G(ctx).
		WithField("ref", ref).
		WithField("mediaType", mediaType).
		Debug("ecr.resolver.resolve")
	// check resolved image's mediaType, it should be one of the specified in
	// the request.
	for i, accepted := range aws.StringValueSlice(batchGetImageInput.AcceptedMediaTypes) {
		if mediaType == accepted {
			break
		}
		if i+1 == len(batchGetImageInput.AcceptedMediaTypes) {
			return "", ocispec.Descriptor{}, errors.Wrap(errdefs.ErrFailedPrecondition, "resolved mediaType not in accepted types")
		}
	}

	desc := ocispec.Descriptor{
		Digest:    digest.Digest(aws.StringValue(ecrImage.ImageId.ImageDigest)),
		MediaType: mediaType,
		Size:      int64(len(aws.StringValue(ecrImage.ImageManifest))),
	}
	// assert matching digest if the provided ref includes one.
	if expectedDigest := ecrSpec.Spec().Digest().String(); expectedDigest != "" &&
		desc.Digest.String() != expectedDigest {
		return "", ocispec.Descriptor{}, errors.Wrap(errdefs.ErrFailedPrecondition, "resolved image digest mismatch")
	}

	return ecrSpec.Canonical(), desc, nil
}

func (r *ecrResolver) getClient(region string) ecrAPI {
	r.clientsLock.Lock()
	defer r.clientsLock.Unlock()
	if _, ok := r.clients[region]; !ok {
		r.clients[region] = ecrsdk.New(r.session, &aws.Config{Region: aws.String(region)})
	}
	return r.clients[region]
}

// manifestProbe provides a structure to parse and then probe a given manifest
// to determine its mediaType.
type manifestProbe struct {
	// SchemaVersion is version identifier for the manifest schema used.
	SchemaVersion int64 `json:"schemaVersion"`
	// Explicit MediaType assignment for the manifest.
	MediaType string `json:"mediaType,omitempty"`
	// Docker Schema 1 signatures.
	Signatures json.RawMessage `json:"signatures,omitempty"`
	// OCI or Docker Manifest Lists, the list of descriptors has mediaTypes
	// embedded.
	Manifests []ocispec.Descriptor `json:"manifests,omitempty"`
	// Config is the Image Config descriptor which provides the targeted
	// mediaType.
	Config ocispec.Descriptor `json:"config,omitempty"`
}

// TODO: make this error and handle in caller.
func parseImageManifestMediaType(ctx context.Context, body string) string {
	// The mediaType specified for the unsigned variant of the Docker v2 Schema
	// 1 manifest.
	const mediaTypeDockerSchema1ManifestUnsigned = "application/vnd.docker.distribution.manifest.v1+json"

	var manifest manifestProbe
	err := json.Unmarshal([]byte(body), &manifest)
	if err != nil {
		log.G(ctx).WithField("manifest", body).
			WithError(err).Warn("ecr.resolver.resolve: could not parse manifest")
		// Default to schema 2 for now.
		return images.MediaTypeDockerSchema2Manifest
	}

	// Defer to the manifest declared type.
	if manifest.MediaType != "" {
		return manifest.MediaType
	}

	switch manifest.SchemaVersion {
	case 2:
		switch manifest.Config.MediaType {
		case images.MediaTypeDockerSchema2Config:
			return images.MediaTypeDockerSchema2Manifest
		case ocispec.MediaTypeImageConfig:
			return ocispec.MediaTypeImageManifest
		}

		// This is a single image manifest, prefer to "cast" to an OCI image
		// media type.
		if len(manifest.Manifests) == 0 {
			return ocispec.MediaTypeImageManifest
		}

		// Parse mediaType based on elements, docker manifests are generally
		// pushed by docker.
		for _, elm := range manifest.Manifests {
			switch elm.MediaType {
			case ocispec.MediaTypeImageManifest:
				return ocispec.MediaTypeImageIndex
			case images.MediaTypeDockerSchema2Manifest:
				return images.MediaTypeDockerSchema2ManifestList
			}
		}

		// Otherwise, this may be an OCI Index containing unhandled mediaTypes
		// for other uses.
		return ocispec.MediaTypeImageIndex

	case 1:
		// Signed Docker Schema 1
		if len(manifest.Signatures) != 0 {
			return images.MediaTypeDockerSchema1Manifest
		}
		return mediaTypeDockerSchema1ManifestUnsigned
	}

	return ""
}

func (r *ecrResolver) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	log.G(ctx).WithField("ref", ref).Debug("ecr.resolver.fetcher")
	ecrSpec, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	return &ecrFetcher{
		ecrBase: ecrBase{
			client:  r.getClient(ecrSpec.Region()),
			ecrSpec: ecrSpec,
		},
		parallelism: r.layerDownloadParallelism,
	}, nil
}

func (r *ecrResolver) Pusher(ctx context.Context, ref string) (remotes.Pusher, error) {
	log.G(ctx).WithField("ref", ref).Debug("ecr.resolver.pusher")
	ecrSpec, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}

	// ECR does not allow push by digest; references will include a digest when
	// the ref is being pushed to a tag to denote *which* digest is the root
	// descriptor in this push.
	tag, digest := ecrSpec.TagDigest()
	if tag == "" && digest != "" {
		return nil, errors.New("pusher: cannot use digest reference for push location")
	}

	// The root descriptor's digest *must* be provided in order to properly tag
	// manifests. A ref string will provide this as of containerd v1.3.0 -
	// earlier versions do not provide it.
	if digest == "" {
		return nil, errors.New("pusher: root descriptor missing from push reference")
	}

	return &ecrPusher{
		ecrBase: ecrBase{
			client:  r.getClient(ecrSpec.Region()),
			ecrSpec: ecrSpec,
		},
		tracker: r.tracker,
	}, nil
}
