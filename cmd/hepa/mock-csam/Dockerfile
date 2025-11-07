# Run this dockerfile from the mock-csam directory like:
#
#   docker build -t mock-csam .

### Compile stage
FROM golang:1.24-alpine3.22 AS build-env

WORKDIR /app

# Cache Go module dependencies separately for better layer caching
COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy source code and build
COPY . .

# timezone data for alpine builds
ENV GOEXPERIMENT=loopvar

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags timetzdata -o /mock-csam main.go

### Run stage
FROM alpine:3.22

RUN apk add --no-cache --update dumb-init ca-certificates
ENTRYPOINT ["dumb-init", "--"]

WORKDIR /app

COPY --from=build-env /mock-csam ./mock-csam

# small things to make golang binaries work well under alpine
ENV GODEBUG=netdns=go
ENV TZ=Etc/UTC

EXPOSE 8080

CMD ["./mock-csam"]

LABEL org.opencontainers.image.source=https://github.com/bluesky-social/indigo
LABEL org.opencontainers.image.description="Mock CSAM Service for Testing (indigo edition)"
LABEL org.opencontainers.image.licenses=MIT
