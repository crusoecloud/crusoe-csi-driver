##########################################
# STEP 1: build crusoe-csi-driver binary #
##########################################

FROM golang:1.21 as builder

ARG CI_COMMIT_REF_NAME
ENV CI_COMMIT_REF_NAME=$CI_COMMIT_REF_NAME
ARG CI_PROJECT_NAME
ENV CI_PROJECT_NAME=$CI_PROJECT_NAME

WORKDIR /build
COPY . .

RUN make cross

################################################################
# STEP 2: build a small image and run crusoe-csi-driver binary #
################################################################
FROM alpine

COPY --from=builder /build/dist/crusoe-csi-driver /usr/local/go/bin/crusoe-csi-driver

ENTRYPOINT ["/usr/local/go/bin/crusoe-csi-driver"]
