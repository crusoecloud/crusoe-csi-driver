##########################################
# STEP 1: build crusoe-csi-driver binary #
##########################################

FROM golang:1.22 AS builder

ARG CRUSOE_CSI_DRIVER_NAME
ENV CRUSOE_CSI_DRIVER_NAME=$CRUSOE_CSI_DRIVER_NAME
ARG CRUSOE_CSI_DRIVER_VERSION
ENV CRUSOE_CSI_DRIVER_VERSION=$CRUSOE_CSI_DRIVER_VERSION

WORKDIR /build
COPY . .

RUN make cross

################################################################
# STEP 2: build a small image and run crusoe-csi-driver binary #
################################################################
FROM alpine


# Need to get these updates for k8s mount-utils library to work properly
RUN apk update && \
    apk add --no-cache e2fsprogs && \
    apk add --no-cache blkid && \
    rm -rf /var/cache/apk/*

COPY --from=builder /build/dist/crusoe-csi-driver /usr/local/go/bin/crusoe-csi-driver

USER 1000

ENTRYPOINT ["/usr/local/go/bin/crusoe-csi-driver"]
