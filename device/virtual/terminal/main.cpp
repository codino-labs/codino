// Terminal Virtual Device: the Device core built for the Host.
//
// Speaks the protocol on stdin/stdout (one JSON message per line) and draws
// the matrix on stderr with ANSI 24-bit colors, so the protocol stream stays
// clean when a Host drives it through a pipe or pseudo-terminal.
//
// Usage: virtual-device [--size WxH]
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <iostream>
#include <string>
#include <vector>

#include "device_core.h"

namespace {

constexpr const char* kFirmwareVersion = "0.1.0";

class SteadyClock : public device::Clock {
 public:
  uint32_t millis() const override {
    using namespace std::chrono;
    return static_cast<uint32_t>(duration_cast<milliseconds>(steady_clock::now() - start_).count());
  }

 private:
  std::chrono::steady_clock::time_point start_ = std::chrono::steady_clock::now();
};

// LEDs at low values still glow visibly; a terminal cell does not. Lift dark
// channels with a square-root curve so dim states stay readable on screen.
int toScreen(uint8_t channel) {
  return static_cast<int>(std::lround(std::sqrt(channel / 255.0) * 255.0));
}

void draw(const device::PixelBuffer& frame, const std::string& cause) {
  std::string out;
  out += "\x1b[2m> " + (cause.size() > 72 ? cause.substr(0, 69) + "..." : cause) + "\x1b[0m\n";
  char cell[48];
  for (uint8_t y = 0; y < frame.height(); ++y) {
    out += "  ";
    for (uint8_t x = 0; x < frame.width(); ++x) {
      const device::Rgb c = frame.at(x, y);
      if (c == device::Rgb{}) {
        out += "\x1b[38;5;238m\xc2\xb7\xc2\xb7";  // "··" for an unlit LED
      } else {
        std::snprintf(cell, sizeof cell, "\x1b[38;2;%d;%d;%dm\xe2\x96\x88\xe2\x96\x88",
                      toScreen(c.r), toScreen(c.g), toScreen(c.b));  // "██"
        out += cell;
      }
    }
    out += "\x1b[0m\n";
  }
  std::fputs(out.c_str(), stderr);
  std::fflush(stderr);
}

std::vector<device::Rgb> pixelsOf(const device::PixelBuffer& frame) {
  std::vector<device::Rgb> pixels;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) pixels.push_back(frame.at(x, y));
  return pixels;
}

bool parseSize(const char* text, device::HardwareProfile& profile) {
  unsigned w = 0, h = 0;
  char extra = 0;
  if (std::sscanf(text, "%ux%u%c", &w, &h, &extra) != 2) return false;
  if (w < 1 || w > 64 || h < 1 || h > 64) return false;
  profile.width = static_cast<uint8_t>(w);
  profile.height = static_cast<uint8_t>(h);
  return true;
}

}  // namespace

int main(int argc, char** argv) {
  device::HardwareProfile profile;  // 8x8 unless --size says otherwise
  for (int i = 1; i < argc; ++i) {
    if (std::strcmp(argv[i], "--size") == 0 && i + 1 < argc && parseSize(argv[i + 1], profile)) {
      ++i;
    } else {
      std::fprintf(stderr, "usage: %s [--size WxH]   (W, H in 1..64)\n", argv[0]);
      return 2;
    }
  }

  SteadyClock clock;
  device::DeviceCore core(profile, kFirmwareVersion, clock);

  auto shown = pixelsOf(core.render());
  draw(core.render(), "(start)");

  std::string line;
  while (std::getline(std::cin, line)) {
    const std::string reply = core.receive(line);
    if (!reply.empty()) {
      std::cout << reply << '\n' << std::flush;
    }
    const auto& frame = core.render();
    auto now = pixelsOf(frame);
    if (now != shown) {
      shown = std::move(now);
      draw(frame, line);
    }
  }
  return 0;
}
