#include "device_core.h"

#include <ArduinoJson.h>

#include <algorithm>
#include <climits>

namespace device {

namespace {

// Everything the core knows about each Session State, in one place.
// `urgency`: higher wins the Aggregate State (Awaiting Input > Error > Done >
// Working > Idle). `color`: the v0 solid color for the whole matrix.
struct SessionStateInfo {
  SessionState state;
  const char* wireName;
  int urgency;
  Rgb color;
};

constexpr SessionStateInfo kSessionStates[] = {
    {SessionState::Idle, "idle", 0, {16, 16, 24}},
    {SessionState::Working, "working", 1, {0, 90, 255}},
    {SessionState::Done, "done", 2, {0, 200, 60}},
    {SessionState::Error, "error", 3, {255, 0, 0}},
    {SessionState::AwaitingInput, "awaiting_input", 4, {255, 150, 0}},
};

const SessionStateInfo* findByWireName(std::string_view name) {
  for (const auto& info : kSessionStates)
    if (name == info.wireName) return &info;
  return nullptr;
}

const SessionStateInfo& infoFor(SessionState state) {
  for (const auto& info : kSessionStates)
    if (info.state == state) return info;
  return kSessionStates[0];
}

// Parses a Project Color, "#rrggbb". False when it isn't one.
bool parseColor(std::string_view text, Rgb& color) {
  if (text.size() != 7 || text[0] != '#') return false;
  int channels[3];
  for (int i = 0; i < 3; ++i) {
    int value = 0;
    for (char c : text.substr(1 + 2 * i, 2)) {
      int digit;
      if (c >= '0' && c <= '9') digit = c - '0';
      else if (c >= 'a' && c <= 'f') digit = c - 'a' + 10;
      else if (c >= 'A' && c <= 'F') digit = c - 'A' + 10;
      else return false;
      value = value * 16 + digit;
    }
    channels[i] = value;
  }
  color = {static_cast<uint8_t>(channels[0]), static_cast<uint8_t>(channels[1]), static_cast<uint8_t>(channels[2])};
  return true;
}

// `color` at `level` of its full brightness (255 = unchanged).
Rgb scaled(Rgb color, int level) {
  auto channel = [level](uint8_t c) { return static_cast<uint8_t>(c * level / 255); };
  return {channel(color.r), channel(color.g), channel(color.b)};
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

}  // namespace

DeviceCore::DeviceCore(HardwareProfile profile, std::string firmwareVersion, const Clock& clock,
                       Escalation escalation, Layout layout)
    : profile_(std::move(profile)),
      firmwareVersion_(std::move(firmwareVersion)),
      clock_(clock),
      escalation_(escalation),
      layout_(layout),
      frame_(profile_.width, profile_.height),
      lastHostMessage_(clock.millis()) {}

std::string DeviceCore::receive(std::string_view line) {
  JsonDocument message;
  if (deserializeJson(message, line.data(), line.size())) return {};

  // Every message the Device can use proves the Host is alive.
  std::string_view type = message["type"] | "";
  // hello and heartbeat carry the Host time they were sent at.
  auto noteHostTime = [&] {
    if (message["host_time"].is<int64_t>()) heardHostTime(message["host_time"]);
  };
  if (type == "hello") {
    // A new link: the Host follows with every session it knows, so sessions
    // from an earlier Host Daemon must not linger.
    sessions_.clear();
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
    if (!parseColor(message["project_color"] | "", session.projectColor)) session.projectColor = state->color;
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
  } else {
    return {};
  }
  hostMessage();
  return {};
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
  if (clock_.millis() - lastHostMessage_ >= kDefaultDisconnectedTimeoutMs) disconnected_ = true;
  return disconnected_;
}

void DeviceCore::hostMessage() {
  lastHostMessage_ = clock_.millis();
  disconnected_ = false;
}

// Disconnected is not a Session State: a dim slash across an otherwise dark
// matrix, so it never reads as Idle (a full, dim fill) or as any other state.
void DeviceCore::renderDisconnected() {
  constexpr Rgb kDisconnected{20, 0, 28};
  frame_.fill(Rgb{});
  const int w = profile_.width, h = profile_.height;
  for (int y = 0; y < h; ++y) {
    const int x = (w - 1) - (y * w) / h;
    frame_.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), kDisconnected);
  }
}

// Where Escalation is for a session Awaiting Input. Latches the steady glow.
DeviceCore::EscalationStep DeviceCore::escalationStep(Session& session) {
  if (session.state != SessionState::AwaitingInput) return {};
  const uint32_t unacknowledged = clock_.millis() - session.escalationStart;
  if (session.settled || unacknowledged >= escalation_.steadyFromMs) {
    session.settled = true;
    return {EscalationStep::Steady, 0};
  }
  if (unacknowledged >= escalation_.takeoverFromMs) return {EscalationStep::Takeover, 0};
  if (unacknowledged >= escalation_.fasterFromMs)
    return {EscalationStep::Faster, unacknowledged - escalation_.fasterFromMs};
  return {EscalationStep::Gentle, unacknowledged};
}

