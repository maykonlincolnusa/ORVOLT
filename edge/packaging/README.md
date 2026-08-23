# Deploying the edge agent on a charger

This directory holds what a charger actually needs. Note what is *not* here: no
container runtime, no orchestrator, no cluster agent.

## Why there is no Kubernetes here

A charger controller is typically an i.MX8 or CM4 class board with 1–2 GB of RAM
running a Yocto or Buildroot image, connected through an LTE modem that drops
regularly. Kubernetes — including k3s — is the wrong tool for that:

- It assumes a reachable API server. A charger offline for six hours in an
  underground car park is normal operation, not an incident.
- It costs hundreds of megabytes of RAM to supervise one process that `systemd`
  already supervises, with a hardware watchdog, resource limits and journald.
- It is a scheduler. It decides *where* a process runs, and never sits in the
  data path, so it makes nothing here faster.

The real fleet problem is not scheduling containers, it is **updating firmware
without bricking equipment in the field**. That is an A/B image update with
rollback, covered below.

Kubernetes is the right answer for the cloud side once that grows past a handful
of services. It is the wrong answer for the device.

## Installing

```sh
install -m 0755 orvolt-edge-agent /usr/bin/orvolt-edge-agent
install -d -m 0750 /etc/orvolt
install -m 0640 edge-agent.env.example /etc/orvolt/edge-agent.env
install -m 0644 orvolt-edge-agent.service /etc/systemd/system/

useradd --system --home-dir /var/lib/orvolt --shell /usr/sbin/nologin orvolt

# Per-device values: EDGE_ID, SITE_ID, the broker address and the credentials.
${EDITOR:-vi} /etc/orvolt/edge-agent.env

systemctl daemon-reload
systemctl enable --now orvolt-edge-agent
```

Check it:

```sh
systemctl status orvolt-edge-agent
curl -s localhost:9090/ready
curl -s localhost:9090/metrics | grep orvolt_edge_spool_pending_bytes
```

## What the unit gives you

`Type=notify` with `WatchdogSec=60s` is the part that matters. `Restart=always`
only catches a process that exits; the watchdog catches one that is still
running but has stopped making progress — a wedged broker loop, or a spool on a
filesystem that went read-only. The agent sends its keepalive from inside the
working loop, after a delivery attempt completes, so a stalled agent stops
reporting health rather than lying about it.

`StateDirectory=orvolt` places the spool at `/var/lib/orvolt/spool` with the
right ownership. That path must be on storage that survives a power cut and an
A/B system update, because it is what holds unsent energy readings.

## Sizing the spool

The spool is the whole reason a link outage does not lose readings, so its
ceiling is a real operational decision:

| Free flash for the spool | Roughly survivable outage, 1 connector at 1 Hz |
| --- | --- |
| 16 MiB | ~1.5 days |
| 64 MiB | ~6 days |
| 256 MiB | ~25 days |

When the ceiling is reached the **oldest** records are discarded, and
`orvolt_edge_spool_dropped_records_total` counts them. Discarding the oldest is
the lesser evil — refusing new writes would make the charger stop recording
entirely — but it is still data loss, so alert on that counter rather than
discovering it during a billing dispute.

## Firmware updates

Do not update this binary in place on a charger you cannot physically reach. Use
an A/B scheme with automatic rollback — RAUC, Mender, SWUpdate or OSTree — so a
bad image reverts on the next boot instead of requiring a technician.

Whatever you choose, the spool directory must be on a data partition that is
**not** part of the A/B pair. Otherwise an update discards the readings the
device had not yet delivered.

## Device identity

`NATS_CREDENTIALS` must point at a credential unique to this charger. A shared
credential means one compromised station can publish telemetry as any other, and
the control plane has no way to tell. See
[docs/architecture/device-identity.md](../../docs/architecture/device-identity.md).
