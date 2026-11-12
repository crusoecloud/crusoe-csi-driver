# Dockerfile.goreleaser should be kept roughly in sync
FROM alpine:3.23.7

ARG TARGETOS
ARG TARGETARCH

# Need to get these updates for k8s mount-utils library to work properly
RUN apk upgrade --no-cache && \
    apk add --no-cache e2fsprogs-extra~=1.47.3 && \
    apk add --no-cache blkid~=2.41.6 && \
    apk add --no-cache xfsprogs-extra~=6.17.0 && \
    rm -rf /var/cache/apk/*

COPY --chmod=0755 bin/${TARGETOS}-${TARGETARCH}/crusoe-csi-driver /usr/local/go/bin/crusoe-csi-driver

ENTRYPOINT ["/usr/local/go/bin/crusoe-csi-driver"]
