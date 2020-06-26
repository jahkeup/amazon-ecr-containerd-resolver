/*
 * Copyright 2017-2019 Amazon.com, Inc. or its affiliates. All Rights Reserved.
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
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/awstesting/unit"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/containerd/containerd/reference"
	"github.com/stretchr/testify/assert"

	"github.com/awslabs/amazon-ecr-containerd-resolver/ecr/internal/testdata"
)

func TestParseImageManifestMediaType(t *testing.T) {
	for _, sample := range []testdata.MediaTypeSample{
		testdata.DockerSchema1Manifest,
		testdata.DockerSchema1ManifestUnsigned,
		testdata.DockerSchema2Manifest,
		testdata.DockerSchema2ManifestList,
		testdata.OCIImageIndex,
		testdata.OCIImageManifest,
		testdata.EmptySample,
	} {
		t.Run(sample.MediaType(), func(t *testing.T) {
			t.Logf("content: %s", sample.Content())
			actual := parseImageManifestMediaType(context.Background(), sample.Content())
			if sample.MediaType() != "" {
				assert.NotEmpty(t, actual)
			}
			assert.Equal(t, sample.MediaType(), actual)
		})
	}
}

func TestResolve(t *testing.T) {
	// Test data
	resolveRef := testdata.FakeRef
	resolveManifest := testdata.OCIImageIndex

	// API output
	image := &ecr.Image{
		RepositoryName: aws.String(testdata.FakeRepository),
		ImageId: &ecr.ImageIdentifier{
			ImageDigest: aws.String(resolveManifest.Digest().String()),
		},
		ImageManifest: aws.String(resolveManifest.Content()),
	}

	fakeClient := &fakeECRClient{
		BatchGetImageFn: func(ctx aws.Context, input *ecr.BatchGetImageInput, opts ...request.Option) (*ecr.BatchGetImageOutput, error) {
			assert.Equal(t, testdata.FakeRegistryID, aws.StringValue(input.RegistryId))
			assert.Equal(t, testdata.FakeRepository, aws.StringValue(input.RepositoryName))
			assert.Equal(t, []*ecr.ImageIdentifier{{ImageTag: aws.String(testdata.FakeImageTag)}}, input.ImageIds)
			return &ecr.BatchGetImageOutput{Images: []*ecr.Image{image}}, nil
		},
	}

	resolver := &ecrResolver{
		clients: map[string]ecrAPI{
			testdata.FakeRegion: fakeClient,
		},
	}

	t.Logf("resolving ref: %q", resolveRef)
	ref, desc, err := resolver.Resolve(context.Background(), resolveRef)
	assert.NoError(t, err)
	assert.Equal(t, resolveRef, ref)
	assert.Equal(t, resolveManifest.Descriptor(), desc)
}

func TestResolveError(t *testing.T) {
	// expected output
	expectedError := errors.New("expected")

	fakeClient := &fakeECRClient{
		BatchGetImageFn: func(aws.Context, *ecr.BatchGetImageInput, ...request.Option) (*ecr.BatchGetImageOutput, error) {
			return nil, expectedError
		},
	}
	resolver := &ecrResolver{
		clients: map[string]ecrAPI{
			testdata.FakeRegion: fakeClient,
		},
	}

	ref, desc, err := resolver.Resolve(context.Background(), testdata.FakeRef)
	assert.EqualError(t, err, expectedError.Error())
	assert.Empty(t, ref)
	assert.Empty(t, desc)
}

func TestResolveNoResult(t *testing.T) {
	fakeClient := &fakeECRClient{
		BatchGetImageFn: func(aws.Context, *ecr.BatchGetImageInput, ...request.Option) (*ecr.BatchGetImageOutput, error) {
			return &ecr.BatchGetImageOutput{}, nil
		},
	}
	resolver := &ecrResolver{
		clients: map[string]ecrAPI{
			testdata.FakeRegion: fakeClient,
		},
	}

	ref, desc, err := resolver.Resolve(context.Background(), testdata.FakeRef)
	assert.Error(t, err)
	assert.Equal(t, reference.ErrInvalid, err)
	assert.Empty(t, ref)
	assert.Empty(t, desc)
}

func TestResolvePusherDenyDigest(t *testing.T) {
	for _, ref := range []string{
		testdata.FakeRefWithObject("@" + testdata.ImageDigest.String()),
	} {
		t.Run(ref, func(t *testing.T) {
			resolver := &ecrResolver{}
			p, err := resolver.Pusher(context.Background(), ref)
			assert.Error(t, err)
			assert.Nil(t, p)
		})
	}
}

func TestResolvePusherAllowTagDigest(t *testing.T) {
	for _, ref := range []string{
		testdata.FakeRefWithObject(":tag-and-digest@" + testdata.ImageDigest.String()),
	} {
		t.Run(ref, func(t *testing.T) {
			resolver := &ecrResolver{
				// Stub session
				session: unit.Session,
				clients: map[string]ecrAPI{},
			}
			p, err := resolver.Pusher(context.Background(), ref)
			assert.NoError(t, err)
			assert.NotNil(t, p)
		})
	}
}
