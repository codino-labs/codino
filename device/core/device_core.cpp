#include "device_core.h"

#include <ArduinoJson.h>

#include <algorithm>
#include <climits>

namespace device {

namespace {

// What the core knows about each Session State beyond its Theme. `urgency`:
// higher wins the Aggregate State (Awaiting Input > Error > Done > Working >
// Idle).
struct SessionStateInfo {
  SessionState state;
  int urgency;
};

constexpr SessionStateInfo kSessionStates[] = {
    {SessionState::Idle, 0},  {SessionState::Working, 1},       {SessionState::Done, 2},
    {SessionState::Error, 3}, {SessionState::AwaitingInput, 4},
};

const SessionStateInfo* findByWireName(std::string_view name) {
  for (const auto& info : kSessionStates)
    if (name == wireName(info.state)) return &info;
  return nullptr;
}

const SessionStateInfo& infoFor(SessionState state) {
  for (const auto& info : kSessionStates)
    if (info.state == state) return info;
  return kSessionStates[0];
}

// A pulse between brightness `low` and `high` (of 255) with the given period:
// a triangle wave starting at `low`, `elapsed` ms in.
struct Pulse {
  uint32_t periodMs;
  int low, high;

  int level(uint32_t elapsed) const {
    const uint32_t half = periodMs / 2;
    const uint32_t phase = elapsed % periodMs;
    const uint32_t up = phase < half ? phase : periodMs - phase;
    return low + static_cast<int>((high - low) * up / half);
  }
};

constexpr Pulse kGentlePulse{2000, 90, 170};
constexpr Pulse kFasterPulse{700, 120, 255};
// The steady glow's brightness: Escalation settles, so lunch isn't a strobe.
constexpr int kSteadyGlow = 170;

struct LayoutName {
  Layout layout;
  const char* wireName;
};
constexpr LayoutName kLayouts[] = {{Layout::Adaptive, "adaptive"}, {Layout::Aggregate, "aggregate"}};

// The shortest Disconnected timeout: the Host sends a heartbeat every second.
constexpr uint32_t kMinDisconnectedTimeoutMs = 1000;

// Reads the Preferences of a `preferences` message into `out`. Every field is
// optional: a missing one takes its value from `defaults`. A field with an
// invalid value makes the whole message unusable (false, `out` untouched).
bool parsePreferences(JsonVariantConst message, const Preferences& defaults, Preferences& out) {
  Preferences p = defaults;
  JsonVariantConst theme = message["theme"];
  if (!theme.isNull()) {
    if (!theme.is<const char*>()) return false;
    std::string_view name = theme.as<const char*>();
    if (!isValidName(name)) return false;
    p.theme = std::string(name);
  }
  JsonVariantConst brightness = message["brightness"];
  if (!brightness.isNull()) {
    if (!brightness.is<int>() || brightness.as<int>() < 1 || brightness.as<int>() > 100) return false;
    p.brightness = static_cast<uint8_t>(brightness.as<int>());
  }
  JsonVariantConst layout = message["layout"];
  if (!layout.isNull()) {
    const LayoutName* found = nullptr;
    for (const auto& l : kLayouts)
      if (layout.is<const char*>() && std::string_view(layout.as<const char*>()) == l.wireName) found = &l;
    if (found == nullptr) return false;
    p.layout = found->layout;
  }
  JsonVariantConst rotation = message["rotation"];
  if (!rotation.isNull()) {
    if (!rotation.is<int>()) return false;
    const int degrees = rotation.as<int>();
    if (degrees != 0 && degrees != 90 && degrees != 180 && degrees != 270) return false;
    p.rotation = static_cast<uint16_t>(degrees);
  }
  JsonVariantConst quiet = message["quiet_mode"];
  if (!quiet.isNull()) {
    if (!quiet.is<bool>()) return false;
    p.quietMode = quiet.as<bool>();
  }
  // A duration in ms: false when present but not one.
  auto duration = [&](const char* field, uint32_t& value) {
    JsonVariantConst v = message[field];
    if (v.isNull()) return true;
    if (!v.is<uint32_t>()) return false;
    value = v.as<uint32_t>();
    return true;
  };
  if (!duration("disconnected_timeout_ms", p.disconnectedTimeoutMs) ||
      !duration("escalation_faster_ms", p.escalation.fasterFromMs) ||
      !duration("escalation_takeover_ms", p.escalation.takeoverFromMs) ||
      !duration("escalation_steady_ms", p.escalation.steadyFromMs))
    return false;
  if (p.disconnectedTimeoutMs < kMinDisconnectedTimeoutMs) return false;
  // The steps come in order; two at the same time skip the first of them.
  if (p.escalation.fasterFromMs > p.escalation.takeoverFromMs ||
      p.escalation.takeoverFromMs > p.escalation.steadyFromMs)
    return false;
  out = p;
  return true;
}

// Width and height of the picture on a matrix turned by `rotation` degrees.
bool turnsSideways(uint16_t rotation) { return rotation == 90 || rotation == 270; }

}  // namespace

DeviceCore::DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock,
                       Preferences preferences, PreferencesCache* cache, ThemeStore* store)
    : profile_(std::move(profile)),
      firmwareVersion_(std::move(firmwareVersion)),
      defaults_(preferences),
      clock_(clock),
      cache_(cache),
      store_(store != nullptr ? store : &memoryStore_),
      preferences_(preferences),
      frame_(profile_.width, profile_.height),
      shown_(profile_.width, profile_.height),
      raw_(profile_.width, profile_.height),
      lastHostMessage_(clock.millis()) {
  Preferences cached;
  JsonDocument message;
  if (cache_ != nullptr) {
    const std::string line = cache_->load();
    if (!deserializeJson(message, line) && parsePreferences(message.as<JsonVariantConst>(), defaults_, cached)) {
      preferences_ = cached;
    }
  }
  JsonDocument abstract;
  std::string error;
  lookUp(InstallKind::Theme, kAbstractTheme, abstract);
  parseTheme(abstract.as<JsonVariantConst>(), abstract_, error);
  selectTheme();
  // Defaults that would be refused on the wire are kept as given.
  if (turnsSideways(preferences_.rotation)) {
    frame_ = PixelBuffer(profile_.height, profile_.width);
    raw_ = PixelBuffer(profile_.height, profile_.width);
  }
}

