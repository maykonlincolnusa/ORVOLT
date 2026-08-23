# Future Energy Provider Adapters

This boundary is reserved for cloud-only adapters to authorized energy-provider APIs, such as solar, battery, site-metering, tariff, and demand-response systems. It is not an EVSE runtime, edge service, safety controller, or power controller.

## Initial Policy

1. Integrate read-only observations before any provider command.
2. Require the asset owner's explicit consent and the provider's official onboarding flow.
3. Store provider credentials in a production secret manager, never in repository configuration or device firmware.
4. Convert provider data to a versioned ORVOLT contract in the cloud integration boundary.
5. Publish observations asynchronously. Provider outages must not affect local charging safety or the running edge agent.

No Tesla, BYD, Enphase, SolarEdge, SMA, utility, or aggregator client is implemented here. An implementation requires a selected provider, its terms, user-consent flow, data-retention policy, and test environment or sandbox.

See `docs/architecture/external-energy-integrations.md` for ownership and promotion criteria.
