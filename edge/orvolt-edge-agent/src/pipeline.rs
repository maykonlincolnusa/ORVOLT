//! Moves spooled telemetry to the cloud without losing it.
//!
//! The forwarder is deliberately split from the transport. Everything that
//! decides *whether data can be dropped* lives here and is unit-tested against
//! a sink that fails on demand; the NATS specifics live behind [`TelemetrySink`].

use std::future::Future;
use std::io;

use crate::spool::{Cursor, Spool};

/// A transport failure. Every variant means "not delivered", so the forwarder
/// treats them identically: keep the records and try again later.
#[derive(Debug, thiserror::Error)]
#[error("{0}")]
pub struct SinkError(pub String);

impl SinkError {
    pub fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }
}

/// Publishes a batch of encoded telemetry records.
///
/// The interface is batch-oriented on purpose. Publishing one record and
/// awaiting its acknowledgement before starting the next serialises a network
/// round trip per sample, which is what limited the previous agent to a handful
/// of samples per second over a high-latency link.
pub trait TelemetrySink {
    fn publish(
        &mut self,
        payloads: &[Vec<u8>],
    ) -> impl Future<Output = Result<(), SinkError>> + Send;
}

#[derive(Debug, Default, PartialEq, Eq)]
pub struct DrainOutcome {
    /// Records confirmed by the sink during this drain.
    pub published: usize,
    /// True when the sink refused and records remain spooled.
    pub blocked: bool,
}

#[derive(Debug, thiserror::Error)]
pub enum ForwardError {
    #[error("spool I/O failed: {0}")]
    Spool(#[from] io::Error),
}

pub struct Forwarder<S> {
    spool: Spool,
    sink: S,
    batch_size: usize,
}

impl<S: TelemetrySink> Forwarder<S> {
    pub fn new(spool: Spool, sink: S, batch_size: usize) -> Self {
        Self {
            spool,
            sink,
            batch_size: batch_size.max(1),
        }
    }

    /// Records a payload for delivery. This must never fail for reasons the
    /// cloud controls: it only touches local storage.
    pub fn enqueue(&mut self, payload: &[u8]) -> Result<(), ForwardError> {
        self.spool.append(payload)?;
        Ok(())
    }

    /// Publishes as much of the spool as the sink will accept.
    ///
    /// The cursor advances only after the sink confirms a batch, so a crash or
    /// a refusal replays records rather than losing them. Cloud ingest is
    /// idempotent, which makes the duplicate side of that trade harmless.
    pub async fn drain(&mut self) -> Result<DrainOutcome, ForwardError> {
        let mut outcome = DrainOutcome::default();
        loop {
            let (records, cursor) = self.spool.read_batch(self.batch_size)?;
            if records.is_empty() {
                return Ok(outcome);
            }
            match self.sink.publish(&records).await {
                Ok(()) => {
                    self.commit(cursor, records.len(), &mut outcome)?;
                }
                Err(_) => {
                    outcome.blocked = true;
                    return Ok(outcome);
                }
            }
        }
    }

    fn commit(
        &mut self,
        cursor: Cursor,
        count: usize,
        outcome: &mut DrainOutcome,
    ) -> Result<(), ForwardError> {
        self.spool.commit(cursor)?;
        outcome.published += count;
        Ok(())
    }

    pub fn sink(&self) -> &S {
        &self.sink
    }

    /// Exposed so the caller can drive transport-specific recovery, such as
    /// reconnecting, without the forwarder knowing what a connection is.
    pub fn sink_mut(&mut self) -> &mut S {
        &mut self.sink
    }

    pub fn spool(&self) -> &Spool {
        &self.spool
    }

