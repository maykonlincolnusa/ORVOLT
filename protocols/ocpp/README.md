# OCPP Adapter Boundary

An OCPP 1.6J core-profile adapter is implemented as a cloud service in
[`cloud/ocpp-gateway/`](../../cloud/ocpp-gateway/), built on the validated
external `lorenzodonini/ocpp-go` implementation rather than a reimplementation.

It covers the core profile only: telemetry, connector status and transactions.
Smart charging, firmware management, local authorization lists, reservations and
diagnostics are not implemented. No OCPP certification or compliance is claimed.

OCPP 2.0.1 and 2.1 remain future work under the same boundary rules.
