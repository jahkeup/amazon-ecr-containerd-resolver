# Copyright 2017 Amazon.com, Inc. or its affiliates. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License"). You
# may not use this file except in compliance with the License. A copy of
# the License is located at
#
# 	http://aws.amazon.com/apache2.0/
#
# or in the "license" file accompanying this file. This file is
# distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF
# ANY KIND, either express or implied. See the License for the specific
# language governing permissions and limitations under the License.

ROOT := $(shell pwd -P)

all: build

SOURCEDIR=./
SOURCES := $(shell find $(SOURCEDIR) -name '*.go' | grep -v './vendor')
PULLDIR=$(SOURCEDIR)/example/ecr-pull
PULL_BINARY=$(ROOT)/bin/ecr-pull
PUSHDIR=$(SOURCEDIR)/example/ecr-push
PUSH_BINARY=$(ROOT)/bin/ecr-push
COPYDIR=$(SOURCEDIR)/example/ecr-copy
COPY_BINARY=$(ROOT)/bin/ecr-copy

export GO111MODULE=on

.PHONY: build
build: $(PULL_BINARY) $(PUSH_BINARY) $(COPY_BINARY)

$(PULL_BINARY): $(SOURCES)
	cd $(PULLDIR) && go build -o $(PULL_BINARY) .

$(PUSH_BINARY): $(SOURCES)
	cd $(PUSHDIR) && go build -o $(PUSH_BINARY) .

$(COPY_BINARY): $(SOURCES)
	cd $(COPYDIR) && go build -o $(COPY_BINARY) .

.PHONY: test
test: $(SOURCES)
	go test -v $(shell go list ./... | grep -v '/vendor/')


.PHONY: clean
clean:
	@rm $(PULL_BINARY) ||:
	@rm $(PUSH_BINARY) ||:

# Tidy go modules
tidy: TIDY=go mod tidy -v
tidy:
	$(TIDY)
	cd example ; \
	$(TIDY)
