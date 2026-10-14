// Browser Virtual Device: the Device core built for WebAssembly (wasm32-wasi,
// reactor), for the web UI's live preview. `make -C device web` builds it.
//
// It does no I/O: the page feeds it the protocol lines the Host Daemon sends
// the Device, sets the clock and reads the frame back. The exported functions
// are a C ABI over linear memory:
//
//   vd_start(width, height, brightness_cap)  a fresh Device with that profile
//   vd_line(size)     a buffer of `size` bytes for the next protocol line
//   vd_receive(size)  feeds the line in that buffer (no newline); returns
//                     the size of the Device's reply (0 for none)
//   vd_reply()        that reply: a hello_reply tells the page which Themes
//                     the preview has
//   vd_set_millis(ms) the Device clock, before each vd_render
//   vd_render()       the frame: width x height x RGB, row-major
//   vd_width(), vd_height()  the frame's size: the matrix's
//
// The Host Daemon talks to the real Device: the page only watches, and sends
// no reply back.
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "device_core.h"

#define EXPORT(name) extern "C" __attribute__((export_name(#name)))

namespace {

constexpr const char* kFirmwareVersion = "0.1.0";

class PageClock : public device::Clock {
 public:
  uint32_t millis() const override { return now; }
  uint32_t now = 0;
};

PageClock pageClock;
std::unique_ptr<device::DeviceCore> core;
std::string line;
std::string reply;
std::vector<uint8_t> rgb;
device::HardwareProfile profile;

}  // namespace

EXPORT(vd_start) void vdStart(int width, int height, int brightnessCap) {
  profile = {};
  profile.width = static_cast<uint8_t>(width);
  profile.height = static_cast<uint8_t>(height);
  profile.brightnessCap = static_cast<uint8_t>(brightnessCap);
  core = std::make_unique<device::DeviceCore>(profile, kFirmwareVersion, pageClock);
}

EXPORT(vd_set_millis) void vdSetMillis(uint32_t ms) { pageClock.now = ms; }

EXPORT(vd_line) char* vdLine(int size) {
  line.assign(static_cast<size_t>(size), '\0');
  return line.data();
}

EXPORT(vd_receive) int vdReceive(int size) {
  reply = core ? core->receive(std::string_view(line.data(), static_cast<size_t>(size))) : std::string();
  return static_cast<int>(reply.size());
}

EXPORT(vd_reply) const char* vdReply() { return reply.data(); }

EXPORT(vd_render) const uint8_t* vdRender() {
  if (!core) return nullptr;
  const device::PixelBuffer& frame = core->render();
  rgb.resize(static_cast<size_t>(frame.width()) * frame.height() * 3);
  size_t i = 0;
  for (uint8_t y = 0; y < frame.height(); ++y)
    for (uint8_t x = 0; x < frame.width(); ++x) {
      const device::Rgb c = frame.at(x, y);
      rgb[i++] = c.r;
      rgb[i++] = c.g;
      rgb[i++] = c.b;
    }
  return rgb.data();
}

// The matrix's size: the rotation turns the picture, not the LEDs.
EXPORT(vd_width) int vdWidth() { return profile.width; }
EXPORT(vd_height) int vdHeight() { return profile.height; }
