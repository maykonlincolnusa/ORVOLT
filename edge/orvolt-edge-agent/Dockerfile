FROM rust:1.83-bookworm AS build
WORKDIR /src
COPY contracts ./contracts
COPY edge/orvolt-edge-agent ./edge/orvolt-edge-agent
RUN cargo build --manifest-path edge/orvolt-edge-agent/Cargo.toml --release

FROM debian:bookworm-slim
RUN useradd --system --uid 10001 orvolt
COPY --from=build /src/edge/orvolt-edge-agent/target/release/orvolt-edge-agent /usr/local/bin/orvolt-edge-agent
USER orvolt
ENTRYPOINT ["/usr/local/bin/orvolt-edge-agent"]
