// The Themes and Sprite Pack built into the firmware, as JSON in the format of
// an `install` message (protocol/themes.md), parsed by the same code as an
// uploaded Theme.
//
// "default" is the Character Theme: Nino, Codino's own mascot (a round mint
// creature with a pink ponytail, "codino" in Italian), one pose per Session
// State. "minimal" is the abstract Theme; its effects are also what Tiles fall
// back to under a Character Theme. Clawd is never built in (ADR 0003).
#include "theme.h"

namespace device {

namespace {

constexpr const char* kMinimal = R"json({
  "kind": "theme", "name": "minimal",
  "states": {
    "idle": {"palette": ["#8080c0"], "effect": {"name": "steady", "level": 32}},
    "working": {"palette": ["#005aff"], "effect": {"name": "scroll", "period_ms": 125, "low": 80, "high": 255}},
    "awaiting_input": {"palette": ["#ff9600"], "effect": {"name": "steady"}},
    "done": {"palette": ["#00c83c"], "effect": {"name": "steady"}},
    "error": {"palette": ["#ff0000"], "effect": {"name": "blink", "period_ms": 1000}}
  }
})json";

// Palette indices of Nino's frames: 0 the Session State's color (accents),
// 1 the body, 2 the ponytail, 3 the closed eyes, 4 the sleeping twinkle.
constexpr const char* kDefault = R"json({
  "kind": "theme", "name": "default",
  "states": {
    "idle": {"palette": ["#8080c0", "#0b3029", "#3a1530", "#03100d", "#202040"],
             "sprite": {"pack": "nino", "frames": ["idle-1", "idle-2"], "frame_ms": 1500}},
    "working": {"palette": ["#005aff", "#2ec4a0", "#ff5fa2"],
                "sprite": {"pack": "nino", "frames": ["working-1", "working-2", "working-3", "working-2"], "frame_ms": 300}},
    "awaiting_input": {"palette": ["#ff9600", "#2ec4a0", "#ff5fa2"],
                       "sprite": {"pack": "nino", "frames": ["awaiting-1", "awaiting-2"], "frame_ms": 400}},
    "done": {"palette": ["#00c83c", "#2ec4a0", "#ff5fa2"],
             "sprite": {"pack": "nino", "frames": ["done-1", "done-2"], "frame_ms": 700}},
    "error": {"palette": ["#ff0000", "#a83226", "#ff5fa2"],
              "sprite": {"pack": "nino", "frames": ["error-1", "error-2"], "frame_ms": 250}}
  }
})json";

// Nino, 8x8: '.' is unlit, a digit a palette index of the Theme's state.
constexpr const char* kNino = R"json({
  "kind": "sprite_pack", "name": "nino", "width": 8, "height": 8,
  "frames": {
    "idle-1": [
        "4...222.",
        ".....2..",
        "..1111..",
        ".111111.",
        ".131131.",
        ".111111.",
        ".111111.",
        "..1..1.."],
    "idle-2": [
        "....222.",
        ".....2..",
        "..1111..",
        ".111111.",
        ".131131.",
        ".111111.",
        ".111111.",
        "..1..1.."],
    "working-1": [
        "...222..",
        ".....2..",
        "..1111..",
        ".111111.",
        "..11.11.",
        ".111111.",
        ".111111.",
        "..1..1.."],
    "working-2": [
        "....222.",
        ".....2..",
        "..1111..",
        ".111111.",
        ".1.11.1.",
        ".111111.",
        ".111111.",
        "..1..1.."],
    "working-3": [
        ".....222",
        ".....2..",
        "..1111..",
        ".111111.",
        ".11.11..",
        ".111111.",
        ".111111.",
        "..1..1.."],
    "awaiting-1": [
        "....222.",
        "0....2..",
        "0.1111..",
        "01.11.1.",
        ".1.11.1.",
        "0111111.",
        ".111111.",
        "..1..1.."],
    "awaiting-2": [
        ".....2..",
        "..1111..",
        ".1.11.1.",
        ".1.11.1.",
        ".111111.",
        ".111111.",
        "..1..1..",
        "........"],
    "done-1": [
        "....222.",
        ".....2..",
        "..1111..",
        ".111111.",
        ".1.11.1.",
        ".111111.",
        ".11..11.",
        "..1..1.."],
    "done-2": [
        "0...222.",
        ".....2..",
        "..1111.0",
        ".111111.",
        ".1.11.1.",
        ".111111.",
        ".11..11.",
        "..1..1.."],
    "error-1": [
        "...222..",
        "....2...",
        ".1111...",
        "111111..",
        "101101..",
        "11..11..",
        "111111..",
        ".1..1..."],
    "error-2": [
        ".....222",
        "......2.",
        "...1111.",
        "..111111",
        "..101101",
        "..11..11",
        "..111111",
        "...1..1."]
  }
})json";

}  // namespace

const BuiltIn kBuiltInThemes[] = {{kDefaultTheme, kDefault}, {kAbstractTheme, kMinimal}};
const size_t kBuiltInThemeCount = sizeof kBuiltInThemes / sizeof kBuiltInThemes[0];
const BuiltIn kBuiltInSpritePacks[] = {{"nino", kNino}};
const size_t kBuiltInSpritePackCount = sizeof kBuiltInSpritePacks / sizeof kBuiltInSpritePacks[0];

}  // namespace device