std::string DeviceCore::preferencesLine() const {
  JsonDocument message;
  message["type"] = "preferences";
  message["theme"] = preferences_.theme;
  message["brightness"] = preferences_.brightness;
  for (const auto& l : kLayouts)
    if (l.layout == preferences_.layout) message["layout"] = l.wireName;
  message["rotation"] = preferences_.rotation;
  message["quiet_mode"] = preferences_.quietMode;
  message["disconnected_timeout_ms"] = preferences_.disconnectedTimeoutMs;
  message["escalation_faster_ms"] = preferences_.escalation.fasterFromMs;
  message["escalation_takeover_ms"] = preferences_.escalation.takeoverFromMs;
  message["escalation_steady_ms"] = preferences_.escalation.steadyFromMs;
  std::string line;
  serializeJson(message, line);
  return line;
}

void DeviceCore::applyPreferences(const Preferences& preferences) {
  const std::string before = preferencesLine();
  const bool wasSideways = turnsSideways(preferences_.rotation);
  const bool themeChanged = preferences.theme != preferences_.theme;
  preferences_ = preferences;
  if (themeChanged) selectTheme();
  if (turnsSideways(preferences_.rotation) != wasSideways) {
    // The picture's width and height swap: Raw Mode's pixels no longer fit.
    frame_ = PixelBuffer(frame_.height(), frame_.width());
    raw_ = PixelBuffer(raw_.height(), raw_.width());
  }
  const std::string after = preferencesLine();
  // Only a change is saved: flash wears with every write.
  if (cache_ != nullptr && after != before) cache_->save(after);
}

