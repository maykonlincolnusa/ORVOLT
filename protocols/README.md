# Future Protocol Adapters

ORVOLT will integrate standards through dedicated adapters around established, standards-compliant implementations rather than reimplementing protocols from scratch. The preferred future EVSE runtime is EVerest.

Each adapter must translate its protocol-specific representation to or from versioned ORVOLT contracts at a bounded interface. It must not bypass safety or power-controller authority.
