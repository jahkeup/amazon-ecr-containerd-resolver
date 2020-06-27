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
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/awslabs/amazon-ecr-containerd-resolver/ecr/internal/testdata"
)

func TestFetchUnimplemented(t *testing.T) {
	fetcher := &ecrFetcher{}
	desc := ocispec.Descriptor{
		MediaType: "never-implemented",
	}
	_, err := fetcher.Fetch(context.Background(), desc)
	assert.EqualError(t, err, errUnimplemented.Error())
}

func TestFetchForeignLayer(t *testing.T) {
	// setup
	expectedBody := "hello this is dog"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, expectedBody)
	}))
	defer ts.Close()

	fetcher := &ecrFetcher{}

	// test both media types
	for _, mediaType := range []string{
		images.MediaTypeDockerSchema2LayerForeign,
		images.MediaTypeDockerSchema2LayerForeignGzip,
	} {
		t.Run(mediaType, func(t *testing.T) {
			// input
			desc := ocispec.Descriptor{
				MediaType: mediaType,
				URLs:      []string{ts.URL},
			}

			reader, err := fetcher.Fetch(context.Background(), desc)
			require.NoError(t, err, "fetch should succeed from test server")
			defer reader.Close()

			output, err := ioutil.ReadAll(reader)
			assert.NoError(t, err, "should have a valid byte buffer")
			assert.Equal(t, expectedBody, string(output))
		})
	}
}

func TestFetchForeignLayerNotFound(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()

	fetcher := &ecrFetcher{}
	mediaType := images.MediaTypeDockerSchema2LayerForeignGzip

	desc := ocispec.Descriptor{
		MediaType: mediaType,
		URLs:      []string{ts.URL},
	}

	_, err := fetcher.Fetch(context.Background(), desc)
	assert.Error(t, err)
	cause := errors.Cause(err)
	assert.Equal(t, errdefs.ErrNotFound, cause)
}

func TestFetchManifest(t *testing.T) {
	imageManifest := `{"schemaVersion": 0}`      // content is unimportant.
	imageDigest := testdata.ImageDigest.String() // digest is unimportant.
	imageTag := testdata.FakeImageTag

	// Test all supported media types
	for _, mediaType := range supportedImageMediaTypes {
		// Test variants of Object (tag, digest, and combination).
		for _, testObject := range []struct {
			ImageIdentifier ecr.ImageIdentifier
			Object          string
		}{
			// Tag alone - used on first get image.
			{Object: imageTag, ImageIdentifier: ecr.ImageIdentifier{ImageTag: aws.String(imageTag)}},
			// Tag and digest assertive fetch
			{Object: imageTag + "@" + imageDigest, ImageIdentifier: ecr.ImageIdentifier{ImageTag: aws.String(imageTag), ImageDigest: aws.String(imageDigest)}},
			// Digest fetch
			{Object: "@" + imageDigest, ImageIdentifier: ecr.ImageIdentifier{ImageDigest: aws.String(imageDigest)}},
		} {
			fakeClient := &fakeECRClient{}
			fetcher := &ecrFetcher{
				ecrBase: ecrBase{
					client: fakeClient,
					ecrSpec: ECRSpec{
						arn: arn.ARN{
							AccountID: testdata.FakeAccountID,
						},
						Repository: testdata.FakeRepository,
						Object:     testObject.Object,
					},
				},
			}

			t.Run(mediaType+"_"+testObject.Object, func(t *testing.T) {
				callCount := 0
				fakeClient.BatchGetImageFn = func(_ aws.Context, input *ecr.BatchGetImageInput, _ ...request.Option) (*ecr.BatchGetImageOutput, error) {
					callCount++
					assert.Equal(t, testdata.FakeRegistryID, aws.StringValue(input.RegistryId))
					assert.Equal(t, testdata.FakeRepository, aws.StringValue(input.RepositoryName))
					assert.Equal(t, []*ecr.ImageIdentifier{&testObject.ImageIdentifier}, input.ImageIds)
					return &ecr.BatchGetImageOutput{Images: []*ecr.Image{{ImageManifest: aws.String(imageManifest)}}}, nil
				}
				desc := ocispec.Descriptor{
					MediaType: mediaType,
				}
				if testObject.ImageIdentifier.ImageDigest != nil {
					desc.Digest = digest.Digest(aws.StringValue(testObject.ImageIdentifier.ImageDigest))
				}

				reader, err := fetcher.Fetch(context.Background(), desc)
				require.NoError(t, err, "fetch")
				defer reader.Close()
				assert.Equal(t, 1, callCount, "BatchGetImage should be called once")
				manifest, err := ioutil.ReadAll(reader)
				require.NoError(t, err, "reading manifest")
				assert.Equal(t, imageManifest, string(manifest))
			})
		}
	}
}

func TestFetchManifestAPIError(t *testing.T) {
	mediaType := ocispec.MediaTypeImageManifest

	fakeClient := &fakeECRClient{
		BatchGetImageFn: func(aws.Context, *ecr.BatchGetImageInput, ...request.Option) (*ecr.BatchGetImageOutput, error) {
			return nil, errors.New("expected")
		},
	}
	resolver := &ecrResolver{
		clients: map[string]ecrAPI{
			testdata.FakeRegion: fakeClient,
		},
	}
	fetcher, err := resolver.Fetcher(context.Background(), testdata.FakeRef+"@"+testdata.ImageDigest.String())
	require.NoError(t, err, "failed to create fetcher")
	_, err = fetcher.Fetch(context.Background(), ocispec.Descriptor{MediaType: mediaType})
	assert.EqualError(t, err, "expected")
}

