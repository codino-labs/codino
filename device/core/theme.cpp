#include "theme.h"

#include <algorithm>

namespace device {

namespace {

constexpr SessionState kStates[kSessionStateCount] = {SessionState::Idle, SessionState::Working,
                                                      SessionState::AwaitingInput, SessionState::Done,
                                                      SessionState::Error};
constexpr const char* kWireNames[kSessionStateCount] = {"idle", "working", "awaiting_input", "done", "error"};

bool fail(std::string& error, std::string why) {
  error = std::move(why);
  return false;
}

// Reads an optional integer field in [min, max] into `out`.
template <typename T>
bool optionalInt(JsonVariantConst object, const char* field, uint32_t min, uint32_t max, T& out, std::string& error,
                 const std::string& where) {
  JsonVariantConst v = object[field];
  if (v.isNull()) return true;
  if (!v.is<uint32_t>() || v.as<uint32_t>() < min || v.as<uint32_t>() > max)
    return fail(error, where + ": " + field + " must be an integer from " + std::to_string(min) + " to " +
                           std::to_string(max));
  out = static_cast<T>(v.as<uint32_t>());
  return true;
}

struct EffectName {
  Effect::Kind kind;
  const char* name;
};
constexpr EffectName kEffects[] = {{Effect::Steady, "steady"},
                                   {Effect::Pulse, "pulse"},
                                   {Effect::Breathe, "breathe"},
                                   {Effect::Blink, "blink"},
                                   {Effect::Scroll, "scroll"}};

struct DirectionName {
  Effect::Direction direction;
  const char* name;
};
constexpr DirectionName kDirections[] = {
    {Effect::Down, "down"}, {Effect::Up, "up"}, {Effect::Right, "right"}, {Effect::Left, "left"}};

bool parseEffect(JsonVariantConst object, Effect& effect, std::string& error, const std::string& where) {
  if (!object.is<JsonObjectConst>()) return fail(error, where + ": effect must be an object");
  std::string_view name = object["name"] | "";
  const EffectName* found = nullptr;
  for (const auto& e : kEffects)
    if (name == e.name) found = &e;
  if (found == nullptr) return fail(error, where + ": effect name must be steady, pulse, breathe, blink or scroll");
  effect = Effect{};
  effect.kind = found->kind;
  if (effect.kind == Effect::Scroll) effect.periodMs = 125;
  if (effect.kind == Effect::Pulse || effect.kind == Effect::Breathe) effect.periodMs = 2000;
  if (!optionalInt(object, "period_ms", 2, 60000, effect.periodMs, error, where) ||
      !optionalInt(object, "low", 0, 255, effect.low, error, where) ||
      !optionalInt(object, "high", 0, 255, effect.high, error, where) ||
      !optionalInt(object, "level", 0, 255, effect.high, error, where))
    return false;
  if (!object["level"].isNull() && !object["high"].isNull())
    return fail(error, where + ": give level or high, not both");
  JsonVariantConst direction = object["direction"];
  if (!direction.isNull()) {
    const DirectionName* d = nullptr;
    for (const auto& candidate : kDirections)
      if (direction.is<const char*>() && std::string_view(direction.as<const char*>()) == candidate.name)
        d = &candidate;
    if (d == nullptr) return fail(error, where + ": direction must be down, up, right or left");
    effect.direction = d->direction;
  }
  return true;
}

bool parseState(JsonVariantConst object, ThemeState& state, std::string& error, const std::string& where) {
  if (!object.is<JsonObjectConst>()) return fail(error, where + " is missing");
  JsonArrayConst palette = object["palette"];
  if (palette.isNull() || palette.size() < 1 || palette.size() > kMaxPaletteColors)
    return fail(error, where + ": palette must hold 1 to 16 colors");
  state.palette.clear();
  for (JsonVariantConst c : palette) {
    Rgb color;
    if (!c.is<const char*>() || !parseColor(c.as<const char*>(), color))
      return fail(error, where + ": palette colors are #rrggbb");
    state.palette.push_back(color);
  }
  const bool hasEffect = !object["effect"].isNull(), hasSprite = !object["sprite"].isNull();
  if (hasEffect == hasSprite) return fail(error, where + ": give either an effect or a sprite");
  state.usesSprite = hasSprite;
  if (hasEffect) return parseEffect(object["effect"], state.effect, error, where);

  JsonVariantConst sprite = object["sprite"];
  std::string_view pack = sprite["pack"] | "";
  if (!isValidName(pack)) return fail(error, where + ": sprite pack must be a Sprite Pack name");
  state.sprite = SpriteRef{};
  state.sprite.pack = std::string(pack);
  JsonArrayConst frames = sprite["frames"];
  if (frames.isNull() || frames.size() < 1 || frames.size() > kMaxFramesPerState)
    return fail(error, where + ": sprite frames must name 1 to 8 frames");
  for (JsonVariantConst f : frames) {
    if (!f.is<const char*>() || !isValidName(f.as<const char*>()))
      return fail(error, where + ": sprite frames must be frame names");
    state.sprite.frames.emplace_back(f.as<const char*>());
  }
  return optionalInt(sprite, "frame_ms", kMinFrameMs, 60000, state.sprite.frameMs, error, where);
}

// A triangle wave from `low` up to `high` and back over `period`, `t` in.
int triangle(uint32_t t, uint32_t period, int low, int high) {
  const uint32_t half = std::max<uint32_t>(period / 2, 1);
  const uint32_t phase = t % (2 * half);
  const uint32_t up = phase < half ? phase : 2 * half - phase;
  return low + static_cast<int>((high - low) * static_cast<int64_t>(up) / half);
}

// Like triangle, eased in and out (smoothstep), so it lingers at both ends.
int breathe(uint32_t t, uint32_t period, int low, int high) {
  const uint32_t half = std::max<uint32_t>(period / 2, 1);
  const uint32_t phase = t % (2 * half);
  const int64_t up = phase < half ? phase : 2 * half - phase;  // 0..half
  const int64_t u = up * 1000 / half;                           // 0..1000
  const int64_t eased = u * u * (3000 - 2 * u) / 1000000;       // 0..1000
  return low + static_cast<int>((high - low) * eased / 1000);
}

}  // namespace

const char* wireName(InstallKind kind) { return kind == InstallKind::Theme ? "theme" : "sprite_pack"; }

bool parseInstallKind(std::string_view name, InstallKind& kind) {
  for (InstallKind k : {InstallKind::Theme, InstallKind::SpritePack})
    if (name == wireName(k)) {
      kind = k;
      return true;
    }
  return false;
}

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

size_t stateIndex(SessionState state) {
  for (size_t i = 0; i < kSessionStateCount; ++i)
    if (kStates[i] == state) return i;
  return 0;
}

const char* wireName(SessionState state) { return kWireNames[stateIndex(state)]; }

bool isValidName(std::string_view name) {
  if (name.empty() || name.size() > kMaxNameLength) return false;
  return std::all_of(name.begin(), name.end(), [](char c) {
    return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_';
  });
}

bool parseTheme(JsonVariantConst message, Theme& theme, std::string& error) {
  std::string_view name = message["name"] | "";
  if (!isValidName(name)) return fail(error, "name must be 1-32 of a-z, 0-9, - and _");
  Theme parsed;
  parsed.name = std::string(name);
  JsonVariantConst states = message["states"];
  if (!states.is<JsonObjectConst>()) return fail(error, "states must be an object");
  for (size_t i = 0; i < kSessionStateCount; ++i) {
    const std::string where = std::string("states.") + kWireNames[i];
    if (!parseState(states[kWireNames[i]], parsed.states[i], error, where)) return false;
  }
  theme = std::move(parsed);
  return true;
}

bool parseSpritePack(JsonVariantConst message, SpritePack& pack, std::string& error) {
  std::string_view name = message["name"] | "";
  if (!isValidName(name)) return fail(error, "name must be 1-32 of a-z, 0-9, - and _");
  SpritePack parsed;
  parsed.name = std::string(name);
  if (!optionalInt(message, "width", 1, kMaxSpriteSize, parsed.width, error, "sprite pack") ||
      !optionalInt(message, "height", 1, kMaxSpriteSize, parsed.height, error, "sprite pack"))
    return false;
  if (parsed.width == 0 || parsed.height == 0) return fail(error, "sprite pack: width and height are required");
  JsonObjectConst frames = message["frames"];
  if (frames.isNull() || frames.size() < 1 || frames.size() > kMaxSpriteFrames)
    return fail(error, "frames must hold 1 to 32 frames");
  for (JsonPairConst frame : frames) {
    const std::string frameName = frame.key().c_str();
    const std::string where = "frames." + frameName;
    if (!isValidName(frameName)) return fail(error, where + ": frame names are 1-32 of a-z, 0-9, - and _");
    JsonArrayConst rows = frame.value();
    if (rows.isNull() || rows.size() != parsed.height)
      return fail(error, where + ": give " + std::to_string(parsed.height) + " rows");
    std::vector<uint8_t> pixels;
    for (JsonVariantConst row : rows) {
      std::string_view text = row | "";
      if (text.size() != parsed.width)
        return fail(error, where + ": every row is " + std::to_string(parsed.width) + " characters");
      for (char c : text) {
        if (c == '.') pixels.push_back(SpritePack::kUnlit);
        else if (c >= '0' && c <= '9') pixels.push_back(static_cast<uint8_t>(c - '0'));
        else if (c >= 'a' && c <= 'f') pixels.push_back(static_cast<uint8_t>(c - 'a' + 10));
        else return fail(error, where + ": pixels are '.' (unlit) or a palette index 0-9, a-f");
      }
    }
    parsed.frames[frameName] = std::move(pixels);
  }
  pack = std::move(parsed);
  return true;
}

Rgb scaled(Rgb color, int level) {
  auto channel = [level](uint8_t c) { return static_cast<uint8_t>(c * level / 255); };
  return {channel(color.r), channel(color.g), channel(color.b)};
}

void drawEffect(PixelBuffer& frame, Region region, const Effect& effect, Rgb color, uint32_t now) {
  const int w = std::max(region.x1 - region.x0, 1), h = std::max(region.y1 - region.y0, 1);
  const uint32_t period = std::max<uint32_t>(effect.periodMs, 1);
  for (int y = region.y0; y < region.y1; ++y) {
    for (int x = region.x0; x < region.x1; ++x) {
      int level = effect.high;
      switch (effect.kind) {
        case Effect::Steady:
          break;
        case Effect::Pulse:
          level = triangle(now, period, effect.low, effect.high);
          break;
        case Effect::Breathe:
          level = breathe(now, period, effect.low, effect.high);
          break;
        case Effect::Blink:
          level = (now / std::max<uint32_t>(period / 2, 1)) % 2 == 0 ? effect.high : 0;
          break;
        case Effect::Scroll: {
          const bool vertical = effect.direction == Effect::Down || effect.direction == Effect::Up;
          const int extent = vertical ? h : w;
          int line = static_cast<int>((now / period) % extent);
          if (effect.direction == Effect::Up || effect.direction == Effect::Left) line = extent - 1 - line;
          const int at = vertical ? y - region.y0 : x - region.x0;
          level = at == line ? effect.high : effect.low;
          break;
        }
      }
      frame.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), scaled(color, level));
    }
  }
}

void drawSprite(PixelBuffer& frame, const std::vector<uint8_t>& pixels, uint8_t width, uint8_t height,
                const std::vector<Rgb>& palette) {
  const int fw = frame.width(), fh = frame.height();
  const int scale = std::max(1, std::min(fw / width, fh / height));
  const int x0 = (fw - width * scale) / 2, y0 = (fh - height * scale) / 2;
  for (int sy = 0; sy < height; ++sy) {
    for (int sx = 0; sx < width; ++sx) {
      const uint8_t index = pixels[static_cast<size_t>(sy) * width + sx];
      if (index >= palette.size()) continue;
      for (int dy = 0; dy < scale; ++dy)
        for (int dx = 0; dx < scale; ++dx) {
          const int x = x0 + sx * scale + dx, y = y0 + sy * scale + dy;
          if (x >= 0 && x < fw && y >= 0 && y < fh)
            frame.set(static_cast<uint8_t>(x), static_cast<uint8_t>(y), palette[index]);
        }
    }
  }
}

}  // namespace device
