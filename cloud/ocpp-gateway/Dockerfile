FROM golang:1.25-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler && rm -rf /var/lib/apt/lists/*
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
ENV PATH="/go/bin:${PATH}"
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY contracts/proto ./contracts/proto
COPY scripts/generate-go-bindings.sh ./scripts/
RUN sh scripts/generate-go-bindings.sh contracts/gen/go

COPY cloud ./cloud
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ocpp-gateway ./cloud/ocpp-gateway/cmd/ocpp-gateway

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ocpp-gateway /usr/local/bin/ocpp-gateway
USER nonroot:nonroot
# 8887 is the conventional OCPP 1.6J WebSocket port; 8081 serves health.
EXPOSE 8887 8081
ENTRYPOINT ["/usr/local/bin/ocpp-gateway"]
