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
int liftForTerminal(uint8_t channel) {
  return static_cast<int>(std::lround(std::sqrt(channel / 255.0) * 255.0));
}

// The input line shown above a frame: control bytes (ANSI escapes, a CR from
// CRLF) are replaced so a message cannot drive the terminal, and long lines are
// cut on a UTF-8 character boundary.
std::string caption(const std::string& line) {
  constexpr size_t kMax = 72;
  std::string out;
  for (size_t i = 0; i < line.size(); ++i) {
    const unsigned char byte = static_cast<unsigned char>(line[i]);
    const bool startsCharacter = (byte & 0xC0) != 0x80;
    if (out.size() >= kMax && startsCharacter) return out + "...";
    out += (byte < 0x20 || byte == 0x7F) ? '?' : line[i];
  }
  return out;
}

void draw(const device::PixelBuffer& frame, const std::string& cause) {
  std::string out = "\x1b[2m> " + caption(cause) + "\x1b[0m\n";
  char pixelText[48];
  for (uint8_t y = 0; y < frame.height(); ++y) {
    out += "  ";
    for (uint8_t x = 0; x < frame.width(); ++x) {
      const device::Rgb c = frame.at(x, y);
      if (c == device::Rgb{}) {
        out += "\x1b[38;5;238m\xc2\xb7\xc2\xb7";  // "··" for an unlit LED
      } else {
        std::snprintf(pixelText, sizeof pixelText, "\x1b[38;2;%d;%d;%dm\xe2\x96\x88\xe2\x96\x88",
                      liftForTerminal(c.r), liftForTerminal(c.g), liftForTerminal(c.b));  // "██"
        out += pixelText;
      }
    }
    out += "\x1b[0m\n";
  }
  std::fputs(out.c_str(), stderr);
  std::fflush(stderr);
}

// Defaults to the reference build (Waveshare ESP32-S3-Matrix, built-in 8x8),
// the same Hardware Profile as protocol/fixtures/hello-reply.jsonl.
device::HardwareProfile referenceProfile() {
  device::HardwareProfile p;
  p.width = 8;
  p.height = 8;
  p.dataPin = 14;
  p.serpentine = false;
  p.colorOrder = "RGB";
  p.brightnessCap = 48;
  p.currentCapMa = 300;
  p.button = false;
  p.imu = true;
  return p;
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
  device::HardwareProfile profile = referenceProfile();
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

  device::PixelBuffer shown = core.render();
  draw(shown, "(start)");

  std::string line;
  while (std::getline(std::cin, line)) {
    const std::string reply = core.receive(line);
    if (!reply.empty()) {
      std::cout << reply << '\n' << std::flush;
    }
    const device::PixelBuffer& frame = core.render();
    if (frame != shown) {
      shown = frame;
      draw(frame, line);
    }
  }
  return 0;
}
