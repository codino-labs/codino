// The Hardware Profile as the Device stores it in flash.
#pragma once

#include <string>
#include <string_view>

#include "device_core.h"

namespace device {

// The Hardware Profile of the reference build (Waveshare ESP32-S3-Matrix,
// built-in 8x8): the one the firmware falls back to, and stores, when the
// flash holds none.
HardwareProfile referenceHardwareProfile();

// The profile as stored: one line of JSON with the fields of the
// `hardware_profile` object of a hello_reply (protocol/README.md), without the
// newline.
std::string hardwareProfileLine(const HardwareProfile& profile);

// Reads a stored profile: the line up to the first newline (a flash
// partition holds erased bytes after it). False, leaving `profile` as it was,
// unless every field is there and valid: width and height 1-255 with a Raw
// Mode frame fitting the line buffer, data_pin 0-255 (whether the board can
// drive it is the firmware's to say), color_order the letters R, G and B in
// some order, brightness_cap 1-255, current_cap_ma 1-65535, and the booleans.
bool parseHardwareProfile(std::string_view stored, HardwareProfile& profile);

// Whether `order` is the letters R, G and B, each once, in some order.
bool validColorOrder(std::string_view order);

}  // namespace device
