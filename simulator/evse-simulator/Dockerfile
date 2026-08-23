FROM gcc:14-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends cmake git ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY simulator/evse-simulator ./simulator/evse-simulator
RUN cmake -S simulator/evse-simulator -B /build -DBUILD_TESTING=OFF && cmake --build /build --parallel

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libstdc++6 && rm -rf /var/lib/apt/lists/*
COPY --from=build /build/evse-simulator /usr/local/bin/evse-simulator
ENTRYPOINT ["/usr/local/bin/evse-simulator"]
