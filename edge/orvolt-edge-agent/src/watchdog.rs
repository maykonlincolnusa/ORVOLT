//! systemd readiness and watchdog integration.
//!
//! On a charger the supervisor is systemd, not an orchestrator. `Restart=always`
//! covers a process that exits; it does not cover a process that is still
//! running but has stopped making progress — a wedged MQTT loop, a spool on a
//! filesystem that went read-only. The hardware watchdog covers that case, but
//! only if the agent actually reports progress.
//!
//! The protocol is a single datagram to the socket named by `NOTIFY_SOCKET`.
//! Implementing it directly keeps the agent free of a libsystemd dependency,
//! which matters because the binary must cross-compile statically for musl.

/// Reports that startup finished and the agent is serving.
pub fn ready() {
    notify("READY=1\n");
}

/// Reports that the agent is still making progress. Must be called more often
/// than the unit's `WatchdogSec`, and only when the agent really is working:
/// a keepalive sent unconditionally defeats the entire mechanism.
pub fn alive() {
    notify("WATCHDOG=1\n");
}

/// Reports that the agent is shutting down, so systemd does not treat the
/// closing socket as a failure.
pub fn stopping() {
    notify("STOPPING=1\n");
}

#[cfg(target_os = "linux")]
fn notify(state: &str) {
    use std::os::unix::net::UnixDatagram;

    let Ok(path) = std::env::var("NOTIFY_SOCKET") else {
        // Not running under systemd, which is the normal case in a container
        // or during development.
        return;
    };
    if path.starts_with('@') {
        // Abstract-namespace sockets are used by user-session units. The agent
        // is a system service, so this is left unsupported rather than
        // half-implemented.
        return;
    }
    if let Ok(socket) = UnixDatagram::unbound() {
        let _ = socket.send_to(state.as_bytes(), path);
    }
}

#[cfg(not(target_os = "linux"))]
fn notify(_state: &str) {
    // systemd is Linux-only. Development hosts simply have no supervisor to
    // talk to.
}

/// The interval at which [`alive`] should be called, derived from the
/// `WATCHDOG_USEC` systemd exports.
pub fn keepalive_interval() -> Option<std::time::Duration> {
    keepalive_from(std::env::var("WATCHDOG_USEC").ok().as_deref())
}

/// Half the deadline is the conventional margin: it tolerates one missed tick
/// without tripping a restart.
///
/// The parsing is separated from the environment so it can be tested as a pure
/// function. Tests that mutate process environment variables interfere with
/// each other under Rust's parallel test runner.
fn keepalive_from(raw: Option<&str>) -> Option<std::time::Duration> {
    let microseconds: u64 = raw?.trim().parse().ok()?;
    if microseconds == 0 {
        return None;
    }
    Some(std::time::Duration::from_micros(microseconds / 2))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[test]
    fn keepalive_is_half_the_deadline() {
        assert_eq!(
            keepalive_from(Some("60000000")),
            Some(Duration::from_secs(30))
        );
    }

    #[test]
    fn no_watchdog_configured_means_no_keepalive() {
        assert_eq!(keepalive_from(None), None);
    }

    #[test]
    fn a_zero_deadline_is_not_a_zero_interval() {
        // A zero interval would busy-loop the notifier.
        assert_eq!(keepalive_from(Some("0")), None);
    }

    #[test]
    fn a_malformed_deadline_disables_the_keepalive() {
        // Reporting health on a value we could not read would be worse than
        // not reporting it at all.
        assert_eq!(keepalive_from(Some("soon")), None);
    }
}
