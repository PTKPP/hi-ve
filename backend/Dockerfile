FROM docker.io/library/golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/api ./cmd/api
COPY internal ./internal
RUN --network=none go build -mod=readonly -trimpath -buildvcs=false -o /out/hive-api ./cmd/api
RUN install -D -m 0644 /etc/ssl/certs/ca-certificates.crt /out/rootfs/etc/ssl/certs/ca-certificates.crt \
    && mv /out/hive-api /out/rootfs/hive-api \
    && find /out/rootfs -exec touch -h -d '@0' {} +

FROM scratch
ARG VCS_REF
LABEL org.opencontainers.image.source="https://github.com/PTKPP/hi-ve" \
      org.opencontainers.image.revision="${VCS_REF}"
COPY --from=build /out/rootfs/ /
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/hive-api"]
