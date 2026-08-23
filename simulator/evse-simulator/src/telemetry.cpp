#include "orvolt/simulator/telemetry.hpp"

#include <algorithm>
#include <chrono>
#include <iomanip>
#include <sstream>
#include <utility>

namespace orvolt::simulator {
namespace {

std::int64_t now_millis() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(
             std::chrono::system_clock::now().time_since_epoch())
      .count();
}

double jitter(std::mt19937& random, double low, double high) {
  return std::uniform_real_distribution<double>(low, high)(random);
}

}  // namespace

TelemetryGenerator::TelemetryGenerator(std::string station_id, std::string connector_id, std::uint32_t seed)
    : station_id_(std::move(station_id)), connector_id_(std::move(connector_id)), random_(seed) {}

Telemetry TelemetryGenerator::next() {
  const auto cycle = sequence_++ % 180;
  ChargingState state = ChargingState::Charging;
  if (cycle < 15) {
    state = ChargingState::Available;
  } else if (cycle < 30) {
    state = ChargingState::Preparing;
  } else if (cycle > 165) {
    state = ChargingState::Finishing;
  }
  if (sequence_ % 997 == 0) {
    state = ChargingState::Faulted;
  }

  double voltage = 0.0;
  double current = 0.0;
  if (state == ChargingState::Charging) {
    voltage = jitter(random_, 390.0, 410.0);
    current = jitter(random_, 68.0, 78.0);
    energy_kwh_ += (voltage * current / 1000.0) / 3600.0;
    soc_ = std::min(99.0, soc_ + 0.015);
  } else if (state == ChargingState::Finishing) {
    voltage = jitter(random_, 395.0, 405.0);
    current = jitter(random_, 4.0, 12.0);
  }

  return Telemetry{station_id_, connector_id_, now_millis(), state, voltage, current,
                   voltage * current / 1000.0, energy_kwh_, soc_, jitter(random_, 29.0, 38.0)};
}

std::string to_string(ChargingState state) {
  switch (state) {
    case ChargingState::Available:
      return "Available";
    case ChargingState::Preparing:
      return "Preparing";
    case ChargingState::Charging:
      return "Charging";
    case ChargingState::Finishing:
      return "Finishing";
    case ChargingState::Faulted:
      return "Faulted";
  }
  return "Faulted";
}

std::string to_json(const Telemetry& telemetry) {
  std::ostringstream out;
  out << std::fixed << std::setprecision(3);
  out << '{'
      << "\"station_id\":\"" << telemetry.station_id << "\","
      << "\"connector_id\":\"" << telemetry.connector_id << "\","
      << "\"timestamp_ms\":" << telemetry.timestamp_ms << ','
      << "\"state\":\"" << to_string(telemetry.state) << "\","
      << "\"voltage\":" << telemetry.voltage << ','
      << "\"current\":" << telemetry.current << ','
      << "\"power_kw\":" << telemetry.power_kw << ','
      << "\"energy_kwh\":" << telemetry.energy_kwh << ','
      << "\"soc\":" << telemetry.soc << ','
      << "\"temperature_c\":" << telemetry.temperature_c << '}';
  return out.str();
}

}  // namespace orvolt::simulator
