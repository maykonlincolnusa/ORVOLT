#include "orvolt/simulator/telemetry.hpp"

#include <cassert>

int main() {
  orvolt::simulator::TelemetryGenerator generator{"test-station", "1", 7};
  bool observed_charging = false;
  for (int index = 0; index < 50; ++index) {
    const auto telemetry = generator.next();
    assert(telemetry.station_id == "test-station");
    assert(telemetry.connector_id == "1");
    assert(telemetry.timestamp_ms > 0);
    assert(telemetry.soc >= 0.0 && telemetry.soc <= 100.0);
    assert(telemetry.temperature_c >= 29.0 && telemetry.temperature_c <= 38.0);
    if (telemetry.state == orvolt::simulator::ChargingState::Charging) {
      observed_charging = true;
      assert(telemetry.voltage >= 390.0 && telemetry.voltage <= 410.0);
      assert(telemetry.current >= 68.0 && telemetry.current <= 78.0);
      assert(telemetry.power_kw > 0.0);
    }
    assert(orvolt::simulator::to_json(telemetry).find("\"station_id\":\"test-station\"") != std::string::npos);
  }
  assert(observed_charging);
}
