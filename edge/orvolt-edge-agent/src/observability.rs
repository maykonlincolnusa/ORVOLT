//! Local health and metrics for a device nobody can SSH into.
//!
//! When a charger misbehaves, the person standing in front of it is a
//! technician with a laptop, not an SRE with a dashboard. The agent therefore
//! serves its own state on a local port in the Prometheus text format: it is
//! readable by a human over `curl`, scrapeable by a site gateway, and needs no
//! client library on the device.

use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tracing::{info, warn};

#[derive(Debug, Default)]
pub struct Metrics {
    pub received: AtomicU64,
    pub rejected: AtomicU64,
    pub published: AtomicU64,
    pub publish_failures: AtomicU64,
    pub spool_pending_bytes: AtomicU64,
    pub spool_dropped: AtomicU64,
    pub spool_corrupt: AtomicU64,
    pub unsynchronized_clock: AtomicU64,
    pub bus_connected: AtomicBool,
    pub broker_connected: AtomicBool,
}

impl Metrics {
    pub fn increment(counter: &AtomicU64) {
        counter.fetch_add(1, Ordering::Relaxed);
    }

    pub fn add(counter: &AtomicU64, amount: u64) {
        counter.fetch_add(amount, Ordering::Relaxed);
    }

    /// True when the agent is doing its job: reading the broker and reaching
    /// the bus. Used as the readiness answer.
    pub fn is_ready(&self) -> bool {
        self.broker_connected.load(Ordering::Relaxed) && self.bus_connected.load(Ordering::Relaxed)
    }

    pub fn render(&self) -> String {
        let counter = |name: &str, help: &str, value: u64| {
            format!("# HELP {name} {help}\n# TYPE {name} counter\n{name} {value}\n")
        };
        let gauge = |name: &str, help: &str, value: u64| {
            format!("# HELP {name} {help}\n# TYPE {name} gauge\n{name} {value}\n")
        };

        [
            counter(
                "orvolt_edge_telemetry_received_total",
                "Telemetry messages read from the local broker.",
                self.received.load(Ordering::Relaxed),
            ),
            counter(
                "orvolt_edge_telemetry_rejected_total",
                "Messages rejected by local validation.",
                self.rejected.load(Ordering::Relaxed),
            ),
            counter(
                "orvolt_edge_telemetry_published_total",
                "Records confirmed by the cloud event bus.",
                self.published.load(Ordering::Relaxed),
            ),
            counter(
                "orvolt_edge_publish_failures_total",
                "Publish attempts refused by the cloud event bus.",
                self.publish_failures.load(Ordering::Relaxed),
            ),
            counter(
                "orvolt_edge_spool_dropped_records_total",
                "Records discarded because the local spool reached its size ceiling.",
                self.spool_dropped.load(Ordering::Relaxed),
            ),
            counter(
                "orvolt_edge_spool_corrupt_records_total",
                "Spool records abandoned after a checksum mismatch.",
                self.spool_corrupt.load(Ordering::Relaxed),
            ),
            counter(
                "orvolt_edge_unsynchronized_clock_total",
                "Observations produced while the device clock was untrusted.",
                self.unsynchronized_clock.load(Ordering::Relaxed),
            ),
            gauge(
                "orvolt_edge_spool_pending_bytes",
                "Bytes waiting in the local spool for delivery.",
                self.spool_pending_bytes.load(Ordering::Relaxed),
            ),
            gauge(
                "orvolt_edge_broker_connected",
                "1 when the local MQTT broker connection is up.",
                u64::from(self.broker_connected.load(Ordering::Relaxed)),
            ),
            gauge(
                "orvolt_edge_bus_connected",
                "1 when the cloud event bus connection is up.",
                u64::from(self.bus_connected.load(Ordering::Relaxed)),
            ),
        ]
        .concat()
    }
}

/// Serves `/health`, `/ready` and `/metrics` until the task is cancelled.
pub async fn serve(address: String, metrics: Arc<Metrics>) {
    let listener = match TcpListener::bind(&address).await {
        Ok(listener) => listener,
        Err(error) => {
            warn!(%error, address = %address, "local observability endpoint is unavailable");
            return;
        }
    };
    info!(address = %address, "edge agent observability endpoint listening");

    loop {
        match listener.accept().await {
            Ok((stream, _)) => {
                let metrics = Arc::clone(&metrics);
                tokio::spawn(async move {
                    if let Err(error) = handle(stream, metrics).await {
                        warn!(%error, "observability request failed");
                    }
                });
            }
            Err(error) => {
                warn!(%error, "accepting an observability connection failed");
            }
        }
    }
}

async fn handle(mut stream: TcpStream, metrics: Arc<Metrics>) -> std::io::Result<()> {
    // A request line is all that is needed. The read is bounded so that a
    // malformed or hostile client cannot make the agent allocate.
    let mut buffer = [0u8; 1024];
    let read = stream.read(&mut buffer).await?;
    let request = String::from_utf8_lossy(&buffer[..read]);
    let path = request
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        .unwrap_or("/");

    let (status, content_type, body) = match path {
        "/metrics" => ("200 OK", "text/plain; version=0.0.4", metrics.render()),
        "/health" => (
            "200 OK",
            "application/json",
            "{\"status\":\"ok\"}\n".to_string(),
        ),
        "/ready" => {
            if metrics.is_ready() {
                (
                    "200 OK",
                    "application/json",
                    "{\"status\":\"ready\"}\n".to_string(),
                )
            } else {
                (
                    "503 Service Unavailable",
                    "application/json",
                    "{\"status\":\"not_ready\"}\n".to_string(),
                )
            }
        }
        _ => (
            "404 Not Found",
            "application/json",
            "{\"error\":\"not found\"}\n".to_string(),
        ),
    };

    let response = format!(
        "HTTP/1.1 {status}\r\nContent-Type: {content_type}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    stream.write_all(response.as_bytes()).await?;
    stream.flush().await
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rendered_metrics_use_the_prometheus_text_format() {
        let metrics = Metrics::default();
        Metrics::add(&metrics.published, 7);
        metrics.broker_connected.store(true, Ordering::Relaxed);

        let rendered = metrics.render();
        assert!(rendered.contains("orvolt_edge_telemetry_published_total 7"));
        assert!(rendered.contains("# TYPE orvolt_edge_telemetry_published_total counter"));
        assert!(rendered.contains("orvolt_edge_broker_connected 1"));
    }

    #[test]
    fn readiness_requires_both_links() {
        let metrics = Metrics::default();
        assert!(!metrics.is_ready());
        metrics.broker_connected.store(true, Ordering::Relaxed);
        assert!(!metrics.is_ready(), "the cloud link is still down");
        metrics.bus_connected.store(true, Ordering::Relaxed);
        assert!(metrics.is_ready());
    }
}
