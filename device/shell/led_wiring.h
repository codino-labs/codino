// How a build's LEDs are wired: which LED of the data line shows which pixel,
// and in which order each LED takes its color channels; and the current
// limit that keeps the matrix inside the Hardware Profile's current_cap_ma.
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
  //
  // A frame the power model says draws more than current_cap_ma is dimmed
  // as a whole until it fits; a lit channel stays at least 1, like the
  // brightness cap's, and that is counted too. When the dark LEDs alone
  // draw the cap, the matrix stays dark. The model is FastLED's for a 5050
  // WS2812B (kRedMa, kGreenMa, kBlueMa at 255, kDarkMa per LED);
  // docs/hardware/reference-board.md has the budget of each preset.
  void write(const PixelBuffer& shown, uint8_t* out) const;

  static constexpr uint32_t kRedMa = 16, kGreenMa = 11, kBlueMa = 15, kDarkMa = 1;

 private:
  uint8_t width_, height_;
  bool serpentine_;
  uint16_t currentCapMa_;
  // Which of R, G, B (0, 1, 2) each LED takes first, second and third.
  uint8_t channels_[3] = {0, 1, 2};
};

}  // namespace device
