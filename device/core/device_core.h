// Device core: protocol messages and clock ticks in, pixel buffer out.
//
// Portable C++17 with no Arduino or FastLED dependency, so the same code runs
// in the firmware shell, the terminal Virtual Device and the browser Virtual Device.
#pragma once

#include <cstdint>
#include <array>
#include <map>
#include <string>
#include <string_view>
#include <vector>

#include "pixels.h"
#include "theme.h"

namespace device {

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
  // The name of the Theme the Device renders by: a built-in one or one
  // installed. A Theme the Device doesn't have shows as the default one.
  std::string theme = kDefaultTheme;
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

// Where the firmware shell keeps installed Themes and Sprite Packs across
// reboots (flash on a Device, a directory on the terminal Virtual Device).
// Each is one `install` protocol line under its kind and name. The Device core
// does no I/O: it lists them when constructed and loads one when it needs it.
class ThemeStore {
 public:
  virtual ~ThemeStore() = default;
  virtual std::vector<std::string> names(InstallKind kind) = 0;
  // The line saved under `name`; empty when there is none.
  virtual std::string load(InstallKind kind, std::string_view name) = 0;
  // Replaces what is saved under `name`. False when it couldn't be saved.
  virtual bool save(InstallKind kind, std::string_view name, std::string_view line) = 0;
  // False when it couldn't be removed.
  virtual bool erase(InstallKind kind, std::string_view name) = 0;
};

// Themes and Sprite Packs kept in RAM only: a Device without storage keeps
// what it was sent until it reboots.
class MemoryThemeStore : public ThemeStore {
 public:
  std::vector<std::string> names(InstallKind kind) override;
  std::string load(InstallKind kind, std::string_view name) override;
  bool save(InstallKind kind, std::string_view name, std::string_view line) override;
  bool erase(InstallKind kind, std::string_view name) override;

 private:
  std::map<std::pair<InstallKind, std::string>, std::string> lines_;
};

class DeviceCore {
 public:
  static constexpr int kProtocolVersion = 0;

  // `preferences` are the defaults; Preferences in `cache` (if any) win, so a
  // Device boots with the user's choices before a Host connects. Themes and
  // Sprite Packs are installed into `store` (RAM only when there is none).
  // The firmware may hold at most kMaxInstalled of each kind besides its
  // built-in ones.
  DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock,
             Preferences preferences = {}, PreferencesCache* cache = nullptr, ThemeStore* store = nullptr);

  static constexpr size_t kMaxInstalled = 8;
  // The longest protocol line a Device takes, its newline included: an
  // `install` with a Sprite Pack is the longest message.
  static constexpr size_t kMaxLineLength = 8192;

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
  // The names of the Themes or Sprite Packs the Device has, built-in first.
  std::vector<std::string> available(InstallKind kind) const;
  void addAvailable(JsonDocument& reply) const;

  // install and uninstall: the reply to send back.
  std::string install(JsonVariantConst message);
  std::string uninstall(JsonVariantConst message);
  std::string installReply(JsonVariantConst message, const std::string& error) const;
  // Reads the Theme named by the Preferences (or the default one), and the
  // Sprite Pack frames it uses, into theme_ and poses_.
  void selectTheme();
  // The Theme or Sprite Pack `name` as JSON (built-in or installed) into
  // `doc`; false when the Device has none of that name.
  bool lookUp(InstallKind kind, std::string_view name, JsonDocument& doc) const;

  HardwareProfile profile_;
  std::string firmwareVersion_;
  // The firmware's Preferences: what a field missing from a push means.
  Preferences defaults_;
  bool disconnected();
  void hostMessage();
  void renderDisconnected();
  void renderUpdateMe();
  // Renders the picture into frame_ for the current clock time.
  void renderFrame();
  // Renders sessions_: a Takeover, the Aggregate State or the Tiles.
  void renderSessions();

  // Renders by `preferences` from now on, and caches them when they changed.
  void applyPreferences(const Preferences& preferences);
  // Turns the logical frame by the rotation and scales it under the
  // brightness cap into what the LEDs show.
  const PixelBuffer& present();

  const Clock& clock_;
  PreferencesCache* cache_;
  MemoryThemeStore memoryStore_;
  ThemeStore* store_;
  // The Theme rendered by and, per Session State, the frames of its pose
  // (none when the state is an effect, or its Sprite Pack is missing). The
  // abstract Theme's effects stand in for a pose wherever it can't show.
  Theme theme_;
  Theme abstract_;
  struct Pose {
    uint8_t width = 0, height = 0;
    std::vector<std::vector<uint8_t>> frames;
    uint32_t frameMs = 500;
  };
  std::array<Pose, kSessionStateCount> poses_;
  // Draws how the Theme shows `state` over `region` in `color`: its pose
  // when the region is the whole matrix and it has one, else its effect, or
  // the abstract Theme's for a pose.
  void drawState(Region region, SessionState state, Rgb color, bool wholeMatrix);
  Preferences preferences_;
  // The frame being rendered, in the orientation the picture is seen in
  // (width and height swapped when turned by 90 or 270 degrees), and the
  // frame the LEDs show.
  PixelBuffer frame_;
  PixelBuffer shown_;
  struct Session {
    SessionState state = SessionState::Idle;
    // Without a valid Project Color the session shows its Session State's.
    bool hasProjectColor = false;
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
  void renderAggregate(EscalationStep mostInsistent);
  void renderTiles();
  void renderTile(Region region, SessionState state, Rgb color, EscalationStep step);
  // The session's Project Color, or its Session State's own color.
  Rgb colorOf(const Session& session) const;
  // Acknowledges the session at clock time `at`.
  void acknowledge(Session& session, uint32_t at);
  void heardHostTime(int64_t hostTime);
  uint32_t clockTimeOf(int64_t hostTime) const;

  void receiveRawFrame(std::string_view frame);

  std::map<std::string, Session> sessions_;
  // Raw Mode: the Host's pixels, shown while no session is Awaiting Input.
  bool rawMode_ = false;
  // Preview (the web UI's preview-state override): the matrix shows what it
  // would with one session, the one in previewed_, in place of the sessions
  // and Raw Mode, unless a session is Awaiting Input, which pre-empts it.
  // previewed_ is swapped into sessions_ to render.
  bool preview_ = false;
  std::map<std::string, Session> previewed_;
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
  // The Host said goodbye (it exited cleanly): its silence is no
  // Disconnected, until any usable message ends it.
  bool saidGoodbye_ = false;
  // A Host of another protocol version said hello: the Device shows "update
  // me" and ignores everything but the next hello.
  bool updateWanted_ = false;
};

}  // namespace device
