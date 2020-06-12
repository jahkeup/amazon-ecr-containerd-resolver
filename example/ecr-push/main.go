/*
 * Copyright 2017-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/awslabs/amazon-ecr-containerd-resolver/ecr"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/progress"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

func main() {
	enableVerbose := flag.Bool("verbose", false, "enable verbose logging")
	flag.Parse()
	if *enableVerbose {
		log.L.Logger.SetLevel(log.TraceLevel)
	}

	ctx := namespaces.NamespaceFromEnv(context.Background())

	if len(flag.Args()) < 1 {
		log.G(ctx).Fatal("Must provide image to push as argument")
	}
	ref := flag.Arg(0)
	local := ""
	if len(flag.Args()) > 2 {
		local = flag.Arg(1)
	} else {
		local = ref
	}

	client, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to connect to containerd")
	}
	defer client.Close()

	tracker := docker.NewInMemoryTracker()
	resolver, err := ecr.NewResolver(ecr.WithTracker(tracker))
	if err != nil {
		log.G(ctx).WithError(err).Fatal("Failed to create resolver")
	}

	img, err := client.ImageService().Get(ctx, local)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	ongoing := newPushJobs(tracker)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		log.G(ctx).WithField("local", local).WithField("ref", ref).Info("Pushing to Amazon ECR")
		desc := img.Target

		jobHandler := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			ongoing.add(remotes.MakeRefKey(ctx, desc))
			return nil, nil
		})

		return client.Push(ctx, ref, desc,
			containerd.WithResolver(resolver),
			containerd.WithImageHandler(jobHandler))

	})
	errs := make(chan error)
	go func() {
		defer close(errs)
		errs <- eg.Wait()
	}()

	err = displayUploadProgress(ctx, ongoing, errs)
	if err != nil {
		log.G(ctx).WithError(err).WithField("ref", ref).Fatal("Failed to push")

	}
	log.G(ctx).WithField("ref", ref).Info("Pushed successfully!")
}

func displayUploadProgress(ctx context.Context, ongoing *pushjobs, errs chan error) error {
	var (
		ticker = time.NewTicker(100 * time.Millisecond)
		fw     = progress.NewWriter(os.Stdout)
		start  = time.Now()
		done   bool
	)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fw.Flush()

			tw := tabwriter.NewWriter(fw, 1, 8, 1, ' ', 0)

			display(tw, ongoing.status(), start)
			tw.Flush()

			if done {
				fw.Flush()
				return nil
			}
		case err := <-errs:
			if err != nil {
				return err
			}
			done = true
		case <-ctx.Done():
			done = true // allow ui to update once more
		}
	}
}
