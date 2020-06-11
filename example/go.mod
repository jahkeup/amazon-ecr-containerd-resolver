module github.com/awslabs/amazon-ecr-containerd-resolver/example

go 1.12

require (
	github.com/awslabs/amazon-ecr-containerd-resolver v0.0.0
	github.com/containerd/console v1.0.0 // indirect
	github.com/containerd/containerd v1.2.7
	github.com/docker/go-units v0.4.0
	github.com/opencontainers/go-digest v0.0.0-20190228220655-ac19fd6e7483
	github.com/opencontainers/image-spec v0.0.0-20190321123305-da296dcb1e47
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
)

replace github.com/awslabs/amazon-ecr-containerd-resolver => ../