func TestFetchManifestNotFound(t *testing.T) {
	mediaType := ocispec.MediaTypeImageManifest

	fakeClient := &fakeECRClient{
		BatchGetImageFn: func(aws.Context, *ecr.BatchGetImageInput, ...request.Option) (*ecr.BatchGetImageOutput, error) {
			return &ecr.BatchGetImageOutput{
				Failures: []*ecr.ImageFailure{
					{FailureCode: aws.String(ecr.ImageFailureCodeImageNotFound)},
				},
			}, nil
		},
	}
	resolver := &ecrResolver{
		clients: map[string]ecrAPI{
			testdata.FakeRegion: fakeClient,
		},
	}
	fetcher, err := resolver.Fetcher(context.Background(), testdata.FakeRef+"@"+testdata.ImageDigest.String())
	require.NoError(t, err, "failed to create fetcher")
	_, err = fetcher.Fetch(context.Background(), ocispec.Descriptor{MediaType: mediaType})
	assert.Error(t, err)
}

func TestFetchLayer(t *testing.T) {
	fakeClient := &fakeECRClient{}
	fetcher := &ecrFetcher{
		ecrBase: ecrBase{
			client: fakeClient,
			ecrSpec: ECRSpec{
				arn: arn.ARN{
					AccountID: testdata.FakeAccountID,
				},
				Repository: testdata.FakeRepository,
			},
		},
	}
	expectedBody := "hello this is dog"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, expectedBody)
	}))
	defer ts.Close()

	// test all supported media types
	for _, mediaType := range []string{
		images.MediaTypeDockerSchema2Config,
		images.MediaTypeDockerSchema2Layer,
		images.MediaTypeDockerSchema2LayerGzip,
		ocispec.MediaTypeImageConfig,
		ocispec.MediaTypeImageLayer,
		ocispec.MediaTypeImageLayerGzip,
	} {
		t.Run(mediaType, func(t *testing.T) {
			callCount := 0
			fakeClient.GetDownloadUrlForLayerFn = func(_ aws.Context, input *ecr.GetDownloadUrlForLayerInput, _ ...request.Option) (*ecr.GetDownloadUrlForLayerOutput, error) {
				callCount++
				assert.Equal(t, testdata.FakeRegistryID, aws.StringValue(input.RegistryId))
				assert.Equal(t, testdata.FakeRepository, aws.StringValue(input.RepositoryName))
				return &ecr.GetDownloadUrlForLayerOutput{DownloadUrl: aws.String(ts.URL)}, nil
			}
			desc := ocispec.Descriptor{
				MediaType: mediaType,
			}

			reader, err := fetcher.Fetch(context.Background(), desc)
			assert.NoError(t, err, "fetch")
			defer reader.Close()
			assert.Equal(t, 1, callCount, "GetDownloadURLForLayer should be called once")
			body, err := ioutil.ReadAll(reader)
			assert.NoError(t, err, "reading body")
			assert.Equal(t, expectedBody, string(body))
		})
	}
}

func TestFetchLayerAPIError(t *testing.T) {
	fakeClient := &fakeECRClient{
		GetDownloadUrlForLayerFn: func(aws.Context, *ecr.GetDownloadUrlForLayerInput, ...request.Option) (*ecr.GetDownloadUrlForLayerOutput, error) {
			return nil, errors.New("expected")
		},
	}
	fetcher := &ecrFetcher{
		ecrBase: ecrBase{
			client: fakeClient,
		},
	}
	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
	}
	_, err := fetcher.Fetch(context.Background(), desc)
	assert.Error(t, err)
}

func TestFetchLayerHtcat(t *testing.T) {
	fakeClient := &fakeECRClient{}
	fetcher := &ecrFetcher{
		ecrBase: ecrBase{
			client: fakeClient,
			ecrSpec: ECRSpec{
				arn: arn.ARN{
					AccountID: testdata.FakeAccountID,
				},
				Repository: testdata.FakeRepository,
			},
		},
		parallelism: 2,
	}
	// need >1mb of content for htcat to do parallel requests
	const (
		kB = 1024 * 1
		mB = 1024 * kB
	)
	expectedBody := make([]byte, 30*mB)
	_, err := rand.Read(expectedBody)
	require.NoError(t, err)

	handlerCallCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCallCount++
		http.ServeContent(w, r, "", time.Now(), bytes.NewReader(expectedBody))
	}))
	defer ts.Close()

	downloadURLCallCount := 0
	fakeClient.GetDownloadUrlForLayerFn = func(_ aws.Context, input *ecr.GetDownloadUrlForLayerInput, _ ...request.Option) (*ecr.GetDownloadUrlForLayerOutput, error) {
		downloadURLCallCount++
		assert.Equal(t, testdata.FakeRegistryID, aws.StringValue(input.RegistryId))
		assert.Equal(t, testdata.FakeRepository, aws.StringValue(input.RepositoryName))
		return &ecr.GetDownloadUrlForLayerOutput{DownloadUrl: aws.String(ts.URL)}, nil
	}

	reader, err := fetcher.Fetch(context.Background(), ocispec.Descriptor{
		// testing code path choice, mediaType is unimportant.
		MediaType: images.MediaTypeDockerSchema2LayerGzip,
	})
	assert.Equal(t, 1, downloadURLCallCount, "GetDownloadURLForLayer should be called once")
	if assert.NoError(t, err, "fetch") {
		defer reader.Close()
	}
	if assert.NotNil(t, reader, "no reader") {
		body, err := ioutil.ReadAll(reader)
		assert.NoError(t, err, "reading body")
		assert.Equal(t, expectedBody, body)
	}
	assert.True(t, handlerCallCount > 1, "ServeContent should be called more than once: %d", handlerCallCount)
}
