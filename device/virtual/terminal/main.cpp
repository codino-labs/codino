// Terminal Virtual Device: the Device core built for the Host.
//
// Speaks the protocol on stdin/stdout (one message per line: JSON, or a Raw
// Mode frame) and draws
// the matrix on stderr with ANSI 24-bit colors, so the protocol stream stays
// clean when a Host drives it through a pipe or pseudo-terminal.
//
// When the matrix is drawn on a terminal, pressing Enter in that terminal is
// the Device's Acknowledge (the button of a real Device): the Virtual Device
// sends it to the Host on stdout.
//
// With --preferences-file the Virtual Device keeps its Preferences in that
// file, as a Device keeps them in flash, and boots with them next time. With
// --store-dir it keeps the Themes and Sprite Packs installed on it in that
// directory (theme/NAME.jsonl, sprite_pack/NAME.jsonl), as a Device keeps them
// in flash.
//
// Usage: virtual-device [--size WxH] [--layout adaptive|aggregate] [--preferences-file PATH] [--store-dir DIR]
#include <chrono>
#include <cmath>
#include <condition_variable>
#include <cstdio>
#include <cstring>
#include <deque>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <memory>
#include <mutex>
#include <string>
#include <thread>

#include "device_core.h"
#include "hardware_profile.h"

#if defined(__unix__) || defined(__APPLE__)
#include <unistd.h>
#define HAVE_TERMINAL_KEYS 1
#endif

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

// What the main loop handles: a protocol line from the Host on stdin, or the
// human pressing Enter (Acknowledge).
struct Input {
  bool acknowledge = false;
  std::string line;
};

// Inputs read on their own threads, so the main loop keeps rendering
// (Disconnected, Escalation) while the Host is silent.
class LineQueue {
 public:
  void push(Input input) {
    std::lock_guard<std::mutex> lock(mu_);
    lines_.push_back(std::move(input));
    ready_.notify_one();
  }
  void close() {
    std::lock_guard<std::mutex> lock(mu_);
    closed_ = true;
    ready_.notify_one();
  }
  enum class Result { Line, Timeout, Closed };
  // Waits up to `wait` for the next input.
  Result pop(Input& line, std::chrono::milliseconds wait) {
    std::unique_lock<std::mutex> lock(mu_);
    ready_.wait_for(lock, wait, [this] { return !lines_.empty() || closed_; });
    if (!lines_.empty()) {
      line = std::move(lines_.front());
      lines_.pop_front();
      return Result::Line;
    }
    return closed_ ? Result::Closed : Result::Timeout;
  }

 private:
  std::mutex mu_;
  std::condition_variable ready_;
  std::deque<Input> lines_;
  bool closed_ = false;
};

bool parseSize(const char* text, device::HardwareProfile& profile) {
  unsigned w = 0, h = 0;
  char extra = 0;
  if (std::sscanf(text, "%ux%u%c", &w, &h, &extra) != 2) return false;
  if (w < 1 || w > 64 || h < 1 || h > 64) return false;
  profile.width = static_cast<uint8_t>(w);
  profile.height = static_cast<uint8_t>(h);
  return true;
}

// Writes `line` to `path` through a temporary file renamed over it, so a
// Virtual Device killed mid-write leaves the previous content, never half.
bool writeAtomically(const std::string& path, std::string_view line) {
  const std::string temporary = path + ".tmp";
  {
    std::ofstream out(temporary, std::ios::binary | std::ios::trunc);
    out << line << '\n';
    if (!out.flush()) return false;
  }
  return std::rename(temporary.c_str(), path.c_str()) == 0;
}

// The Preferences cache of a Device, in a file: one `preferences` line.
class FilePreferencesCache : public device::PreferencesCache {
 public:
  explicit FilePreferencesCache(std::string path) : path_(std::move(path)) {}

  std::string load() override {
    std::ifstream in(path_, std::ios::binary);
    std::string line;
    std::getline(in, line);
    return line;
  }

  void save(std::string_view line) override {
    if (!writeAtomically(path_, line)) std::fprintf(stderr, "virtual-device: can't save Preferences to %s\n", path_.c_str());
  }

 private:
  std::string path_;
};

// The Themes and Sprite Packs installed on a Device, in a directory: one
// `install` line per file, DIR/theme/NAME.jsonl and DIR/sprite_pack/NAME.jsonl.
// The Device core only hands over valid names (a-z, 0-9, - and _), so a name
// is a safe file name.
class DirectoryThemeStore : public device::ThemeStore {
 public:
  explicit DirectoryThemeStore(std::string dir) : dir_(std::move(dir)) {}

  std::vector<std::string> names(device::InstallKind kind) override {
    std::vector<std::string> out;
    std::error_code ec;
    for (const auto& entry : std::filesystem::directory_iterator(folder(kind), ec)) {
      if (entry.path().extension() == ".jsonl") out.push_back(entry.path().stem().string());
    }
    return out;
  }

