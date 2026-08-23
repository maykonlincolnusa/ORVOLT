# Kubernetes (cloud only)

These manifests deploy the **cloud** services. Nothing here belongs on a
charger — see [edge/packaging/](../../edge/packaging/README.md) for why.

## When this is worth it

Kubernetes is a scheduler. It decides *where* a process runs and never sits in
the data path, so it makes nothing faster. It earns its cost when you have:

- more than a handful of services to place,
- ingest that must scale horizontally as the fleet grows,
- rolling deploys that must not drop consumers,
- more than one person deploying.

Below that, a single host running `docker compose`, or a managed container
service, is less machinery for the same result. Adopting an orchestrator to run
two stateless services is overhead, not architecture.

## What is deliberately not here

**PostgreSQL and NATS.** Running your own stateful systems on Kubernetes is a
separate decision with its own operators, backup strategy and failure modes, and
making it implicitly by dropping a StatefulSet into a starter manifest is how
teams lose data. Use managed PostgreSQL and either managed NATS or the NATS
Helm chart, and point `POSTGRES_DSN` and `NATS_URL` at them.

**Ingress and TLS termination.** Cluster-specific, and the OCPP listener needs
WebSocket support with a long idle timeout — charge points hold a connection for
hours between messages. A default 60-second idle timeout will disconnect your
whole fleet on a loop.

## Scaling, honestly

| Service | Replicas | Why |
| --- | --- | --- |
| `control-plane` | Scales out | JetStream pull consumers share one durable consumer, so replicas divide the work rather than duplicating it. Ingest is idempotent, so a redelivery during a rollout is harmless. |
| `ocpp-gateway` | **1** | Two reasons, both real. Charge points hold long-lived WebSockets, so a replica's connections are not shareable. And the bundled transaction-id allocator is per-process: two replicas can issue the same id. Implement `csms.TransactionIDs` against a shared durable sequence before raising this. |

The `HorizontalPodAutoscaler` targets CPU, which tracks decode and database work
reasonably. If ingest lag is what you actually care about, scale on JetStream
consumer pending count through an external metric instead.

## Applying

```sh
kubectl apply -k infra/k8s/

# Supply the real secrets rather than the placeholders in secret.example.yaml.
kubectl -n orvolt create secret generic orvolt-secrets \
  --from-literal=POSTGRES_DSN='postgres://...' \
  --from-literal=TOKEN_PEPPER="$(openssl rand -hex 32)" \
  --from-file=nats.creds=./control-plane.creds
```

`TOKEN_PEPPER` is what makes stored card identifiers irreversible. Changing it
re-identifies every card in the fleet, so treat it as permanent and back it up
with the database.

## What this does not give you

- No backup or restore. That belongs to whatever runs PostgreSQL.
- No secret rotation.
- No multi-region. The control plane is stateless, but the streams and the
  database are not, and pretending otherwise is how you get split-brain billing.
- No authentication on the management API. `NetworkPolicy` limits who can reach
  it; that is containment, not authorization. See
  [docs/architecture/device-identity.md](../../docs/architecture/device-identity.md).
