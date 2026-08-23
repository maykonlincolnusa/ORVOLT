FROM golang:1.23-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler && rm -rf /var/lib/apt/lists/*
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.0
ENV PATH="/go/bin:${PATH}"
WORKDIR /src
COPY go.mod ./
COPY contracts/proto ./contracts/proto
RUN mkdir -p contracts/gen/go && protoc -I contracts/proto --go_out=contracts/gen/go --go_opt=paths=source_relative contracts/proto/orvolt/telemetry/evse/v1/telemetry.proto contracts/proto/orvolt/energy/site/v1/energy_site.proto
RUN go mod download
COPY cloud/control-plane ./cloud/control-plane
RUN go build -o /out/control-plane ./cloud/control-plane/cmd/control-plane

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 orvolt
WORKDIR /app
COPY --from=build /out/control-plane /usr/local/bin/control-plane
COPY cloud/control-plane/migrations ./migrations
ENV MIGRATIONS_DIR=/app/migrations
USER orvolt
ENTRYPOINT ["/usr/local/bin/control-plane"]