    pub fn spool_mut(&mut self) -> &mut Spool {
        &mut self.spool
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::spool::SpoolConfig;

    /// A sink that can be told to fail, so that the "link is down" path is
    /// exercised without a broker.
    #[derive(Default)]
    struct RecordingSink {
        accepted: Vec<Vec<u8>>,
        offline: bool,
        calls: usize,
    }

    impl TelemetrySink for RecordingSink {
        async fn publish(&mut self, payloads: &[Vec<u8>]) -> Result<(), SinkError> {
            self.calls += 1;
            if self.offline {
                return Err(SinkError::new("link is down"));
            }
            self.accepted.extend_from_slice(payloads);
            Ok(())
        }
    }

    fn forwarder(directory: &std::path::Path, sink: RecordingSink) -> Forwarder<RecordingSink> {
        let spool = Spool::open(directory, SpoolConfig::default()).expect("open spool");
        Forwarder::new(spool, sink, 4)
    }

    #[tokio::test]
    async fn publishes_everything_when_the_link_is_healthy() {
        let directory = tempdir();
        let mut forwarder = forwarder(&directory, RecordingSink::default());

        for index in 0..10u8 {
            forwarder.enqueue(&[index]).expect("enqueue");
        }
        let outcome = forwarder.drain().await.expect("drain");

        assert_eq!(outcome.published, 10);
        assert!(!outcome.blocked);
        assert_eq!(forwarder.sink.accepted.len(), 10);
        // Batching means fewer round trips than records.
        assert!(forwarder.sink.calls < 10, "expected batched publishes");
    }

    #[tokio::test]
    async fn keeps_everything_when_the_link_is_down() {
        let directory = tempdir();
        let mut forwarder = forwarder(
            &directory,
            RecordingSink {
                offline: true,
                ..Default::default()
            },
        );

        for index in 0..5u8 {
            forwarder.enqueue(&[index]).expect("enqueue");
        }
        let outcome = forwarder.drain().await.expect("drain");

        assert_eq!(outcome.published, 0);
        assert!(outcome.blocked);
        // Nothing was consumed, so the records survive for the next attempt.
        assert!(!forwarder.spool_mut().is_empty().expect("is_empty"));
    }

    #[tokio::test]
    async fn replays_spooled_telemetry_after_the_link_returns() {
        let directory = tempdir();
        let mut forwarder = forwarder(
            &directory,
            RecordingSink {
                offline: true,
                ..Default::default()
            },
        );
        for index in 0..7u8 {
            forwarder.enqueue(&[index]).expect("enqueue");
        }
        assert!(forwarder.drain().await.expect("drain").blocked);

        forwarder.sink.offline = false;
        let outcome = forwarder.drain().await.expect("drain");

        assert_eq!(outcome.published, 7);
        assert!(!outcome.blocked);
        let delivered: Vec<u8> = forwarder
            .sink
            .accepted
            .iter()
            .map(|record| record[0])
            .collect();
        assert_eq!(delivered, vec![0, 1, 2, 3, 4, 5, 6], "order must be FIFO");
    }

    /// The property that matters on a charger: a process restart mid-outage
    /// must not lose the readings that were already recorded.
    #[tokio::test]
    async fn survives_a_restart_while_the_link_is_down() {
        let directory = tempdir();
        {
            let mut forwarder = forwarder(
                &directory,
                RecordingSink {
                    offline: true,
                    ..Default::default()
                },
            );
            for index in 0..6u8 {
                forwarder.enqueue(&[index]).expect("enqueue");
            }
            assert!(forwarder.drain().await.expect("drain").blocked);
        }

        // A brand new process over the same directory.
        let mut restarted = forwarder(&directory, RecordingSink::default());
        let outcome = restarted.drain().await.expect("drain");

        assert_eq!(
            outcome.published, 6,
            "spooled readings must survive a restart"
        );
        let delivered: Vec<u8> = restarted
            .sink
            .accepted
            .iter()
            .map(|record| record[0])
            .collect();
        assert_eq!(delivered, vec![0, 1, 2, 3, 4, 5]);
    }

    #[tokio::test]
    async fn does_not_redeliver_committed_records_after_a_restart() {
        let directory = tempdir();
        {
            let mut forwarder = forwarder(&directory, RecordingSink::default());
            for index in 0..4u8 {
                forwarder.enqueue(&[index]).expect("enqueue");
            }
            assert_eq!(forwarder.drain().await.expect("drain").published, 4);
        }

        let mut restarted = forwarder(&directory, RecordingSink::default());
        let outcome = restarted.drain().await.expect("drain");

        assert_eq!(
            outcome.published, 0,
            "committed records must not be replayed"
        );
    }

    fn tempdir() -> std::path::PathBuf {
        let base = std::env::temp_dir().join(format!(
            "orvolt-spool-test-{}-{:?}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir_all(&base).expect("create temp dir");
        base
    }
}