  std::string load(device::InstallKind kind, std::string_view name) override {
    std::ifstream in(path(kind, name), std::ios::binary);
    std::string line;
    std::getline(in, line);
    return line;
  }

  bool save(device::InstallKind kind, std::string_view name, std::string_view line) override {
    std::error_code ec;
    std::filesystem::create_directories(folder(kind), ec);
    if (ec || !writeAtomically(path(kind, name), line)) {
      std::fprintf(stderr, "virtual-device: can't save to %s\n", path(kind, name).c_str());
      return false;
    }
    return true;
  }

  bool erase(device::InstallKind kind, std::string_view name) override {
    std::error_code ec;
    return std::filesystem::remove(path(kind, name), ec) && !ec;
  }

 private:
  std::string folder(device::InstallKind kind) const {
    return dir_ + "/" + device::wireName(kind);
  }
  std::string path(device::InstallKind kind, std::string_view name) const {
    return folder(kind) + "/" + std::string(name) + ".jsonl";
  }
  std::string dir_;
};

// The Layout the Virtual Device boots with, until a Preferences push.
bool parseLayout(const char* text, device::Layout& layout) {
  if (std::strcmp(text, "adaptive") == 0) {
    layout = device::Layout::Adaptive;
  } else if (std::strcmp(text, "aggregate") == 0) {
    layout = device::Layout::Aggregate;
  } else {
    return false;
  }
  return true;
}

}  // namespace

int main(int argc, char** argv) {
  // The reference build (Waveshare ESP32-S3-Matrix, built-in 8x8), the
  // Hardware Profile of protocol/fixtures/hello-reply.jsonl.
  device::HardwareProfile profile = device::referenceHardwareProfile();
  device::Preferences preferences;
  std::unique_ptr<FilePreferencesCache> cache;
  std::unique_ptr<DirectoryThemeStore> store;
  for (int i = 1; i < argc; ++i) {
    if (std::strcmp(argv[i], "--size") == 0 && i + 1 < argc && parseSize(argv[i + 1], profile)) {
      ++i;
    } else if (std::strcmp(argv[i], "--layout") == 0 && i + 1 < argc && parseLayout(argv[i + 1], preferences.layout)) {
      ++i;
    } else if (std::strcmp(argv[i], "--preferences-file") == 0 && i + 1 < argc && argv[i + 1][0] != '\0') {
      cache = std::make_unique<FilePreferencesCache>(argv[++i]);
    } else if (std::strcmp(argv[i], "--store-dir") == 0 && i + 1 < argc && argv[i + 1][0] != '\0') {
      store = std::make_unique<DirectoryThemeStore>(argv[++i]);
    } else {
      std::fprintf(stderr,
                   "usage: %s [--size WxH] [--layout adaptive|aggregate] [--preferences-file PATH] [--store-dir DIR]"
                   "   (W, H in 1..64)\n",
                   argv[0]);
      return 2;
    }
  }

  SteadyClock clock;
  device::DeviceCore core(profile, kFirmwareVersion, clock, preferences, cache.get(), store.get());

  device::PixelBuffer shown = core.render();
  draw(shown, "(start)");

  // Shared with the detached terminal reader, which may outlive main().
  auto input = std::make_shared<LineQueue>();
  std::thread reader([input] {
    std::string line;
    while (std::getline(std::cin, line)) input->push({false, line});
    input->close();
  });
#ifdef HAVE_TERMINAL_KEYS
  // Only when a human watches the matrix on a terminal: a Virtual Device a
  // test or a background Host Daemon drives must not read the terminal.
  if (isatty(STDERR_FILENO)) {
    // Detached: it may block reading the terminal until the process exits.
    std::thread([input] {
      std::ifstream tty("/dev/tty");
      std::string line;
      while (std::getline(tty, line)) input->push({true, {}});
    }).detach();
    std::fputs("\x1b[2m(press Enter to Acknowledge)\x1b[0m\n", stderr);
  }
#endif

  // Render at least this often without input, so time-driven frames show.
  constexpr std::chrono::milliseconds kFrameInterval{100};
  Input in;
  for (;;) {
    const LineQueue::Result got = input->pop(in, kFrameInterval);
    if (got == LineQueue::Result::Closed) break;
    std::string cause = "(no message from the Host)";
    if (got == LineQueue::Result::Line) {
      const std::string reply = in.acknowledge ? core.acknowledge() : core.receive(in.line);
      if (!reply.empty()) {
        std::cout << reply << '\n' << std::flush;
      }
      if (in.acknowledge) {
        cause = "(Acknowledge: Enter pressed)";
      } else if (!in.line.empty() && in.line[0] == device::DeviceCore::kRawFrameStart) {
        cause = "(Raw Mode frame)";  // binary pixels, not text to print
      } else {
        cause = in.line;
      }
    }
    const device::PixelBuffer& frame = core.render();
    if (frame != shown) {
      shown = frame;
      draw(frame, cause);
    }
  }
  reader.join();
  return 0;
}
