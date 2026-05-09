REGISTRY ?= ghcr.io/jfroy
IMAGE_NAME ?= zfs-static-csi
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE ?= $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
GO ?= go
GOFLAGS ?=

.PHONY: all build test vet tidy image push clean

all: build

build:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath \
		-ldflags="-s -w -X main.version=$(VERSION)" \
		-o bin/zfs-static-csi ./cmd/zfs-static-csi

test:
	$(GO) test $(GOFLAGS) ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

push: image
	docker push $(IMAGE)

clean:
	rm -rf bin/
