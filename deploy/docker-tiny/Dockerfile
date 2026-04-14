# Urgentry Tiny Mode — single container, SQLite, zero dependencies.
#
# Build:  docker build -t urgentry-tiny -f deploy/docker-tiny/Dockerfile .
# Run:    docker run -p 8080:8080 -v urgentry-data:/data urgentry-tiny
#
# Data persists in /data (SQLite database, blobs, WAL).
# First run auto-creates admin account — check logs for credentials.

FROM golang:1.26-alpine AS builder
WORKDIR /build
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN VERSION="$VERSION" sh ./scripts/build-urgentry.sh --output /build/urgentry

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /build/urgentry /usr/local/bin/urgentry

RUN mkdir -p /data
ENV URGENTRY_DATA_DIR=/data
ENV URGENTRY_HTTP_ADDR=:8080

EXPOSE 8080
VOLUME /data

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
  CMD wget -q -O - http://127.0.0.1:8080/readyz || exit 1

ENTRYPOINT ["urgentry"]
CMD ["serve"]
