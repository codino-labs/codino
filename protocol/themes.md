# Themes and Sprite Packs

A **Theme** says how the Device shows each Session State. It is declarative data, a JSON file: per Session State a palette plus either a built-in procedural effect with its parameters, or frames of a **Sprite Pack** with their timing. The Device core interprets it (on the LEDs and on the Virtual Device alike); nothing is compiled. A **Sprite Pack** is a bundle of named pixel-art frames that Themes draw from.

Both install on the Device without reflashing (`codino theme install FILE`), and the Device keeps them in its flash. The `theme` Preference (`codino set theme NAME`) selects the Theme to show.

## Built-in Themes

| Theme | What it is |
|---|---|
| `default` | The default **Character Theme**: Nino, Codino's own mascot (a round mint creature with a pink ponytail), strikes one pose per Session State: asleep (Idle), looking around (Working), hopping with a "!" (Awaiting Input), smiling with sparkles (Done), shaking (Error). Frames are 8×8, scaled up by whole factors and centered on a larger matrix (2× on 16×16). |
| `minimal` | The abstract Theme: a dim steady glow (Idle), a bright line running down (Working), a steady fill that Escalation pulses (Awaiting Input), steadily lit (Done), blinking (Error). |

The built-in Sprite Pack is `nino`. Their JSON is in `device/core/builtin_themes.cpp`, in the same format as below. Built-in names can't be installed over or uninstalled.

