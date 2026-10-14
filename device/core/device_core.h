// Device core: protocol messages and clock ticks in, pixel buffer out.
//
// Portable C++17 with no Arduino or FastLED dependency, so the same code runs
// in the firmware shell, the terminal Virtual Device and (later) the browser.
#pragma once

#include <cstdint>
#include <map>
#include <string>
#include <string_view>
#include <vector>

namespace device {

enum class SessionState { Idle, Working, AwaitingInput, Done, Error };

struct Rgb {
  uint8_t r = 0, g = 0, b = 0;

  friend bool operator==(Rgb lhs, Rgb rhs) { return lhs.r == rhs.r && lhs.g == rhs.g && lhs.b == rhs.b; }
  friend bool operator!=(Rgb lhs, Rgb rhs) { return !(lhs == rhs); }
};

// Describes a physical build. Flashed with the firmware; reported in the
// handshake reply.
struct HardwareProfile {
  uint8_t width = 8;
  uint8_t height = 8;
  int dataPin = 0;
  bool serpentine = false;
  std::string colorOrder = "GRB";
  uint8_t brightnessCap = 255;
  uint16_t currentCapMa = 500;
  bool button = false;
  bool imu = false;
};

// Injectable time source. The firmware shell wraps millis(); tests use a fake.
class Clock {
 public:
  virtual ~Clock() = default;
  virtual uint32_t millis() const = 0;
};

// Logical pixels, row-major from the top-left. Mapping to the physical wiring
// (serpentine, color order) is the firmware shell's job.
class PixelBuffer {
 public:
  PixelBuffer(uint8_t width, uint8_t height) : width_(width), height_(height), pixels_(width * height) {}

  uint8_t width() const { return width_; }
  uint8_t height() const { return height_; }
  Rgb at(uint8_t x, uint8_t y) const { return pixels_[y * width_ + x]; }
  void fill(Rgb color) { pixels_.assign(pixels_.size(), color); }
  void set(uint8_t x, uint8_t y, Rgb color) { pixels_[y * width_ + x] = color; }

  friend bool operator==(const PixelBuffer& lhs, const PixelBuffer& rhs) {
    return lhs.width_ == rhs.width_ && lhs.height_ == rhs.height_ && lhs.pixels_ == rhs.pixels_;
  }
  friend bool operator!=(const PixelBuffer& lhs, const PixelBuffer& rhs) { return !(lhs == rhs); }

 private:
  uint8_t width_;
  uint8_t height_;
  std::vector<Rgb> pixels_;
};

// How Awaiting Input escalates the longer it goes unacknowledged. The
// thresholds are Preferences.
struct Escalation {
  // When each step starts, counted from when the session entered Awaiting
  // Input or was last Acknowledged: a gentle pulse first, then faster and
  // brighter, then a Takeover, then a steady glow.
  uint32_t fasterFromMs = 30 * 1000;
  uint32_t takeoverFromMs = 2 * 60 * 1000;
  uint32_t steadyFromMs = 10 * 60 * 1000;
  // How long each Awaiting Input session's Project Color holds when several
  // sessions are in Takeover.
  uint32_t takeoverCycleMs = 1000;
};

// How the matrix area is used. Adaptive by default; a Preference.
enum class Layout {
  // One Tile per Agent Session, in its Project Color and animated by its
  // Session State: one session has the whole matrix (showing the Aggregate
  // State), two get halves, three or four quadrants, five to eight columns;
  // with nine or more the eighth column is the Overflow Tile, showing the
  // Aggregate State of the sessions that don't fit. Tile slots come from the
  // Host.
  Adaptive,
  // The Aggregate State on the whole matrix, however many sessions.
  Aggregate,
};

// How long the Host may stay silent before the Device shows Disconnected,
// unless the Preferences say otherwise.
constexpr uint32_t kDefaultDisconnectedTimeoutMs = 5000;

// The user's choices the Device renders by, pushed by the Host (the
// `preferences` message) and cached on the Device. The Host keeps the others
// (Done decay, pinned Project Colors).
struct Preferences {
  // The Theme's name. Kept and cached; v0 renders every Theme alike until
  // Themes land (#12).
  std::string theme = "default";
  // Percent (1-100) of the Hardware Profile's brightness cap: 100 is the cap.
  uint8_t brightness = 100;
  Layout layout = Layout::Adaptive;
  // Degrees (0, 90, 180, 270) the picture is turned clockwise on the matrix,
  // for a Device mounted another way round.
  uint16_t rotation = 0;
  // Shows nothing but Awaiting Input, Escalation and Takeover included.
  bool quietMode = false;
  // How long the Host may stay silent before the Device shows Disconnected.
  uint32_t disconnectedTimeoutMs = kDefaultDisconnectedTimeoutMs;
  Escalation escalation;
};

// Where the firmware shell keeps the Preferences across reboots (flash on a
// Device, a file or nothing on a Virtual Device). The Device core does no I/O:
// it loads them once when constructed and saves them whenever a push changes
// them, as one `preferences` protocol line.
class PreferencesCache {
 public:
  virtual ~PreferencesCache() = default;
  // The line saved last; empty when there is none.
  virtual std::string load() = 0;
  virtual void save(std::string_view line) = 0;
};

class DeviceCore {
 public:
  static constexpr int kProtocolVersion = 0;

