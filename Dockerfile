# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /build
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN VERSION="$VERSION" bash ./scripts/build-urgentry.sh --output /build/urgentry

# Runtime stage
FROM alpine:3.21

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Urgentry" \
      org.opencontainers.image.description="Lightweight, self-hosted error tracking" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/urgentry-project/urgentry" \
      org.opencontainers.image.vendor="Urgentry"

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /build/urgentry /usr/local/bin/urgentry
RUN mkdir -p /data
ENV URGENTRY_DATA_DIR=/data
ENV URGENTRY_HTTP_ADDR=:8080
EXPOSE 8080
VOLUME /data
ENTRYPOINT ["urgentry"]
CMD ["serve", "--role=all"]
