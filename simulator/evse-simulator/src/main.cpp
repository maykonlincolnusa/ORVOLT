#include "orvolt/simulator/telemetry.hpp"

#include <MQTTAsync.h>

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdlib>
#include <iostream>
#include <string>
#include <thread>

namespace {

std::atomic_bool keep_running{true};

void stop(int) { keep_running = false; }

std::string env_or(const char* name, const char* fallback) {
#ifdef _WIN32
  char* value = nullptr;
  std::size_t length = 0;
  if (_dupenv_s(&value, &length, name) != 0 || value == nullptr || *value == '\0') {
    std::free(value);
    return fallback;
  }
  std::string result{value};
  std::free(value);
  return result;
#else
  const char* value = std::getenv(name);
  return value == nullptr || *value == '\0' ? fallback : value;
#endif
}

bool wait_for_connection(MQTTAsync client, int timeout_ms) {
  const auto start = std::chrono::steady_clock::now();
  while (keep_running && !MQTTAsync_isConnected(client) &&
         std::chrono::steady_clock::now() - start < std::chrono::milliseconds(timeout_ms)) {
    std::this_thread::sleep_for(std::chrono::milliseconds(25));
  }
  return MQTTAsync_isConnected(client) != 0;
}

// Keeps trying to reach the broker until it succeeds or the process is asked to
// stop.
//
// A single attempt is not enough. Container orchestration only guarantees that
// the broker's *container* started, not that it is accepting connections, so a
// simulator that exits on the first refusal loses a race it should simply wait
// out.
bool connect_with_retry(MQTTAsync client, const std::string& broker) {
  for (int attempt = 1; keep_running; ++attempt) {
    MQTTAsync_connectOptions options = MQTTAsync_connectOptions_initializer;
    options.keepAliveInterval = 20;
    options.cleansession = 1;
    if (MQTTAsync_connect(client, &options) == MQTTASYNC_SUCCESS &&
        wait_for_connection(client, 5'000)) {
      return true;
    }
    std::cerr << "attempt " << attempt << ": MQTT broker " << broker
              << " is not reachable yet; retrying\n";
    std::this_thread::sleep_for(std::chrono::seconds(2));
  }
  return false;
}

}  // namespace

int main() {
  std::signal(SIGINT, stop);
  std::signal(SIGTERM, stop);

  // Flush every write. Standard output is a pipe when this runs in a container,
  // so the default block buffering holds roughly 4 KB — about twenty seconds of
  // telemetry — before anything becomes visible. A process whose logs appear in
  // delayed chunks cannot be diagnosed while it is misbehaving.
  std::cout << std::unitbuf;

  const std::string broker = std::string{"tcp://"} + env_or("MQTT_HOST", "localhost") + ":" + env_or("MQTT_PORT", "1883");
  const std::string topic = env_or("MQTT_TOPIC", "orvolt/simulators/evse/telemetry");
  const std::string station_id = env_or("STATION_ID", "orvolt-sim-001");
  const std::string connector_id = env_or("CONNECTOR_ID", "1");

  MQTTAsync client = nullptr;
  if (MQTTAsync_create(&client, broker.c_str(), station_id.c_str(), MQTTCLIENT_PERSISTENCE_NONE, nullptr) != MQTTASYNC_SUCCESS) {
    std::cerr << "failed to create MQTT client\n";
    return 1;
  }

  if (!connect_with_retry(client, broker)) {
    MQTTAsync_destroy(&client);
    return 1;
  }

  std::cout << "simulator connected to " << broker << " and publishing " << topic << "\n";
  orvolt::simulator::TelemetryGenerator generator{station_id, connector_id};
  while (keep_running) {
    // A broker restart must not end the simulator: reconnect and carry on, the
    // way the site-local publisher it stands in for would have to.
    if (!MQTTAsync_isConnected(client) && !connect_with_retry(client, broker)) {
      break;
    }

    const auto payload = orvolt::simulator::to_json(generator.next());
    MQTTAsync_message message = MQTTAsync_message_initializer;
    message.payload = const_cast<char*>(payload.data());
    message.payloadlen = static_cast<int>(payload.size());
    message.qos = 1;
    MQTTAsync_responseOptions response = MQTTAsync_responseOptions_initializer;
    if (MQTTAsync_sendMessage(client, topic.c_str(), &message, &response) != MQTTASYNC_SUCCESS) {
      std::cerr << "failed to publish telemetry\n";
    } else {
      std::cout << payload << '\n';
    }
    std::this_thread::sleep_for(std::chrono::seconds(1));
  }

  MQTTAsync_disconnectOptions disconnect = MQTTAsync_disconnectOptions_initializer;
  MQTTAsync_disconnect(client, &disconnect);
  MQTTAsync_destroy(&client);
  return 0;
}
