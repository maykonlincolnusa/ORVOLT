FROM golang:1.25-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler && rm -rf /var/lib/apt/lists/*
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
ENV PATH="/go/bin:${PATH}"
WORKDIR /src

# Dependencies first so that a source-only change does not re-download them.
COPY go.mod go.sum ./
RUN go mod download

# Bindings are generated from the canonical contracts rather than committed, so
# a clean checkout can always be container-built. The generation step is shared
# with the other services: listing .proto files per image is how one service
# ends up unable to compile a contract another one already uses.
COPY contracts/proto ./contracts/proto
COPY scripts/generate-go-bindings.sh ./scripts/
RUN sh scripts/generate-go-bindings.sh contracts/gen/go

COPY cloud ./cloud
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/control-plane ./cloud/control-plane/cmd/control-plane

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/control-plane /usr/local/bin/control-plane
COPY cloud/control-plane/migrations ./migrations
ENV MIGRATIONS_DIR=/app/migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/control-plane"]
