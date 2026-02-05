##########################################
# STEP 1: build crusoe-csi-driver binary #
##########################################

FROM golang:1.23.3 AS builder

ARG CRUSOE_CSI_DRIVER_VERSION
ENV CRUSOE_CSI_DRIVER_VERSION=${CRUSOE_CSI_DRIVER_VERSION}

WORKDIR /build

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN make cross

################################################################
# STEP 2: build a small image and run crusoe-csi-driver binary #
################################################################

# Dockerfile.goreleaser should be kept roughly in sync
FROM ubuntu:24.04

# Need to get these updates for k8s mount-utils library to work properly
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        e2fsprogs \
        nfs-common \
        util-linux \
        xfsprogs && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/dist/crusoe-csi-driver /usr/local/go/bin/crusoe-csi-driver

ENTRYPOINT ["/usr/local/go/bin/crusoe-csi-driver"]
