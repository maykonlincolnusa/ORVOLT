# Future Power Controller

This directory reserves the future low-level power-conversion control boundary. It is not implemented in this milestone.

Future responsibilities include AC/DC conversion, DC/DC conversion, DC-bus regulation, current and voltage control, thermal limits, PWM generation, and power-module coordination. A safety controller must have independent authority to inhibit or shut down the power path.

There is no cloud-to-power-controller connection. Any future software implementation requires hardware design, limits enforcement, safety validation, and certification before use with real equipment.
