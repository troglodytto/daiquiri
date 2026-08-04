# daiquiri: Kubernetes event triage.
#
# The captures are ~16 MB each and live outside the image, so mount the
# directory you want to read and pass a path inside it:
#
#   docker build -t daiquiri .
#   docker run --rm -t -v "$PWD/testdata:/data:ro" daiquiri /data/04-test-a.jsonl
#
# -t keeps stdout a terminal, which is what turns colour on. Drop it (or pipe)
# and the output is plain text with no escape sequences.

FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so a source-only change doesn't refetch the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

ARG VERSION=docker
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/daiquiri ./cmd/triage

FROM alpine:3.21

COPY --from=build /out/daiquiri /usr/local/bin/daiquiri

# Nothing here writes, opens a socket, or needs a home directory.
RUN adduser -D -u 65532 triage
USER triage
WORKDIR /data

ENTRYPOINT ["/usr/local/bin/daiquiri"]