std::string DeviceCore::receive(std::string_view line) {
  // With its newline, longer than a Device's line buffer: a Device never
  // sees it whole.
  if (line.size() + 1 > kMaxLineLength) return {};
  if (!line.empty() && line[0] == kRawFrameStart) {
    receiveRawFrame(line);
    return {};
  }
  JsonDocument message;
  if (deserializeJson(message, line.data(), line.size())) return {};

  // Every message the Device can use proves the Host is alive.
  std::string_view type = message["type"] | "";
  // hello and heartbeat carry the Host time they were sent at.
  auto noteHostTime = [&] {
    if (message["host_time"].is<int64_t>()) heardHostTime(message["host_time"]);
  };
  // A Host of another protocol version refuses to drive the Device: until a
  // Host it can talk to says hello, nothing else it hears is for it.
  if (updateWanted_ && type != "hello") return {};
  if (type == "hello") {
    // A new link: the Host follows with every session it knows, so sessions
    // from an earlier Host Daemon must not linger, nor its Raw Mode.
    sessions_.clear();
    rawMode_ = false;
    preview_ = false;
    // Every version's hello says which version the Host speaks: another
    // one than the Device's means an update is due (protocol/README.md).
    JsonVariantConst protocol = message["protocol"];
    updateWanted_ = protocol.is<int64_t>() && protocol.as<int64_t>() != kProtocolVersion;
    noteHostTime();
    hostMessage();
    return helloReply();
  } else if (type == "session_upsert") {
    std::string_view id = message["id"] | "";
    const SessionStateInfo* state = findByWireName(message["state"] | "");
    if (id.empty() || state == nullptr) return {};
    auto [it, created] = sessions_.try_emplace(std::string(id));
    Session& session = it->second;
    if (created || session.state != state->state) {
      // A Device claimed again learns how long a session has been in its state.
      session.escalationStart = clockTimeOf(message["state_entered"] | int64_t{0});
      session.settled = false;
    }
    session.state = state->state;
    // Without a slot the session goes to the first free Tile.
    const int tile = message["tile"] | -1;
    session.tile = tile < 0 ? INT_MAX : tile;
    // Without a valid Project Color, a Takeover shows the Session State's.
    session.hasProjectColor = parseColor(message["project_color"] | "", session.projectColor);
  } else if (type == "session_remove") {
    if (!message["id"].is<const char*>()) return {};
    sessions_.erase(std::string(message["id"].as<const char*>()));
  } else if (type == "acknowledge") {
    const uint32_t at = clockTimeOf(message["host_time"] | int64_t{0});
    if (message["id"].isNull()) {
      for (auto& [id, session] : sessions_) acknowledge(session, at);
    } else {
      std::string_view id = message["id"] | "";
      if (id.empty()) return {};
      auto it = sessions_.find(std::string(id));
      if (it != sessions_.end()) acknowledge(it->second, at);
    }
  } else if (type == "heartbeat") {
    noteHostTime();
  } else if (type == "raw_mode_enter") {
    rawMode_ = true;
    raw_.fill(Rgb{});
  } else if (type == "raw_mode_exit") {
    rawMode_ = false;
  } else if (type == "preview_enter") {
    const SessionStateInfo* state = findByWireName(message["state"] | "");
    JsonVariantConst elapsed = message["elapsed_ms"];
    if (state == nullptr || (!elapsed.isNull() && !elapsed.is<uint32_t>())) return {};
    // A lone session that entered the state elapsed_ms ago, in the Session
    // State's own color.
    Session previewed;
    previewed.state = state->state;
    previewed.escalationStart = clock_.millis() - (elapsed | uint32_t{0});
    previewed_.clear();
    previewed_.emplace("preview", previewed);
    preview_ = true;
  } else if (type == "preview_exit") {
    preview_ = false;
  } else if (type == "goodbye") {
    // The Host Daemon exits cleanly: nothing is left to show, and its
    // silence from now on is no Disconnected. A hello starts over.
    sessions_.clear();
    rawMode_ = false;
    preview_ = false;
    hostMessage();
    saidGoodbye_ = true;
    return {};
  } else if (type == "install") {
    hostMessage();
    return install(message.as<JsonVariantConst>());
  } else if (type == "uninstall") {
    hostMessage();
    return uninstall(message.as<JsonVariantConst>());
  } else if (type == "preferences") {
    Preferences received;
    if (!parsePreferences(message.as<JsonVariantConst>(), defaults_, received)) return {};
    applyPreferences(received);
  } else {
    return {};
  }
  hostMessage();
  return {};
}

