use std::path::PathBuf;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use anyhow::Context as _;
use async_nats::jetstream;
use clap::Parser;
use orvolt_edge_agent::clock::{self, ClockTrust, Sequence};
use orvolt_edge_agent::observability::{self, Metrics};
use orvolt_edge_agent::pipeline::{Forwarder, SinkError, TelemetrySink};
use orvolt_edge_agent::spool::{Spool, SpoolConfig};
use orvolt_edge_agent::subject;
use orvolt_edge_agent::watchdog;
use orvolt_edge_agent::{encode, normalize, parse_payload, EdgeContext};
use rumqttc::{AsyncClient, Event, Incoming, MqttOptions, QoS};
use tokio::sync::mpsc;
use tokio::time::{interval, sleep};
use tracing::{error, info, warn};
use tracing_subscriber::EnvFilter;

#[derive(Debug, Parser)]
#[command(
    name = "orvolt-edge-agent",
    about = "ORVOLT site edge agent: validates local EVSE telemetry and guarantees its delivery"
)]
struct Config {
    #[arg(long, env = "MQTT_HOST", default_value = "localhost")]
    mqtt_host: String,
    #[arg(long, env = "MQTT_PORT", default_value_t = 1883)]
    mqtt_port: u16,
    #[arg(
        long,
        env = "MQTT_TOPIC",
        default_value = "orvolt/simulators/evse/telemetry"
    )]
    mqtt_topic: String,
    #[arg(long, env = "NATS_URL", default_value = "nats://localhost:4222")]
    nats_url: String,
    #[arg(long, env = "NATS_SUBJECT", default_value = "orvolt.telemetry.evse.v1")]
    nats_subject: String,
    /// NATS credentials file holding this device's JWT and seed. Without it the
    /// agent connects anonymously, which is only acceptable on an isolated
    /// development network.
    #[arg(long, env = "NATS_CREDENTIALS")]
    nats_credentials: Option<PathBuf>,
    #[arg(long, env = "EDGE_ID", default_value = "edge-dev-001")]
    edge_id: String,
    #[arg(long, env = "SITE_ID", default_value = "site-dev-001")]
    site_id: String,

    /// Where undelivered telemetry is buffered. On a real charger this must be
    /// on persistent storage that survives a power cut.
    #[arg(long, env = "SPOOL_DIR", default_value = "spool")]
    spool_dir: PathBuf,
    #[arg(long, env = "SPOOL_SEGMENT_BYTES", default_value_t = 4 * 1024 * 1024)]
    spool_segment_bytes: u64,
    #[arg(long, env = "SPOOL_MAX_BYTES", default_value_t = 256 * 1024 * 1024)]
    spool_max_bytes: u64,

    /// Records published per round trip to the event bus.
    #[arg(long, env = "PUBLISH_BATCH_SIZE", default_value_t = 64)]
    publish_batch_size: usize,
    #[arg(long, env = "FLUSH_INTERVAL_MS", default_value_t = 500)]
    flush_interval_ms: u64,

    #[arg(long, env = "OBSERVABILITY_ADDR", default_value = "0.0.0.0:9090")]
    observability_addr: String,
}

/// Publishes canonical telemetry to JetStream.
///
/// Publishes are pipelined: every record in a batch is sent before any
/// acknowledgement is awaited. Awaiting each acknowledgement in turn, as the
/// first version did, costs one network round trip per sample and collapses on
/// a high-latency site link.
struct NatsSink {
    url: String,
    credentials: Option<PathBuf>,
    subject: String,
    context: Option<jetstream::Context>,
    metrics: Arc<Metrics>,
}

impl NatsSink {
    fn new(
        url: String,
        credentials: Option<PathBuf>,
        subject: String,
        metrics: Arc<Metrics>,
    ) -> Self {
        Self {
            url,
            credentials,
            subject,
            context: None,
            metrics,
        }
    }

    /// Attempts to establish the connection. Failure is normal and is reported
    /// to the caller as "still offline" rather than as a fatal error.
    async fn connect(&mut self) -> bool {
        if self.context.is_some() {
            return true;
        }
        let connection = match &self.credentials {
            Some(path) => async_nats::ConnectOptions::with_credentials_file(path.clone())
                .await
                .map(|options| options.name("orvolt-edge-agent")),
            None => Ok(async_nats::ConnectOptions::new().name("orvolt-edge-agent")),
        };
        let options = match connection {
            Ok(options) => options,
            Err(error) => {
                warn!(%error, "NATS credentials could not be loaded");
                return false;
            }
        };
        match options.connect(&self.url).await {
            Ok(client) => {
                info!(url = %self.url, authenticated = self.credentials.is_some(), "connected to NATS");
                self.context = Some(jetstream::new(client));
                self.metrics.bus_connected.store(true, Ordering::Relaxed);
                true
            }
            Err(error) => {
                warn!(%error, url = %self.url, "NATS unavailable; telemetry stays spooled");
                self.metrics.bus_connected.store(false, Ordering::Relaxed);
                false
            }
        }
    }

    fn mark_offline(&mut self) {
        self.context = None;
        self.metrics.bus_connected.store(false, Ordering::Relaxed);
    }
}

