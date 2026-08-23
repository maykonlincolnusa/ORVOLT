# SMA Sandbox Adapter Plan

The first provider adapter will be a cloud-only, read-only SMA sandbox client. It will translate only sanctioned SMA API responses into `orvolt.energy.site.v1.EnergySiteObservation` and publish them to `orvolt.energy.site.v1`.

## Required Onboarding Inputs

The adapter cannot start against a real provider account until the operator supplies these through a secret manager or deployment environment:

- SMA sandbox client ID and client secret
- authorized redirect URI
- approved OAuth consent flow
- ORVOLT site ID to SMA site/asset mapping
- selected data capability and polling or subscription limits

Do not place client secrets, access tokens, site identifiers owned by customers, or recorded production payloads in this repository.

## Read-Only Scope

The first adapter may emit only observed solar generation, site load, grid import/export, battery charge/discharge, battery state of charge, and provider metadata. It must label missing fields as absent and retain the original observed/retrieved timestamps.

SMA GridControl and other remote-control functions are explicitly out of scope. The adapter may never issue a provider control request, and its failure must not affect the EVSE simulator, edge agent, or electrical safety domains.

## Completion Criteria

1. Approved SMA sandbox credentials are supplied outside the repository.
2. Sanctioned sandbox fixtures cover the selected endpoint schema.
3. OAuth refresh, retry/backoff, rate-limit handling, and readiness checks have tests.
4. The adapter publishes a generated `EnergySiteObservation` and the control-plane API returns it from `/api/v1/energy/sites/{siteId}/latest`.