// A Raw Mode frame is used only in Raw Mode and only when it has exactly one
// pixel per LED of this matrix.
void DeviceCore::receiveRawFrame(std::string_view frame) {
  const size_t w = raw_.width(), h = raw_.height();
  if (!rawMode_ || frame.size() != 1 + w * h * 3) return;
  const auto* rgb = reinterpret_cast<const uint8_t*>(frame.data() + 1);
  for (size_t y = 0; y < h; ++y)
    for (size_t x = 0; x < w; ++x, rgb += 3)
      raw_.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), Rgb{rgb[0], rgb[1], rgb[2]});
  hostMessage();
}

std::string DeviceCore::helloReply() const {
  JsonDocument reply;
  reply["type"] = "hello_reply";
  reply["protocol"] = kProtocolVersion;
  reply["firmware"] = firmwareVersion_;
  JsonObject hw = reply["hardware_profile"].to<JsonObject>();
  hw["width"] = profile_.width;
  hw["height"] = profile_.height;
  hw["data_pin"] = profile_.dataPin;
  hw["serpentine"] = profile_.serpentine;
  hw["color_order"] = profile_.colorOrder;
  hw["brightness_cap"] = profile_.brightnessCap;
  hw["current_cap_ma"] = profile_.currentCapMa;
  hw["button"] = profile_.button;
  hw["imu"] = profile_.imu;
  addAvailable(reply);
  std::string line;
  serializeJson(reply, line);
  return line;
}

// Acknowledge clears Done and restarts Escalation; Awaiting Input stays. An
// Acknowledge older than the Escalation start changes nothing about it.
void DeviceCore::acknowledge(Session& session, uint32_t at) {
  if (session.state == SessionState::Done) session.state = SessionState::Idle;
  const uint32_t now = clock_.millis();
  if (session.settled || now - at < now - session.escalationStart) {
    session.escalationStart = at;
    session.settled = false;
  }
}

void DeviceCore::heardHostTime(int64_t hostTime) {
  hostTimeKnown_ = true;
  hostTime_ = hostTime;
  hostTimeHeardAt_ = clock_.millis();
}

// The clock time a Host time corresponds to; now when the Host time is unknown
// (0) or no Host time has been heard. Times in the Host's future count as now,
// and the distant past as a day ago, past every Escalation step.
uint32_t DeviceCore::clockTimeOf(int64_t hostTime) const {
  constexpr int64_t kLongAgo = 24LL * 60 * 60 * 1000;
  const uint32_t now = clock_.millis();
  if (!hostTimeKnown_ || hostTime <= 0) return now;
  const int64_t hostNow = hostTime_ + static_cast<uint32_t>(now - hostTimeHeardAt_);
  const int64_t age = std::clamp<int64_t>(hostNow - hostTime, 0, kLongAgo);
  return now - static_cast<uint32_t>(age);
}

std::string DeviceCore::acknowledge() {
  for (auto& [id, session] : sessions_) acknowledge(session, clock_.millis());
  return R"({"type":"acknowledge"})";
}

SessionState DeviceCore::aggregateState() const {
  SessionState aggregate = SessionState::Idle;
  for (const auto& [id, session] : sessions_) {
    if (infoFor(session.state).urgency > infoFor(aggregate).urgency) aggregate = session.state;
  }
  return aggregate;
}

// Unsigned subtraction keeps working when millis() wraps (every ~49.7 days).
bool DeviceCore::disconnected() {
  if (saidGoodbye_) return false;
  if (clock_.millis() - lastHostMessage_ >= preferences_.disconnectedTimeoutMs) {
    // The Host that streamed the pixels, or asked for a preview, is gone:
    // Raw Mode and the preview end with it.
    disconnected_ = true;
    rawMode_ = false;
    preview_ = false;
  }
  return disconnected_;
}

