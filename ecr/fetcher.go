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
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/remotes"
	"github.com/htcat/htcat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/net/context/ctxhttp"
)

// ecrFetcher implements the containerd remotes.Fetcher interface and can be
// used to pull images from Amazon ECR.
type ecrFetcher struct {
	ecrBase
	parallelism int
}

var _ remotes.Fetcher = (*ecrFetcher)(nil)

func (f *ecrFetcher) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("desc", desc))
	log.G(ctx).Debug("ecr.fetch")

	// need to do different things based on the media type
	switch desc.MediaType {
	case
		ocispec.MediaTypeImageManifest,
		ocispec.MediaTypeImageIndex,
		images.MediaTypeDockerSchema1Manifest,
		images.MediaTypeDockerSchema2Manifest,
		images.MediaTypeDockerSchema2ManifestList:
		return f.fetchManifest(ctx, desc)
	case
		images.MediaTypeDockerSchema2Layer,
		images.MediaTypeDockerSchema2LayerGzip,
		images.MediaTypeDockerSchema2Config,
		ocispec.MediaTypeImageLayerGzip,
		ocispec.MediaTypeImageLayer,
		ocispec.MediaTypeImageConfig:
		return f.fetchLayer(ctx, desc)
	case
		images.MediaTypeDockerSchema2LayerForeign,
		images.MediaTypeDockerSchema2LayerForeignGzip:
		return f.fetchForeignLayer(ctx, desc)
	default:
		log.G(ctx).
			WithField("media type", desc.MediaType).
			Error("ecr.fetcher: unimplemented media type")
		return nil, unimplemented
	}
}

func (f *ecrFetcher) fetchManifest(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	var (
		image *ecr.Image
		err   error
	)
	// Single manifest images will not have a digest for the initial fetch, so
	// its fetched by its ECRSpec reference. Otherwise, all fetches will be made
	// by *exactly* its descriptor's digest.
	if desc.Digest == "" {
		log.G(ctx).Debug("ecr.fetcher.manifest: fetch image by tag")
		image, err = f.getImage(ctx)
	} else {
		log.G(ctx).Debug("ecr.fetcher.manifest: fetch image by digest")
		image, err = f.getImageByDescriptor(ctx, desc)
	}
	if err != nil {
		return nil, err
	}
	if image == nil {
		return nil, errors.New("fetchManifest: nil image")
	}

	return ioutil.NopCloser(bytes.NewReader([]byte(aws.StringValue(image.ImageManifest)))), nil
}

func (f *ecrFetcher) fetchLayer(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	log.G(ctx).Debug("ecr.fetcher.layer")
	getDownloadUrlForLayerInput := &ecr.GetDownloadUrlForLayerInput{
		RegistryId:     aws.String(f.ecrSpec.Registry()),
		RepositoryName: aws.String(f.ecrSpec.Repository),
		LayerDigest:    aws.String(desc.Digest.String()),
	}
	output, err := f.client.GetDownloadUrlForLayerWithContext(ctx, getDownloadUrlForLayerInput)
	if err != nil {
		return nil, err
	}

	downloadURL := aws.StringValue(output.DownloadUrl)
	if f.parallelism > 0 {
		return f.fetchLayerHtcat(ctx, desc, downloadURL)
	}
	return f.fetchLayerURL(ctx, desc, downloadURL)
}

func (f *ecrFetcher) fetchForeignLayer(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	log.G(ctx).Debug("ecr.fetcher.layer.foreign")
	if len(desc.URLs) < 1 {
		log.G(ctx).Error("cannot pull foreign layer without URL")
	}
	// TODO try more than one URL
	return f.fetchLayerURL(ctx, desc, desc.URLs[0])
}

func (f *ecrFetcher) fetchLayerURL(ctx context.Context, desc ocispec.Descriptor, downloadURL string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		log.G(ctx).
			WithError(err).
			WithField("url", downloadURL).
			Error("ecr.fetcher.layer.url: failed to create HTTP request")
		return nil, err
	}
	log.G(ctx).WithField("url", downloadURL).Debug("ecr.fetcher.layer.url")

	req.Header.Set("Accept", strings.Join([]string{desc.MediaType, `*`}, ", "))
	resp, err := f.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode > 299 {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, errors.Wrapf(errdefs.ErrNotFound, "content at %v not found", downloadURL)
		}
		return nil, errors.Errorf("ecr.fetcher.layer.url: unexpected status code %v: %v", downloadURL, resp.Status)
	}
	log.G(ctx).WithField("desc", desc).Debug("ecr.fetcher.layer.url: returning body")
	return resp.Body, nil
}

func (f *ecrFetcher) doRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	// TODO: use configurable http.Client
	client := http.DefaultClient
	resp, err := ctxhttp.Do(ctx, client, req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to do request")
	}
	return resp, nil
}

func (f *ecrFetcher) fetchLayerHtcat(ctx context.Context, desc ocispec.Descriptor, downloadURL string) (io.ReadCloser, error) {
	log.G(ctx).WithField("url", downloadURL).Debug("ecr.fetcher.layer.htcat")
	parsedURL, err := url.Parse(downloadURL)
	if err != nil {
		log.G(ctx).
			WithError(err).
			WithField("url", downloadURL).
			Error("ecr.fetcher.layer.htcat: failed to parse URL")
		return nil, err
	}
	htc := htcat.New(http.DefaultClient, parsedURL, f.parallelism)
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, err := htc.WriteTo(pw)
		if err != nil {
			log.G(ctx).
				WithError(err).
				WithField("url", downloadURL).
				Error("ecr.fetcher.layer.htcat: failed to download layer")
		}
	}()
	return pr, nil
}
