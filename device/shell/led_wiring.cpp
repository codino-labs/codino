#include "led_wiring.h"

#include "hardware_profile.h"

namespace device {

LedWiring::LedWiring(const HardwareProfile& profile)
    : width_(profile.width), height_(profile.height), serpentine_(profile.serpentine), currentCapMa_(profile.currentCapMa) {
  if (!validColorOrder(profile.colorOrder)) return;
  for (size_t i = 0; i < 3; ++i) {
    const char letter = profile.colorOrder[i];
    channels_[i] = letter == 'R' ? 0 : letter == 'G' ? 1 : 2;
  }
}

void LedWiring::write(const PixelBuffer& shown, uint8_t* out) const {
  // The model in 1/255 mA: what the lit channels draw (light), what a step
  // of 1 on each of them draws (the floor a lit channel keeps), and what the
  // LEDs draw dark.
  uint64_t light = 0, floor = 0;
  for (uint8_t y = 0; y < height_; ++y) {
    for (uint8_t x = 0; x < width_; ++x) {
      const Rgb p = shown.at(x, y);
      light += uint64_t{p.r} * kRedMa + uint64_t{p.g} * kGreenMa + uint64_t{p.b} * kBlueMa;
      floor += (p.r > 0 ? kRedMa : 0) + (p.g > 0 ? kGreenMa : 0) + (p.b > 0 ? kBlueMa : 0);
    }
  }
  const uint64_t cap = uint64_t{currentCapMa_} * 255, dark = uint64_t{kDarkMa} * count() * 255;
  // Inside the cap the frame goes out as it is. Over it every channel is
  // scaled by num / den, rounded down but kept lit: at most num / den of the
  // light plus the floor, which the scale leaves room for.
  uint64_t num = 1, den = 1;
  bool keepLit = true;
  if (light + dark > cap) {
    den = light;
    num = cap > dark + floor ? cap - dark - floor : 0;
    keepLit = num > 0;
  }
  const auto limit = [&](uint8_t c) -> uint8_t {
    if (num == den) return c;
    const uint64_t v = c * num / den;
    return static_cast<uint8_t>(v == 0 && c > 0 && keepLit ? 1 : v);
  };
  for (uint8_t y = 0; y < height_; ++y) {
    // Serpentine (zig-zag) wiring turns back at the end of every row.
    const bool reversed = serpentine_ && y % 2 == 1;
    for (uint8_t i = 0; i < width_; ++i) {
      const Rgb pixel = shown.at(reversed ? width_ - 1 - i : i, y);
      const uint8_t rgb[3] = {limit(pixel.r), limit(pixel.g), limit(pixel.b)};
      for (uint8_t channel : channels_) *out++ = rgb[channel];
    }
  }
}

}  // namespace device