void DeviceCore::hostMessage() {
  lastHostMessage_ = clock_.millis();
  disconnected_ = false;
  saidGoodbye_ = false;
}

// Disconnected is not a Session State: a dim slash across an otherwise dark
// matrix, so it never reads as Idle (a full, dim fill) or as any other state.
void DeviceCore::renderDisconnected() {
  constexpr Rgb kDisconnected{20, 0, 28};
  frame_.fill(Rgb{});
  const int w = frame_.width(), h = frame_.height();
  for (int y = 0; y < h; ++y) {
    const int x = (w - 1) - (y * w) / h;
    frame_.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), kDisconnected);
  }
}

// "Update me": an amber arrow pointing up, its head as wide as the matrix
// in the upper half, its shaft down the middle to the bottom row. Nothing
// else the Device shows is an arrow, or this color.
void DeviceCore::renderUpdateMe() {
  constexpr Rgb kUpdateMe{255, 110, 0};
  frame_.fill(Rgb{});
  const int w = frame_.width(), h = frame_.height();
  const int left = (w - 1) / 2, right = w / 2;  // the middle column, or the two of an even width
  const int headRows = std::max(1, std::min((w + 1) / 2, h / 2));
  for (int y = 0; y < h; ++y) {
    const int spread = y < headRows ? y : 0;
    for (int x = std::max(0, left - spread); x <= std::min(w - 1, right + spread); ++x) {
      frame_.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), kUpdateMe);
    }
  }
}

// Where Escalation is for a session Awaiting Input. Latches the steady glow.
DeviceCore::EscalationStep DeviceCore::escalationStep(Session& session) {
  if (session.state != SessionState::AwaitingInput) return {};
  const uint32_t unacknowledged = clock_.millis() - session.escalationStart;
  if (session.settled || unacknowledged >= preferences_.escalation.steadyFromMs) {
    session.settled = true;
    return {EscalationStep::Steady, 0};
  }
  if (unacknowledged >= preferences_.escalation.takeoverFromMs) return {EscalationStep::Takeover, 0};
  if (unacknowledged >= preferences_.escalation.fasterFromMs)
    return {EscalationStep::Faster, unacknowledged - preferences_.escalation.fasterFromMs};
  return {EscalationStep::Gentle, unacknowledged};
}

namespace {

// How many Tiles the adaptive Layout has room for with n sessions; with nine
// or more the eighth column is the Overflow Tile, so seven.
constexpr int kOverflowTile = 7;
int tileRoom(size_t n) {
  if (n <= 1) return 1;
  if (n == 2) return 2;
  if (n <= 4) return 4;
  if (n <= 8) return 8;
  return kOverflowTile;
}

}  // namespace

// The brightness (of 255) Awaiting Input shows at an Escalation step: each
// pulse starts at its dimmest when its step starts.
int DeviceCore::awaitingInputLevel(EscalationStep step) {
  switch (step.kind) {
    case EscalationStep::Faster:
      return kFasterPulse.level(step.elapsed);
    case EscalationStep::Gentle:
      return kGentlePulse.level(step.elapsed);
    default:
      return kSteadyGlow;
  }
}

void DeviceCore::drawState(Region region, SessionState state, Rgb color, bool wholeMatrix) {
  const size_t i = stateIndex(state);
  const uint32_t now = clock_.millis();
  const Pose& pose = poses_[i];
  if (wholeMatrix && !pose.frames.empty()) {
    const std::vector<uint8_t>& pixels = pose.frames[(now / std::max<uint32_t>(pose.frameMs, 1)) % pose.frames.size()];
    drawSprite(frame_, pixels, pose.width, pose.height, theme_.states[i].palette);
    return;
  }
  // A Character Theme's pose needs the full matrix: elsewhere, and when its
  // Sprite Pack is missing, the abstract Theme's effect stands in.
  const ThemeState& shown = theme_.states[i];
  drawEffect(frame_, region, shown.usesSprite ? abstract_.states[i].effect : shown.effect, color, now);
}

