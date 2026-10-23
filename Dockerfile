##########################################
# STEP 1: build crusoe-csi-driver binary #
##########################################

FROM golang:1.26 AS builder

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
FROM alpine:3.23.7

# Need to get these updates for k8s mount-utils library to work properly
RUN apk upgrade --no-cache && \
    apk add --no-cache e2fsprogs-extra~=1.47.3 && \
    apk add --no-cache blkid~=2.41.6 && \
    apk add --no-cache xfsprogs-extra~=6.17.0 && \
    rm -rf /var/cache/apk/*

COPY --from=builder /build/dist/crusoe-csi-driver /usr/local/go/bin/crusoe-csi-driver

ENTRYPOINT ["/usr/local/go/bin/crusoe-csi-driver"]
