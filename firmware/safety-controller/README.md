# Future Safety Controller

This directory defines the boundary for a future dedicated safety controller. It is not implemented and must not be replaced by cloud or application software.

Its future responsibilities include emergency stop, insulation monitoring, RCD state, contactor feedback, over-temperature, over-voltage, over-current, watchdog supervision, and transition to a fail-safe state. The design must use appropriate hardware engineering, independent protection paths, validation, and certification before any physical deployment.

The controller must remain safe without internet, cloud, NATS, MQTT, or the ORVOLT edge agent. No source in this milestone may energize or command high-voltage equipment.