  // `preferences` are the defaults; Preferences in `cache` (if any) win, so a
  // Device boots with the user's choices before a Host connects.
  DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock,
             Preferences preferences = {}, PreferencesCache* cache = nullptr);

  // The Preferences the Device renders by.
  const Preferences& preferences() const { return preferences_; }
  // The Preferences as a `preferences` protocol line, as the cache keeps them.
  std::string preferencesLine() const;

  // The first byte of a Raw Mode frame: never valid UTF-8, so no JSON line
  // starts with it. The frame is that byte, then width x height x 3 bytes of
  // RGB, row-major from the top-left, none of them a newline.
  static constexpr char kRawFrameStart = '\xFF';

  // Feeds one protocol line (without the trailing newline): a JSON message or
  // a Raw Mode frame. Returns the reply line to send back, or an empty string
  // when there is none.
  std::string receive(std::string_view line);

  // The human Acknowledges on the Device (button press, IMU double-tap, a key
  // on the Virtual Device): every Done session is cleared and the Escalation
  // of every Awaiting Input session starts over. Returns the line to send to
  // the Host.
  std::string acknowledge();

  // Renders the frame for the current clock time.
  const PixelBuffer& render();

 private:
  SessionState aggregateState() const;
  std::string helloReply() const;

  HardwareProfile profile_;
  std::string firmwareVersion_;
  // The firmware's Preferences: what a field missing from a push means.
  Preferences defaults_;
  bool disconnected();
  void hostMessage();
  void renderDisconnected();
  // Renders the picture into frame_ for the current clock time.
  void renderFrame();

  // Renders by `preferences` from now on, and caches them when they changed.
  void applyPreferences(const Preferences& preferences);
  // Turns the logical frame by the rotation and scales it under the
  // brightness cap into what the LEDs show.
  const PixelBuffer& present();

  const Clock& clock_;
  PreferencesCache* cache_;
  Preferences preferences_;
  // The frame being rendered, in the orientation the picture is seen in
  // (width and height swapped when turned by 90 or 270 degrees), and the
  // frame the LEDs show.
  PixelBuffer frame_;
  PixelBuffer shown_;
  struct Session {
    SessionState state = SessionState::Idle;
    Rgb projectColor;
    // Tile slot from the Host: where the session goes in the adaptive Layout.
    int tile = 0;
    // Clock time Escalation counts from: when the session entered Awaiting
    // Input or was last Acknowledged.
    uint32_t escalationStart = 0;
    // Latched once Escalation settles into the steady glow, so going
    // unacknowledged longer than the millis() wrap (~49.7 days) never starts
    // it over.
    bool settled = false;
  };
  // How far Escalation has gone for a session Awaiting Input: its step and
  // how long the session has been in that step. None for other states.
  struct EscalationStep {
    enum Kind { None, Steady, Gentle, Faster, Takeover } kind = None;  // least insistent first
    uint32_t elapsed = 0;
  };
  EscalationStep escalationStep(Session& session);
  static int awaitingInputLevel(EscalationStep step);
  // A region of the matrix: columns [x0, x1), rows [y0, y1).
  struct Region {
    int x0, y0, x1, y1;
  };
  void renderAggregate(EscalationStep mostInsistent);
  void renderTiles();
  void renderTile(Region region, SessionState state, Rgb color, EscalationStep step);
  // Acknowledges the session at clock time `at`.
  void acknowledge(Session& session, uint32_t at);
  void heardHostTime(int64_t hostTime);
  uint32_t clockTimeOf(int64_t hostTime) const;

  void receiveRawFrame(std::string_view frame);

  std::map<std::string, Session> sessions_;
  // Raw Mode: the Host's pixels, shown while no session is Awaiting Input.
  bool rawMode_ = false;
  PixelBuffer raw_;
  // The latest Host time heard (hello, heartbeat) and the clock time it was
  // heard at, to relate Host times to the clock.
  bool hostTimeKnown_ = false;
  int64_t hostTime_ = 0;
  uint32_t hostTimeHeardAt_ = 0;
  // Clock time of the last message from the Host (boot until the first).
  uint32_t lastHostMessage_;
  // Latched once the timeout passes, until the next Host message, so a
  // silence longer than the millis() wrap (~49.7 days) never ends it.
  bool disconnected_ = false;
};

}  // namespace device