Clawd, the Claude Code mascot, is not distributed with Codino (ADR 0003). You can draw your own Clawd Sprite Pack and load it yourself: see [Loading your own Sprite Pack](#loading-your-own-sprite-pack).

## Where a Theme shows

- **The whole matrix** (one session, or the Aggregate Layout): the Session State as the Theme says, a Sprite Pack pose included.
- **Tiles** (several sessions in the adaptive Layout): each Tile draws its Session State's effect in the session's Project Color, so you can tell projects apart. A pose needs the whole matrix: under a Character Theme, Tiles fall back to the effects of the abstract Theme (`minimal`). The Overflow Tile draws in the Session State's own color (below).
- **Escalation** dims and brightens whatever the Theme draws for Awaiting Input: the gentle pulse, the faster brighter pulse, the steady glow. A **Takeover** fills the matrix with the waiting session's Project Color whatever the Theme.
- **Quiet Mode**, the **brightness cap**, **rotation**, **Raw Mode** and **Disconnected** work as before: Disconnected is not a Session State and stays built in.
- A Theme the Device doesn't have (not installed, or uninstalled since) shows as `default`. A pose whose Sprite Pack or frame is missing shows the abstract Theme's effect in the state's own color.

## Theme file

```json
{
  "kind": "theme",
  "name": "neon",
  "states": {
    "idle":           {"palette": ["#7a00ff"], "effect": {"name": "breathe", "period_ms": 6000, "low": 8, "high": 40}},
    "working":        {"palette": ["#00e5ff"], "effect": {"name": "scroll", "period_ms": 100, "low": 60, "high": 255, "direction": "right"}},
    "awaiting_input": {"palette": ["#ffea00"], "effect": {"name": "steady"}},
    "done":           {"palette": ["#39ff14", "#ffffff"], "sprite": {"pack": "my-pack", "frames": ["done-1", "done-2"], "frame_ms": 700}},
    "error":          {"palette": ["#ff073a"], "effect": {"name": "blink", "period_ms": 800}}
  }
}
```

| Field | Meaning |
|---|---|
| `kind` | `"theme"`. |
| `name` | 1–32 of `a-z`, `0-9`, `-`, `_`. Installing a Theme of the same name replaces it. |
| `states` | One entry for each of `idle`, `working`, `awaiting_input`, `done`, `error`; all five are required. |
| `states.*.palette` | 1–16 colors, `#rrggbb`. The **first is the Session State's own color**: effects draw in it on the whole matrix, and so do the Overflow Tile and a Takeover of a session without a Project Color. Sprite pixels index into the whole palette. |
| `states.*.effect` | A procedural effect (below). Give either `effect` or `sprite`, not both. |
| `states.*.sprite` | A pose: `pack` (a Sprite Pack name), `frames` (1–8 frame names of that pack, shown in order and looping) and `frame_ms` (how long each shows, 20–60000, default 500). |

A Theme with a `sprite` in any state is a Character Theme.

### Effects

Levels are 0–255, a fraction of the color the effect draws in (the palette's first color, or the Project Color in a Tile). Every parameter is optional.

| `name` | Parameters (default) | What it draws |
|---|---|---|
| `steady` | `level` (255) | The color at one level. |
| `pulse` | `period_ms` (2000), `low` (0), `high` (255) | A linear ramp from `low` up to `high` and back, once a period. |
| `breathe` | `period_ms` (2000), `low` (0), `high` (255) | Like `pulse`, eased at both ends, so it lingers dim and bright. |
| `blink` | `period_ms` (1000), `level` (255) | On at `level` for half the period, then off. |
| `scroll` | `period_ms` (125), `low` (0), `high` (255), `direction` (`down`) | A line at `high` across a region at `low`, moving one row (`down`, `up`) or column (`right`, `left`) every `period_ms`. |

`period_ms` is 2–60000. Effects run on the Device's clock, so every Tile in the same state moves in step.

## Sprite Pack file

```json
{
  "kind": "sprite_pack",
  "name": "my-pack",
  "width": 8,
  "height": 8,
  "frames": {
    "done-1": [
      "....222.",
      ".....2..",
      "..1111..",
      ".111111.",
      ".1.11.1.",
      ".111111.",
      ".11..11.",
      "..1..1.."
    ]
  }
}
```

| Field | Meaning |
|---|---|
| `kind` | `"sprite_pack"`. |
| `name` | As a Theme's name. |
| `width`, `height` | 1–16: the size of every frame. 8×8 reads on every Hardware Profile; on a larger matrix a frame is scaled up by the largest whole factor that fits and centered, on a smaller one cropped around its center. |
| `frames` | 1–32 frames by name (names as Theme names). A frame is `height` rows of `width` characters: `.` is an unlit LED, `0`–`9` and `a`–`f` a palette index (0–15) of the Theme state that shows it. An index beyond that palette stays unlit. |

A pack holds no colors: the Theme gives each state its palette, so one pack can serve several Themes, and a pose can change color by state (Nino's body turns red for Error).

## Size limits

A Device has little RAM and flash, so the format is small: a Theme or Sprite Pack is one protocol line of at most 8192 bytes (a 16×16 pack holds about 25 frames, an 8×8 one all 32). A Device holds at most 8 installed Themes and 8 installed Sprite Packs besides the built-in ones; uninstall one to make room. It reads a Theme and the frames it uses into RAM only when the Theme is selected.

## Protocol

`codino theme install FILE` sends the file's JSON object, compacted to one line, as an `install` message with `"type":"install"` added; the Device validates it, stores it and replies with an `install_reply`. See [README.md](README.md#themes-and-sprite-packs) for the messages and `fixtures/install-*.jsonl` for examples.

## Loading your own Sprite Pack

To show your own mascot (for example a Clawd you draw yourself), make a Sprite Pack and a Character Theme that uses it, and install both. `themes/examples/` has a starting point: `my-pack.json` is Nino's frames under another name, `my-character.json` the Theme that poses them.

1. Copy both files. In the pack, give it your `name` and draw your frames: one or more per Session State, `.` for unlit and a digit for a color of the palette.
2. In the Theme, give it a `name`, point every `sprite.pack` at your pack and list your frame names. Set each state's `palette`: the first color is the state's own color (keep it distinct per state, it colors the Overflow Tile), the others are what your digits mean.
3. Install the pack, then the Theme, and select it:

   ```sh
   codino theme install my-pack.json
   codino theme install my-character.json
   codino set theme my-character
   codino theme list        # what the Device has; * marks the selected Theme
   ```

   The Device checks both and says what is wrong if anything is (`codino theme install` prints it). Install again after an edit: the same name replaces the old one.
4. To remove them: `codino theme uninstall my-character`, `codino theme uninstall --sprite-pack my-pack`.

Your pack stays on your Device: Codino never ships or uploads it anywhere else. Clawd is Anthropic's character; a Clawd pack is for your own Device, not for sharing.

Without hardware, the terminal Virtual Device keeps what you install in its `--store-dir` (the Host Daemon passes one in its runtime directory), so you can try a Theme on screen first.
