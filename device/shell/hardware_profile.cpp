#include "hardware_profile.h"

#include <ArduinoJson.h>

namespace device {

// The Waveshare ESP32-S3-Matrix with its built-in 8x8 matrix: see
// docs/hardware/reference-board.md for where each value comes from.
HardwareProfile referenceHardwareProfile() {
  HardwareProfile p;
  p.width = 8;
  p.height = 8;
  p.dataPin = 14;
  p.serpentine = false;
  p.colorOrder = "RGB";
  p.brightnessCap = 48;
  p.currentCapMa = 300;
  p.button = true;
  p.imu = true;
  return p;
}

bool validColorOrder(std::string_view order) {
  return order.size() == 3 && order.find('R') != std::string_view::npos && order.find('G') != std::string_view::npos &&
         order.find('B') != std::string_view::npos;
}

std::string hardwareProfileLine(const HardwareProfile& profile) {
  JsonDocument doc;
  doc["width"] = profile.width;
  doc["height"] = profile.height;
  doc["data_pin"] = profile.dataPin;
  doc["serpentine"] = profile.serpentine;
  doc["color_order"] = profile.colorOrder;
  doc["brightness_cap"] = profile.brightnessCap;
  doc["current_cap_ma"] = profile.currentCapMa;
  doc["button"] = profile.button;
  doc["imu"] = profile.imu;
  std::string line;
  serializeJson(doc, line);
  return line;
}

bool parseHardwareProfile(std::string_view stored, HardwareProfile& profile) {
  stored = stored.substr(0, stored.find('\n'));
  JsonDocument doc;
  if (deserializeJson(doc, stored.data(), stored.size()) || !doc.is<JsonObjectConst>()) return false;
  // Every field must be there, of its type and in its range: a profile half
  // read would drive the wrong pin or wiring.
  const auto integer = [&](const char* field, long min, long max, long& out) {
    JsonVariantConst value = doc[field];
    if (!value.is<long>()) return false;
    out = value.as<long>();
    return out >= min && out <= max;
  };
  const auto boolean = [&](const char* field, bool& out) {
    JsonVariantConst value = doc[field];
    if (!value.is<bool>()) return false;
    out = value.as<bool>();
    return true;
  };
  long width, height, dataPin, brightnessCap, currentCapMa;
  HardwareProfile p;
  if (!integer("width", 1, 255, width) || !integer("height", 1, 255, height) ||
      !integer("data_pin", 0, 255, dataPin) || !integer("brightness_cap", 1, 255, brightnessCap) ||
      !integer("current_cap_ma", 1, 65535, currentCapMa) || !boolean("serpentine", p.serpentine) ||
      !boolean("button", p.button) || !boolean("imu", p.imu)) {
    return false;
  }
  // A Raw Mode frame (0xFF, then RGB, then the newline) must fit the line
  // buffer.
  if (2 + 3 * width * height > static_cast<long>(DeviceCore::kMaxLineLength)) return false;
  JsonVariantConst colorOrder = doc["color_order"];
  if (!colorOrder.is<const char*>()) return false;
  p.colorOrder = colorOrder.as<std::string>();
  if (!validColorOrder(p.colorOrder)) return false;
  p.width = static_cast<uint8_t>(width);
  p.height = static_cast<uint8_t>(height);
  p.dataPin = static_cast<int>(dataPin);
  p.brightnessCap = static_cast<uint8_t>(brightnessCap);
  p.currentCapMa = static_cast<uint16_t>(currentCapMa);
  profile = p;
  return true;
}

}  // namespace device
