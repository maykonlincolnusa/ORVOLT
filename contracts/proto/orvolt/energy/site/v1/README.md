# `orvolt.energy.site.v1`

`energy_site.proto` defines the canonical, read-only observation for an authorized external energy site. It carries grid import/export, solar production, stationary-battery state, tariff context, provider provenance, timestamps, consent scope, and quality metadata.

All power values are non-negative kW values. Battery charging and discharging have separate fields so the payload never relies on an ambiguous signed-power convention. An absent optional value means the provider did not supply it; zero is an observed zero value.

The namespace does not represent a direct electrical-control command. Provider-specific payloads are normalized in cloud adapters before entering ORVOLT events.
