use std::time::Duration;

use anyhow::Context;
use async_nats::jetstream;
use clap::Parser;
use orvolt_edge_agent::{encode, normalize, parse_payload};
use rumqttc::{AsyncClient, Event, Incoming, MqttOptions, QoS};
use tokio::time::sleep;
use tracing::{error, info, warn};
use tracing_subscriber::EnvFilter;

#[derive(Debug, Parser)]
#[command(
    name = "orvolt-edge-agent",
    about = "ORVOLT MQTT to NATS telemetry bridge"
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
    #[arg(long, env = "EDGE_ID", default_value = "edge-dev-001")]
    edge_id: String,
    #[arg(long, env = "SITE_ID", default_value = "site-dev-001")]
    site_id: String,
}

fn now_ms() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64
}

async fn connect_nats(url: &str) -> jetstream::Context {
    loop {
        match async_nats::connect(url).await {
            Ok(client) => {
                info!(nats_url = url, "connected to NATS");
                return jetstream::new(client);
            }
            Err(error) => {
                warn!(%error, nats_url = url, "NATS unavailable; retrying");
                sleep(Duration::from_secs(2)).await;
            }
        }
    }
}

async fn publish(
    stream: &jetstream::Context,
    subject: &str,
    payload: Vec<u8>,
) -> anyhow::Result<()> {
    stream
        .publish(subject.to_owned(), payload.into())
        .await
        .context("publishing telemetry to JetStream")?
        .await
        .context("waiting for JetStream publish acknowledgement")?;
    Ok(())
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .json()
        .with_env_filter(EnvFilter::from_default_env().add_directive(tracing::Level::INFO.into()))
        .init();
    let config = Config::parse();
    let mut stream = connect_nats(&config.nats_url).await;

    loop {
        let mut mqtt_options =
            MqttOptions::new(&config.edge_id, &config.mqtt_host, config.mqtt_port);
        mqtt_options.set_keep_alive(Duration::from_secs(15));
        mqtt_options.set_clean_session(false);
        let (client, mut event_loop) = AsyncClient::new(mqtt_options, 100);
        client
            .subscribe(config.mqtt_topic.clone(), QoS::AtLeastOnce)
            .await
            .context("subscribing to MQTT telemetry")?;
        info!(topic = %config.mqtt_topic, "edge agent awaiting MQTT telemetry");

        loop {
            tokio::select! {
                signal = tokio::signal::ctrl_c() => {
                    signal.context("waiting for shutdown signal")?;
                    info!("edge agent shutting down");
                    return Ok(());
                }
                event = event_loop.poll() => match event {
                    Ok(Event::Incoming(Incoming::Publish(message))) => {
                        let raw = match parse_payload(&message.payload) {
                            Ok(raw) => raw,
                            Err(error) => {
                                warn!(%error, bytes = message.payload.len(), "rejected malformed MQTT telemetry");
                                continue;
                            }
                        };
                        let telemetry = match normalize(raw, &config.edge_id, &config.site_id, now_ms()) {
                            Ok(telemetry) => telemetry,
                            Err(error) => {
                                warn!(%error, "rejected invalid MQTT telemetry");
                                continue;
                            }
                        };
                        let station_id = telemetry.station_id.clone();
                        if let Err(error) = publish(&stream, &config.nats_subject, encode(&telemetry)).await {
                            warn!(%error, "NATS publish failed; reconnecting before continuing");
                            stream = connect_nats(&config.nats_url).await;
                            continue;
                        }
                        info!(station_id = %station_id, subject = %config.nats_subject, "published normalized telemetry");
                    }
                    Ok(_) => {}
                    Err(error) => {
                        error!(%error, "MQTT connection failed; recreating client");
                        sleep(Duration::from_secs(2)).await;
                        break;
                    }
                }
            }
        }
    }
}
