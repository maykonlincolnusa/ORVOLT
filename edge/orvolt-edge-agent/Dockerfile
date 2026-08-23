FROM rust:1-bookworm AS build
WORKDIR /src
COPY contracts ./contracts
COPY edge/orvolt-edge-agent ./edge/orvolt-edge-agent
RUN cargo build --manifest-path edge/orvolt-edge-agent/Cargo.toml --release --locked

# The runtime image has no shell, so the spool directory is prepared here and
# copied in with the right ownership.
FROM debian:bookworm-slim AS layout
RUN mkdir -p /var/lib/orvolt/spool

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=layout --chown=nonroot:nonroot /var/lib/orvolt /var/lib/orvolt
COPY --from=build /src/edge/orvolt-edge-agent/target/release/orvolt-edge-agent /usr/local/bin/orvolt-edge-agent
USER nonroot:nonroot
# The spool must outlive the process: mount persistent storage here.
ENV SPOOL_DIR=/var/lib/orvolt/spool
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/orvolt-edge-agent"]