namespace {

// The brightness (of 255) Awaiting Input shows at an Escalation step: each
// pulse starts at its dimmest when its step starts.
template <typename Step>
int awaitingInputLevel(Step step) {
  switch (step.kind) {
    case Step::Faster:
      return kFasterPulse.level(step.elapsed);
    case Step::Gentle:
      return kGentlePulse.level(step.elapsed);
    default:
      return kSteadyGlow;
  }
}

// Tile animations, in the session's Project Color.
constexpr int kTileIdle = 32;            // a dim, steady glow
constexpr int kTileWorkingBase = 80;     // Working: a bright row runs down a dimmer Tile
constexpr uint32_t kTileWorkingRowMs = 125;
constexpr uint32_t kTileErrorBlinkMs = 500;  // Error: on, then off, this long each

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

// The whole matrix shows the Aggregate State. Awaiting Input shows the most
// insistent step any Awaiting Input session has reached: the faster pulse,
// then the gentle pulse, then the steady glow.
void DeviceCore::renderAggregate(EscalationStep mostInsistent) {
  const SessionState aggregate = aggregateState();
  const Rgb color = infoFor(aggregate).color;
  if (aggregate == SessionState::AwaitingInput) {
    frame_.fill(scaled(color, awaitingInputLevel(mostInsistent)));
  } else {
    frame_.fill(color);
  }
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
  const int w = profile_.width, h = profile_.height;
  auto region = [&](int i) {
    const int col = i % columns, row = i / columns;
    return Region{col * w / columns, row * h / rows, (col + 1) * w / columns, (row + 1) * h / rows};
  };
  frame_.fill(Rgb{});
  for (int i = 0; i < room; ++i) {
    if (tiles[i] != nullptr)
      renderTile(region(i), tiles[i]->state, tiles[i]->projectColor, escalationStep(*tiles[i]));
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
  renderTile(region(kOverflowTile), aggregate, infoFor(aggregate).color, mostInsistent);
}

// A Tile in `color`, animated by `state`.
void DeviceCore::renderTile(Region region, SessionState state, Rgb color, EscalationStep step) {
  const uint32_t now = clock_.millis();
  const int height = std::max(region.y1 - region.y0, 1);
  const int workingRow = region.y0 + static_cast<int>((now / kTileWorkingRowMs) % height);
  for (int y = region.y0; y < region.y1; ++y) {
    Rgb pixel;
    switch (state) {
      case SessionState::Idle:
        pixel = scaled(color, kTileIdle);
        break;
      case SessionState::Working:
        pixel = y == workingRow ? color : scaled(color, kTileWorkingBase);
        break;
      case SessionState::Done:
        pixel = color;
        break;
      case SessionState::Error:
        pixel = (now / kTileErrorBlinkMs) % 2 == 0 ? color : Rgb{};
        break;
      case SessionState::AwaitingInput:
        pixel = scaled(color, awaitingInputLevel(step));
        break;
    }
    for (int x = region.x0; x < region.x1; ++x) frame_.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), pixel);
  }
}

// A Takeover of any session takes the whole matrix whatever the Layout,
// cycling between the Project Colors of every session in Takeover. Otherwise
// one session, or the Aggregate Layout, shows the Aggregate State on the
// whole matrix, and several sessions get Tiles.
const PixelBuffer& DeviceCore::render() {
  if (disconnected()) {
    renderDisconnected();
    return frame_;
  }
  if (sessions_.empty()) {
    frame_.fill(Rgb{});
    return frame_;
  }
  std::vector<Rgb> takeover;  // the Project Colors in Takeover, in session id order
  EscalationStep mostInsistent;
  for (auto& [id, session] : sessions_) {
    const EscalationStep step = escalationStep(session);
    if (step.kind == EscalationStep::Takeover) takeover.push_back(session.projectColor);
    if (step.kind > mostInsistent.kind) mostInsistent = step;
  }
  if (!takeover.empty()) {
    const uint32_t cycle = std::max<uint32_t>(escalation_.takeoverCycleMs, 1);
    frame_.fill(takeover[(clock_.millis() / cycle) % takeover.size()]);
  } else if (layout_ == Layout::Aggregate || sessions_.size() == 1) {
    renderAggregate(mostInsistent);
  } else {
    renderTiles();
  }
  return frame_;
}

}  // namespace device
