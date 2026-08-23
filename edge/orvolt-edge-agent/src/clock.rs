//! Wall-clock trust and observation ordering.
//!
//! A charger is not a server. It frequently boots with no battery-backed RTC,
//! reports 1970 until NTP converges, and may never converge at all if the site
//! blocks outbound NTP. Telemetry from that window is still worth keeping, but
//! anything that orders or ages records by the device timestamp will be wrong.
//!
//! So the agent does two things: it labels every observation with whether its
//! clock can be trusted, and it stamps a monotonic sequence number that remains
//! meaningful even when the timestamp is not.

use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};

/// 2025-01-01T00:00:00Z. A clock reporting earlier than this has not been set:
/// the firmware in question did not exist before that date.
pub const PLAUSIBLE_EPOCH_MS: i64 = 1_735_689_600_000;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ClockTrust {
    Synchronized,
    Unsynchronized,
}

/// Judges whether a wall-clock reading can be trusted.
///
/// This is deliberately a floor check rather than an NTP query: it needs no
/// privileges, no daemon and no network, and it catches the failure that
/// actually happens in the field, which is a clock that was never set.
pub fn assess(now_ms: i64) -> ClockTrust {
    if now_ms >= PLAUSIBLE_EPOCH_MS {
        ClockTrust::Synchronized
    } else {
        ClockTrust::Unsynchronized
    }
}

/// Monotonic per-process observation counter. It restarts at 1 on each process
/// start, which the cloud can detect as a gap rather than mistaking a restart
/// for continuous operation.
#[derive(Debug, Default)]
pub struct Sequence(AtomicU64);

impl Sequence {
    pub fn new() -> Self {
        Self(AtomicU64::new(0))
    }

    pub fn next(&self) -> u64 {
        self.0.fetch_add(1, Ordering::Relaxed) + 1
    }
}

pub fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|elapsed| elapsed.as_millis() as i64)
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_clock_that_was_never_set_is_not_trusted() {
        // The classic symptom: an embedded board with no RTC reports the epoch.
        assert_eq!(assess(0), ClockTrust::Unsynchronized);
        assert_eq!(assess(1_600_000_000_000), ClockTrust::Unsynchronized);
    }

    #[test]
    fn a_plausible_clock_is_trusted() {
        assert_eq!(assess(PLAUSIBLE_EPOCH_MS), ClockTrust::Synchronized);
        assert_eq!(assess(1_900_000_000_000), ClockTrust::Synchronized);
    }

    #[test]
    fn sequence_starts_at_one_and_increases() {
        let sequence = Sequence::new();
        assert_eq!(sequence.next(), 1);
        assert_eq!(sequence.next(), 2);
        assert_eq!(sequence.next(), 3);
    }
}
