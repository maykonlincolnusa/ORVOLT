#pragma once

#include <cstdint>
#include <random>
#include <string>

namespace orvolt::simulator {

enum class ChargingState { Available, Preparing, Charging, Finishing, Faulted };

struct Telemetry {
  std::string station_id;
  std::string connector_id;
  std::int64_t timestamp_ms;
  ChargingState state;
  double voltage;
  double current;
  double power_kw;
  double energy_kwh;
  double soc;
  double temperature_c;
};

class TelemetryGenerator {
 public:
  TelemetryGenerator(std::string station_id, std::string connector_id, std::uint32_t seed = 42);
  Telemetry next();

 private:
  std::string station_id_;
  std::string connector_id_;
  std::mt19937 random_;
  std::uint64_t sequence_ = 0;
  double energy_kwh_ = 18.0;
  double soc_ = 42.0;
};

std::string to_json(const Telemetry& telemetry);
std::string to_string(ChargingState state);

}  // namespace orvolt::simulator