namespace {

// Dims `region` of `frame` to `level` (of 255): Awaiting Input's Escalation.
void dim(PixelBuffer& frame, Region region, int level) {
  for (int y = region.y0; y < region.y1; ++y)
    for (int x = region.x0; x < region.x1; ++x) {
      const auto px = static_cast<uint8_t>(x), py = static_cast<uint8_t>(y);
      frame.set(px, py, scaled(frame.at(px, py), level));
    }
}

}  // namespace

// The whole matrix shows the Aggregate State as the Theme does, a pose of a
// Character Theme included. Awaiting Input shows the most insistent step any
// Awaiting Input session has reached: the faster pulse, then the gentle
// pulse, then the steady glow, each dimming the Theme's picture.
void DeviceCore::renderAggregate(EscalationStep mostInsistent) {
  const SessionState aggregate = aggregateState();
  frame_.fill(Rgb{});
  // Quiet Mode shows nothing but Awaiting Input.
  if (preferences_.quietMode && aggregate != SessionState::AwaitingInput) return;
  const Region whole{0, 0, frame_.width(), frame_.height()};
  drawState(whole, aggregate, theme_[aggregate].palette[0], true);
  if (aggregate == SessionState::AwaitingInput) dim(frame_, whole, awaitingInputLevel(mostInsistent));
}

// One Tile per session where its Tile slot says, when the slot fits the
// Layout and no other session holds it; the others take the free Tiles in slot
// order, and those left over go to the Overflow Tile.
void DeviceCore::renderTiles() {
  const int room = tileRoom(sessions_.size());
  std::vector<std::pair<const std::string*, Session*>> byTile;
  for (auto& [id, session] : sessions_) byTile.push_back({&id, &session});
  std::stable_sort(byTile.begin(), byTile.end(),
                   [](const auto& a, const auto& b) { return a.second->tile < b.second->tile; });
  std::vector<Session*> tiles(room, nullptr);
  std::vector<Session*> unplaced;
  for (auto& [id, session] : byTile) {
    if (session->tile < room && tiles[session->tile] == nullptr) {
      tiles[session->tile] = session;
    } else {
      unplaced.push_back(session);
    }
  }
  std::vector<Session*> overflow;
  for (Session* session : unplaced) {
    auto free = std::find(tiles.begin(), tiles.end(), nullptr);
    if (free != tiles.end()) {
      *free = session;
    } else {
      overflow.push_back(session);
    }
  }

  // Halves side by side, quadrants, then columns.
  const int columns = room == 1 ? 1 : room <= 4 ? 2 : 8;
  const int rows = room == 4 ? 2 : 1;
  const int w = frame_.width(), h = frame_.height();
  auto region = [&](int i) {
    const int col = i % columns, row = i / columns;
    return Region{col * w / columns, row * h / rows, (col + 1) * w / columns, (row + 1) * h / rows};
  };
  frame_.fill(Rgb{});
  for (int i = 0; i < room; ++i) {
    if (tiles[i] != nullptr)
      renderTile(region(i), tiles[i]->state, colorOf(*tiles[i]), escalationStep(*tiles[i]));
  }
  if (overflow.empty()) return;
  // The Overflow Tile stands for several projects: it shows the Aggregate
  // State of its sessions in the Session State's own color.
  SessionState aggregate = SessionState::Idle;
  EscalationStep mostInsistent;
  for (Session* session : overflow) {
    if (infoFor(session->state).urgency > infoFor(aggregate).urgency) aggregate = session->state;
    const EscalationStep step = escalationStep(*session);
    if (step.kind > mostInsistent.kind) mostInsistent = step;
  }
  renderTile(region(kOverflowTile), aggregate, theme_[aggregate].palette[0], mostInsistent);
}

// A Tile in `color`, animated by its Session State's effect in the Theme
// (the abstract Theme's under a Character Theme).
void DeviceCore::renderTile(Region region, SessionState state, Rgb color, EscalationStep step) {
  // Quiet Mode leaves dark every Tile but those Awaiting Input.
  if (preferences_.quietMode && state != SessionState::AwaitingInput) return;
  drawState(region, state, color, false);
  if (state == SessionState::AwaitingInput) dim(frame_, region, awaitingInputLevel(step));
}

