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
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reference"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	errImageNotFound = errors.New("ecr: image not found")
)

type ecrBase struct {
	client  ecrAPI
	ecrSpec ECRSpec
}

// ecrAPI contains only the ECR APIs that are called by the resolver.
//
// See https://docs.aws.amazon.com/sdk-for-go/api/service/ecr/ecriface/ for the
// full interface from the SDK.
type ecrAPI interface {
	BatchGetImageWithContext(aws.Context, *ecr.BatchGetImageInput, ...request.Option) (*ecr.BatchGetImageOutput, error)
	GetDownloadUrlForLayerWithContext(aws.Context, *ecr.GetDownloadUrlForLayerInput, ...request.Option) (*ecr.GetDownloadUrlForLayerOutput, error)
	BatchCheckLayerAvailabilityWithContext(aws.Context, *ecr.BatchCheckLayerAvailabilityInput, ...request.Option) (*ecr.BatchCheckLayerAvailabilityOutput, error)
	InitiateLayerUpload(*ecr.InitiateLayerUploadInput) (*ecr.InitiateLayerUploadOutput, error)
	UploadLayerPart(*ecr.UploadLayerPartInput) (*ecr.UploadLayerPartOutput, error)
	CompleteLayerUpload(*ecr.CompleteLayerUploadInput) (*ecr.CompleteLayerUploadOutput, error)
	PutImageWithContext(aws.Context, *ecr.PutImageInput, ...request.Option) (*ecr.PutImageOutput, error)
}

// getImageByDescriptor retrieves an image from ECR for a given OCI descriptor.
func (b *ecrBase) getImageByDescriptor(ctx context.Context, desc ocispec.Descriptor) (*ecr.Image, error) {
	input := ecr.BatchGetImageInput{
		ImageIds: []*ecr.ImageIdentifier{
			&ecr.ImageIdentifier{ImageDigest: aws.String(desc.Digest.String())},
		},
	}
	if desc.MediaType != "" {
		input.AcceptedMediaTypes = []*string{aws.String(desc.MediaType)}

	}

	imgs, err := b.runGetImage(ctx, input)
	if err != nil {
		return nil, err
	}
	return imgs[0], nil
}

func (b *ecrBase) getImage(ctx context.Context) (*ecr.Image, error) {
	imgs, err := b.runGetImage(ctx, ecr.BatchGetImageInput{
		ImageIds: []*ecr.ImageIdentifier{b.ecrSpec.ImageID()},
	})
	if err != nil {
		return nil, err
	}
	return imgs[0], nil
}

// runGetImage submits and handles the response for requests of ECR images.
func (b *ecrBase) runGetImage(ctx context.Context, batchGetImageInput ecr.BatchGetImageInput) ([]*ecr.Image, error) {
	batchGetImageInput.RegistryId = aws.String(b.ecrSpec.Registry())
	batchGetImageInput.RepositoryName = aws.String(b.ecrSpec.Repository)

	log.G(ctx).WithField("batchGetImageInput", batchGetImageInput).Trace("ecr.base.image")

	batchGetImageOutput, err := b.client.BatchGetImageWithContext(ctx, &batchGetImageInput)
	if err != nil {
		log.G(ctx).WithError(err).Error("ecr.base.image: failed to get image")
		fmt.Println(err)
		return nil, err
	}
	log.G(ctx).WithField("batchGetImageOutput", batchGetImageOutput).Trace("ecr.base.image")

	if len(batchGetImageOutput.Images) != 1 {
		for _, failure := range batchGetImageOutput.Failures {
			switch aws.StringValue(failure.FailureCode) {
			case ecr.ImageFailureCodeImageTagDoesNotMatchDigest:
				log.G(ctx).WithField("failure", failure).Debug("ecr.base.image: no matching image with specified digest")
				return nil, errImageNotFound
			case ecr.ImageFailureCodeImageNotFound:
				log.G(ctx).WithField("failure", failure).Debug("ecr.base.image: no image found")
				return nil, errImageNotFound
			}
		}

		return nil, reference.ErrInvalid
	}

	return batchGetImageOutput.Images, nil
}
