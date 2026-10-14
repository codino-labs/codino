// Themes and Sprite Packs: declarative data (protocol/themes.md) the Device
// core interprets. A Theme gives each Session State a palette plus either a
// parameterised procedural effect or Sprite Pack frames with timing.
#pragma once

#include <ArduinoJson.h>

#include <array>
#include <cstdint>
#include <map>
#include <string>
#include <string_view>
#include <vector>

#include "pixels.h"

namespace device {

constexpr size_t kSessionStateCount = 5;
// The position of a Session State in a Theme's per-state arrays.
size_t stateIndex(SessionState state);
// The wire name of a Session State: "idle", "awaiting_input"...
const char* wireName(SessionState state);

// Limits that keep Themes and Sprite Packs small enough for a Device's flash
// and RAM (protocol/themes.md).
constexpr size_t kMaxNameLength = 32;
constexpr size_t kMaxPaletteColors = 16;
constexpr uint8_t kMaxSpriteSize = 16;
constexpr size_t kMaxSpriteFrames = 32;
constexpr size_t kMaxFramesPerState = 8;
constexpr uint32_t kMinFrameMs = 20;

// True for a valid Theme or Sprite Pack name: 1-32 of a-z, 0-9, '-' and '_',
// so a name is safe as a file name too.
bool isValidName(std::string_view name);

// A built-in procedural effect, drawn over a region in one color (the
// palette's first color, or the Project Color in a Tile). Levels are a
// fraction of that color, out of 255.
struct Effect {
  enum Kind { Steady, Pulse, Breathe, Blink, Scroll } kind = Steady;
  // Pulse and breathe: one cycle from `low` up to `high` and back; blink: on
  // for half of it, off for the other half; scroll: how long the line stays
  // on each row or column.
  uint32_t periodMs = 1000;
  // Steady: `high`. Pulse, breathe: the range. Blink: on at `high`.
  // Scroll: `low` everywhere but the moving line at `high`.
  uint8_t low = 0, high = 255;
  enum Direction { Down, Up, Right, Left } direction = Down;
};

// Frames of a Sprite Pack shown one after another, each for `frameMs`.
struct SpriteRef {
  std::string pack;
  std::vector<std::string> frames;
  uint32_t frameMs = 500;
};

// How a Theme shows one Session State.
struct ThemeState {
  // The first color is the Session State's own color: effects draw in it,
  // and so do the Overflow Tile and a Takeover without a Project Color.
  // Sprite pixels index into the whole palette.
  std::vector<Rgb> palette;
  bool usesSprite = false;
  Effect effect;     // when !usesSprite
  SpriteRef sprite;  // when usesSprite
};

struct Theme {
  std::string name;
  std::array<ThemeState, kSessionStateCount> states;

  const ThemeState& operator[](SessionState s) const { return states[stateIndex(s)]; }
};

// Pixel-art frames, each `width` x `height` palette indices (row-major from
// the top-left); kUnlit for an LED that stays off.
struct SpritePack {
  static constexpr uint8_t kUnlit = 0xFF;
  std::string name;
  uint8_t width = 0, height = 0;
  std::map<std::string, std::vector<uint8_t>> frames;
};

// Reads the Theme or Sprite Pack of an `install` message (or a built-in's
// JSON). False, with why in `error`, when it isn't a valid one.
bool parseTheme(JsonVariantConst message, Theme& theme, std::string& error);
bool parseSpritePack(JsonVariantConst message, SpritePack& pack, std::string& error);

// A region of the matrix: columns [x0, x1), rows [y0, y1).
struct Region {
  int x0, y0, x1, y1;
};

// Draws `effect` in `color` over `region` of `frame` at clock time `now`.
void drawEffect(PixelBuffer& frame, Region region, const Effect& effect, Rgb color, uint32_t now);

// Draws one frame of a sprite (palette indices, `width` x `height`), scaled
// up by the largest whole factor that fits the frame and centered on it;
// a sprite larger than the frame is cropped around its center. Indices
// beyond the palette, like unlit ones, leave the LED as it is.
void drawSprite(PixelBuffer& frame, const std::vector<uint8_t>& pixels, uint8_t width, uint8_t height,
                const std::vector<Rgb>& palette);

// Parses a color, "#rrggbb". False when it isn't one.
bool parseColor(std::string_view text, Rgb& color);

// `color` at `level` of its full brightness (255 = unchanged).
Rgb scaled(Rgb color, int level);

// What a Device can install: a Theme, or a Sprite Pack a Theme draws from.
enum class InstallKind { Theme, SpritePack };
// "theme" or "sprite_pack", as in an `install` message.
const char* wireName(InstallKind kind);
// False when `name` is no kind's wire name.
bool parseInstallKind(std::string_view name, InstallKind& kind);

// The Themes or Sprite Packs built into the firmware, as JSON in the format
// of an `install` message (protocol/themes.md).
struct BuiltIn {
  const char* name;
  const char* json;
};
struct BuiltIns {
  const BuiltIn* first;
  size_t count;
  const BuiltIn* begin() const { return first; }
  const BuiltIn* end() const { return first + count; }
};
BuiltIns builtIns(InstallKind kind);
// The built-in one of that name; null when there is none.
const BuiltIn* findBuiltIn(InstallKind kind, std::string_view name);
// The default Character Theme, and the abstract Theme whose effects Tiles
// fall back to when the selected Theme is a Character Theme.
constexpr const char* kDefaultTheme = "default";
constexpr const char* kAbstractTheme = "minimal";

}  // namespace device
