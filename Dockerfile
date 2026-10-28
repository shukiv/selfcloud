# Build a static binary, then copy it into a minimal image that runs as a non-root user.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X selfcloud/internal/version.Version=${VERSION}" \
      -o /out/selfcloud ./cmd/selfcloud \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/selfcloud /usr/local/bin/selfcloud
COPY --from=build --chown=65532:65532 /out/data /data
ENV SELFCLOUD_DATA_DIR=/data \
    SELFCLOUD_LISTEN=:8080
# Keep /data on a local disk, not a network share: the metadata database needs working locks
# and fsync.
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD ["/usr/local/bin/selfcloud", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/selfcloud"]
CMD ["serve"]
