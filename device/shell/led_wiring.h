// How a build's LEDs are wired: which LED of the data line shows which pixel,
// and in which order each LED takes its color channels.
#pragma once

#include <cstddef>
#include <cstdint>

#include "device_core.h"

namespace device {

class LedWiring {
 public:
  // A color order that isn't the letters R, G and B in some order is taken
  // as "RGB" (parseHardwareProfile refuses one).
  explicit LedWiring(const HardwareProfile& profile);

  // How many LEDs the data line drives.
  size_t count() const { return size_t{width_} * height_; }

  // Writes `shown` (what DeviceCore::render() returns: already turned by the
  // rotation and under the brightness cap) into `out` as the data line sends
  // it: 3 x count() bytes, LED after LED from the first on the line.
  void write(const PixelBuffer& shown, uint8_t* out) const;

 private:
  uint8_t width_, height_;
  bool serpentine_;
  // Which of R, G, B (0, 1, 2) each LED takes first, second and third.
  uint8_t channels_[3] = {0, 1, 2};
};

}  // namespace device