Rgb DeviceCore::colorOf(const Session& session) const {
  return session.hasProjectColor ? session.projectColor : theme_[session.state].palette[0];
}

// Disconnected wins over everything; then the sessions while one is Awaiting
// Input, which pre-empts both the preview and Raw Mode (ADR-0001); then a
// preview, which shows its lone session in place of the Device's sessions and
// Raw Mode; then Raw Mode; then the sessions.
void DeviceCore::renderFrame() {
  if (updateWanted_) {
    renderUpdateMe();
    return;
  }
  if (disconnected()) {
    renderDisconnected();
    return;
  }
  const bool awaitingInput = std::any_of(sessions_.begin(), sessions_.end(), [](const auto& entry) {
    return entry.second.state == SessionState::AwaitingInput;
  });
  if (preview_ && !awaitingInput) {
    // O(1) swaps: no allocation per frame.
    sessions_.swap(previewed_);
    renderSessions();
    sessions_.swap(previewed_);
    return;
  }
  if (rawMode_ && !awaitingInput) {
    frame_ = raw_;
    return;
  }
  renderSessions();
}

// A Takeover of any session takes the whole matrix whatever the Layout,
// cycling between the Project Colors of every session in Takeover. Otherwise
// one session, or the Aggregate Layout, shows the Aggregate State on the
// whole matrix, and several sessions get Tiles.
void DeviceCore::renderSessions() {
  if (sessions_.empty()) {
    frame_.fill(Rgb{});
    return;
  }
  std::vector<Rgb> takeover;  // the Project Colors in Takeover, in session id order
  EscalationStep mostInsistent;
  for (auto& [id, session] : sessions_) {
    const EscalationStep step = escalationStep(session);
    if (step.kind == EscalationStep::Takeover) takeover.push_back(colorOf(session));
    if (step.kind > mostInsistent.kind) mostInsistent = step;
  }
  if (!takeover.empty()) {
    const uint32_t cycle = std::max<uint32_t>(preferences_.escalation.takeoverCycleMs, 1);
    frame_.fill(takeover[(clock_.millis() / cycle) % takeover.size()]);
  } else if (preferences_.layout == Layout::Aggregate || sessions_.size() == 1) {
    renderAggregate(mostInsistent);
  } else {
    renderTiles();
  }
}

const PixelBuffer& DeviceCore::render() {
  renderFrame();
  return present();
}

// The rotation turns the picture clockwise on the matrix, and every LED stays
// under the Hardware Profile's brightness cap, scaled by the brightness
// Preference. A lit channel never rounds down to off, so dim patterns
// (Disconnected, an Idle Tile) stay visible on a dim Device.
const PixelBuffer& DeviceCore::present() {
  // At least 1 under any cap above 0, so the lowest brightness isn't off.
  const int level = profile_.brightnessCap == 0 ? 0 : std::max(1, profile_.brightnessCap * preferences_.brightness / 100);
  auto channel = [level](uint8_t c) {
    const int v = c * level / 255;
    return static_cast<uint8_t>(v == 0 && c > 0 && level > 0 ? 1 : v);
  };
  const int w = shown_.width(), h = shown_.height();
  for (int y = 0; y < frame_.height(); ++y) {
    for (int x = 0; x < frame_.width(); ++x) {
      int px = x, py = y;
      switch (preferences_.rotation) {
        case 90: px = w - 1 - y; py = x; break;
        case 180: px = w - 1 - x; py = h - 1 - y; break;
        case 270: px = y; py = h - 1 - x; break;
        default: break;
      }
      const Rgb c = frame_.at(static_cast<uint8_t>(x), static_cast<uint8_t>(y));
      shown_.set(static_cast<uint8_t>(px), static_cast<uint8_t>(py), Rgb{channel(c.r), channel(c.g), channel(c.b)});
    }
  }
  return shown_;
}

}  // namespace device