impl TelemetrySink for NatsSink {
    async fn publish(&mut self, payloads: &[Vec<u8>]) -> Result<(), SinkError> {
        let Some(context) = self.context.clone() else {
            return Err(SinkError::new("not connected to the event bus"));
        };

        let mut acknowledgements = Vec::with_capacity(payloads.len());
        for payload in payloads {
            match context
                .publish(self.subject.clone(), payload.clone().into())
                .await
            {
                Ok(acknowledgement) => acknowledgements.push(acknowledgement),
                Err(error) => {
                    self.mark_offline();
                    Metrics::increment(&self.metrics.publish_failures);
                    return Err(SinkError::new(error.to_string()));
                }
            }
        }
        for acknowledgement in acknowledgements {
            if let Err(error) = acknowledgement.await {
                self.mark_offline();
                Metrics::increment(&self.metrics.publish_failures);
                return Err(SinkError::new(error.to_string()));
            }
        }
        Ok(())
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(EnvFilter::from_default_env().add_directive(tracing::Level::INFO.into()))
        .init();

    let config = Config::parse();
    let metrics = Arc::new(Metrics::default());
    let sequence = Sequence::new();

    let spool = Spool::open(
        &config.spool_dir,
        SpoolConfig {
            segment_bytes: config.spool_segment_bytes,
            max_total_bytes: config.spool_max_bytes,
        },
    )
    .with_context(|| format!("opening the telemetry spool at {:?}", config.spool_dir))?;
    info!(directory = ?config.spool_dir, "telemetry spool ready");

    // Publish under this device's own identity. The control plane compares the
    // subject the broker authenticated against the identity inside the payload,
    // which is what stops one station reporting as another.
    let subject = subject::device_subject(&config.nats_subject, &config.edge_id)
        .with_context(|| format!("EDGE_ID {:?} cannot address a subject", config.edge_id))?;
    info!(subject = %subject, "publishing under the device identity");

    let sink = NatsSink::new(
        config.nats_url.clone(),
        config.nats_credentials.clone(),
        subject,
        Arc::clone(&metrics),
    );
    let mut forwarder = Forwarder::new(spool, sink, config.publish_batch_size);
    // Connect eagerly so a healthy site starts delivering immediately, but do
    // not treat a failure as fatal: telemetry spools until the link returns.
    forwarder.sink_mut().connect().await;

    tokio::spawn(observability::serve(
        config.observability_addr.clone(),
        Arc::clone(&metrics),
    ));

    let mut flush = interval(Duration::from_millis(config.flush_interval_ms.max(50)));

    // Tell systemd startup finished. Until this arrives a Type=notify unit is
    // considered still starting, and dependent units keep waiting.
    watchdog::ready();
    let keepalive = watchdog::keepalive_interval();
    if let Some(period) = keepalive {
        info!(
            period_ms = period.as_millis() as u64,
            "systemd watchdog active"
        );
    }

    // The broker is read on its own task and its payloads arrive over a
    // channel.
    //
    // rumqttc's EventLoop::poll is not cancellation-safe: dropping the future
    // mid-flight discards connection state and received packets. Selecting on
    // it alongside a timer means the timer cancels it on every tick, which
    // silently loses telemetry. A channel receive is cancellation-safe, so the
    // select below is free to fire as often as it likes.
    let (sender, mut receiver) = mpsc::channel::<Vec<u8>>(1024);
    tokio::spawn(read_broker(
        BrokerConfig {
            client_id: config.edge_id.clone(),
            host: config.mqtt_host.clone(),
            port: config.mqtt_port,
            topic: config.mqtt_topic.clone(),
        },
        sender,
        Arc::clone(&metrics),
    ));

    // Containers are stopped with SIGTERM, not SIGINT. Handling only the latter
    // would mean the spool is never flushed on an ordinary shutdown.
    let mut shutdown = shutdown_signals()?;

    loop {
        tokio::select! {
            _ = shutdown.recv() => {
                watchdog::stopping();
                info!("edge agent shutting down; flushing spool");
                drain(&mut forwarder, &metrics).await;
                return Ok(());
            }
            _ = flush.tick() => {
                drain(&mut forwarder, &metrics).await;
                // The keepalive is sent from inside the working loop and only
                // after a drain attempt completed. A ping emitted by an
                // independent timer would keep reporting health for a process
                // that had stopped doing anything.
                if keepalive.is_some() {
                    watchdog::alive();
                }
            }
            payload = receiver.recv() => match payload {
                Some(raw) => {
                    Metrics::increment(&metrics.received);
                    if let Some(encoded) = accept(&raw, &config, &sequence, &metrics) {
                        // Enqueuing only touches local storage, so a cloud
                        // outage can never cause a reading to be lost here.
                        if let Err(error) = forwarder.enqueue(&encoded) {
                            error!(%error, "spooling telemetry failed");
                        }
                    }
                }
                None => {
                    error!("broker reader stopped; flushing spool and exiting");
                    drain(&mut forwarder, &metrics).await;
                    return Err(anyhow::anyhow!("MQTT reader terminated"));
                }
            },
        }
    }
}

struct BrokerConfig {
    client_id: String,
    host: String,
    port: u16,
    topic: String,
}

/// Reads the site broker forever, reconnecting on failure.
///
/// This runs uninterrupted on its own task precisely so that nothing can cancel
/// `poll()` between the broker delivering a packet and the agent recording it.
async fn read_broker(config: BrokerConfig, sender: mpsc::Sender<Vec<u8>>, metrics: Arc<Metrics>) {
    loop {
        let mut options = MqttOptions::new(&config.client_id, &config.host, config.port);
        options.set_keep_alive(Duration::from_secs(15));
        // A persistent session lets the broker hold QoS 1 messages for this
        // agent while it is restarting.
        options.set_clean_session(false);
        let (client, mut event_loop) = AsyncClient::new(options, 100);

        if let Err(error) = client
            .subscribe(config.topic.clone(), QoS::AtLeastOnce)
            .await
        {
            warn!(%error, "subscribing to MQTT telemetry failed; retrying");
            sleep(Duration::from_secs(2)).await;
            continue;
        }
        info!(topic = %config.topic, "edge agent awaiting MQTT telemetry");

        loop {
            match event_loop.poll().await {
                Ok(Event::Incoming(Incoming::ConnAck(_))) => {
                    metrics.broker_connected.store(true, Ordering::Relaxed);
                }
                Ok(Event::Incoming(Incoming::Publish(message))) => {
                    // A full channel blocks this task rather than dropping the
                    // reading. The broker then holds the backlog, which is
                    // exactly what a persistent QoS 1 session is for.
                    if sender.send(message.payload.to_vec()).await.is_err() {
                        return;
                    }
                }
                Ok(_) => {}
                Err(error) => {
                    metrics.broker_connected.store(false, Ordering::Relaxed);
                    error!(%error, "MQTT connection failed; recreating client");
                    sleep(Duration::from_secs(2)).await;
                    break;
                }
            }
        }
    }
}

/// A stream that yields once for either termination signal.
struct Shutdown {
    #[cfg(unix)]
    terminate: tokio::signal::unix::Signal,
    #[cfg(unix)]
    interrupt: tokio::signal::unix::Signal,
}

impl Shutdown {
    async fn recv(&mut self) {
        #[cfg(unix)]
        {
            tokio::select! {
                _ = self.terminate.recv() => {}
                _ = self.interrupt.recv() => {}
            }
        }
        #[cfg(not(unix))]
        {
            let _ = tokio::signal::ctrl_c().await;
        }
    }
}

fn shutdown_signals() -> anyhow::Result<Shutdown> {
    #[cfg(unix)]
    {
        use tokio::signal::unix::{signal, SignalKind};
        Ok(Shutdown {
            terminate: signal(SignalKind::terminate()).context("registering SIGTERM handler")?,
            interrupt: signal(SignalKind::interrupt()).context("registering SIGINT handler")?,
        })
    }
    #[cfg(not(unix))]
    {
        Ok(Shutdown {})
    }
}

/// Validates one broker payload and returns the encoded canonical event.
fn accept(
    payload: &[u8],
    config: &Config,
    sequence: &Sequence,
    metrics: &Arc<Metrics>,
) -> Option<Vec<u8>> {
    let raw = match parse_payload(payload) {
        Ok(raw) => raw,
        Err(error) => {
            Metrics::increment(&metrics.rejected);
            warn!(%error, bytes = payload.len(), "rejected malformed MQTT telemetry");
            return None;
        }
    };

    let received_at_ms = clock::now_ms();
    let trust = clock::assess(received_at_ms);
    if trust == ClockTrust::Unsynchronized {
        Metrics::increment(&metrics.unsynchronized_clock);
    }

    let context = EdgeContext {
        edge_id: &config.edge_id,
        site_id: &config.site_id,
        received_at_ms,
        sequence: sequence.next(),
        clock: trust,
    };
    match normalize(raw, context) {
        Ok(telemetry) => Some(encode(&telemetry)),
        Err(error) => {
            Metrics::increment(&metrics.rejected);
            warn!(%error, "rejected invalid MQTT telemetry");
            None
        }
    }
}

/// Publishes whatever the spool holds, reconnecting first if necessary.
async fn drain(forwarder: &mut Forwarder<NatsSink>, metrics: &Arc<Metrics>) {
    match forwarder.drain().await {
        Ok(outcome) => {
            if outcome.published > 0 {
                Metrics::add(&metrics.published, outcome.published as u64);
                info!(published = outcome.published, "delivered spooled telemetry");
            }
            if outcome.blocked {
                // The sink refused. Reconnecting is the only recovery, and the
                // records stay on disk until it succeeds.
                forwarder.sink_mut().connect().await;
            }
        }
        Err(error) => error!(%error, "draining the spool failed"),
    }

    let spool = forwarder.spool();
    metrics
        .spool_pending_bytes
        .store(spool.pending_bytes(), Ordering::Relaxed);
    metrics
        .spool_dropped
        .store(spool.dropped_records(), Ordering::Relaxed);
    metrics
        .spool_corrupt
        .store(spool.corrupt_records(), Ordering::Relaxed);
}
