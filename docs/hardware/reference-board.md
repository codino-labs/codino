# Reference board: Waveshare ESP32-S3-Matrix

The reference Device is the Waveshare ESP32-S3-Matrix: an ESP32-S3FH4R2 (4 MB flash and 2 MB PSRAM in the package) with an 8×8 WS2812B matrix, a QMI8658 IMU, BOOT and RESET buttons and a USB-C port wired to the chip's native USB. This page covers its Hardware Profile, how the firmware is built and laid out in flash, and the checks to run by hand on a real board. Automated tests can't cover the firmware shell (LED driver, USB, flash).

## Hardware Profile

The firmware reports this profile in its `hello_reply`, and `protocol/fixtures/hello-reply.jsonl` uses the same one. In code it is `device::referenceHardwareProfile()` in `device/shell/hardware_profile.cpp`.

| Field | Value | Source | Confidence |
|---|---|---|---|
| `width`, `height` | 8, 8 | Wiki: "onboard 8\*8 beads matrix of 64 RGB beads" [1]. | Certain |
| `data_pin` | 14 | Schematic [2]: IO14 drives `LED_DIN` through the level shifter Q1 (DMG1012T). Every Waveshare demo uses `RGB_Control_PIN 14` [3]. | Certain |
| `serpentine` | `false` | Schematic [2]: U1…U64 are chained row by row (`DOUT_1`…`DOUT_7` link the rows). Waveshare's demos address LED `row*8+col` [3] and use `NEO_MATRIX_ROWS + NEO_MATRIX_PROGRESSIVE` (HTTP demo). | Confirmed on the board (step 4, 2026-10-16): rows run the same way, not serpentine. |
| `color_order` | `"RGB"` | Waveshare's Color and Game demos use `NEO_RGB` [3]. Robojax, writing for this board, says "The matrix on this board expects RGB order" [4]. | Confirmed on the board (step 4, 2026-10-16). |
| `brightness_cap` | 48 | Waveshare warns that high brightness heats the board fast and can damage it [1][3]. Its demos cap brightness at 40–60 of 255 [3]. | A conservative guess. Check the heat (step 9). |
| `current_cap_ma` | 300 | Leaves 200 mA of a 500 mA USB port: 100 mA for the ESP32-S3 (an estimate) and 100 mA of margin. The firmware enforces it (`LedWiring`, below): by the power model a whole-matrix Takeover in red at the cap of 48 draws 257 mA and goes out as it is; a white frame at the cap (570 mA by the model) is dimmed to fit. | Measure it (step 9). |
| `button` | `true` | The BOOT button on IO0 [2]: a press Acknowledges (Button and IMU, below). | Certain |
| `imu` | `true` | QMI8658 on I2C: SDA IO11, SCL IO12, INT1 IO10, INT2 IO13 [2][3]. A double-tap Acknowledges; its orientation turns the picture under auto rotation (Button and IMU, below). | Certain |

Sources:

1. Waveshare wiki, ESP32-S3-Matrix: <https://www.waveshare.com/wiki/ESP32-S3-Matrix> (also at <https://docs.waveshare.com/ESP32-S3-Matrix>)
2. Schematic: <https://files.waveshare.com/wiki/ESP32-S3-Matrix/ESP32-S3-Matrix-Sch.pdf>
3. Demo code: <https://files.waveshare.com/wiki/ESP32-S3-Matrix/ESP32-S3-Matrix-Demo.zip> (`Color/WS_Matrix.*`, `Game/WS_Matrix.*`, `Game/WS_QMI8658.cpp`, `Font/WS_Flow.cpp`, `HTTP/WS_Flow.cpp`)
4. Robojax, ESP32-S3 RGB LED Matrix projects: <https://robojax.com/RJT829?lang=en>, <https://robojax.com/tutorial_view.php?lang=en&id=841>
5. ESPHome devices, Waveshare ESP32-S3 Matrix: <https://devices.esphome.io/devices/waveshare-esp32s3-matrix/>

### Current limit

The brightness cap scales every channel; the current cap bounds the frame as a whole. Before a frame goes down the data line, `device::LedWiring` estimates its draw with FastLED's power model for 5050 WS2812B LEDs: 16, 11 and 15 mA for red, green and blue at 255, and 1 mA for every LED, lit or not (PJRC measured "just under 1 mA per LED" sitting dark [6]). A frame over `current_cap_ma` is dimmed as a whole until the estimate fits, its colors kept and a lit channel kept at 1 at least; a frame inside it goes out unchanged. The model is FastLED's, but FastLED's own limiter stays off: the shell's is portable and tested natively (`device/test/shell_test.cpp`, "current limit").

#5 had no limit and only reported `current_cap_ma`, trusting the brightness cap alone. That can't work for a 16×16 panel: 256 LEDs draw 256 mA dark, and a uniform cap that keeps a white frame inside the port would be 1 of 255 (below). A frame-aware limit keeps the guarantee and lets sparse frames (a pose, Tiles) shine at the brightness cap.

6. PJRC, "How Much Current Do WS2812 / NeoPixel LEDs Really Use?": <https://www.pjrc.com/how-much-current-do-ws2812-neopixel-leds-really-use>

### Button and IMU

The firmware reads them only when the Hardware Profile says the build has them (`button`, `imu`); a build without either works as before. Their logic is portable and tested natively (`device/shell/button.h`, `imu_gestures.*`; `device/test/input_test.cpp`); the glue in `firmware/src/main.cpp` is checked by hand (step 15).

- **BOOT button** (GPIO 0, low while pressed, read every loop): a press that has held for `Button::kSettleMs` (20 ms) Acknowledges, once however long it is held. GPIO 0 is a strapping pin: held while RESET is pressed, it enters download mode instead, as always.
- **QMI8658** (I2C at 400 kHz, address 0x6B, else 0x6A): the accelerometer alone at 224 Hz and ±8 g, read every 5 ms. A board where neither address answers goes without double-tap and auto-rotation.
- **Double-tap**: `TapDetector` takes a jolt of more than `kTapMg` (1.2 g) away from gravity that dies down within `kMaxTapMs` (50 ms) as a tap, and two taps 80–500 ms apart as an Acknowledge. A longer push (the Device shoved, picked up) is no tap, and gravity is taken afresh after one. The thresholds are a starting point, to tune on the board against desk bumps (step 15).
- **Orientation**: `OrientationSensor` takes the reading along the matrix's right and down, and once one edge has stood highest for `kSettleMs` (500 ms), by a margin past 45°, reports the rotation that shows the picture upright (`DeviceCore::orient`, the `orientation` message). Lying flat keeps the last one. On the reference board the QMI8658 lies turned: the matrix's right is the IMU's +y and its down the IMU's −x (measured with the board standing on its edges, 2026-10-16), which `sampleImu()` feeds it.

A board flashed before #13 keeps the Hardware Profile it stored then, with `button` `false`: erase it (`$ESPTOOL erase_region 0x210000 0x1000`) and the next boot stores the reference profile.

## Hardware Profile presets

The web flasher (below) offers these, the reference board's preselected, plus Custom (every field). They live in `device/flasher/hardware_profile.mjs`; `device/test/golden/hardware-profiles.tsv` holds the line each is stored as.

| Preset | Size | `data_pin` | `serpentine` | `color_order` | `brightness_cap` | `current_cap_ma` | `button`, `imu` |
|---|---|---|---|---|---|---|---|
| ESP32-S3-Matrix, built-in 8×8 (preselected) | 8×8 | 14 | `false` | `RGB` | 48 | 300 | `true`, `true` |
| ESP32-S3-Matrix + external 8×8 panel on GPIO 1 | 8×8 | 1 | `false` | `GRB` | 64 | 330 | `true`, `true` |
| ESP32-S3-Matrix + external 16×16 panel on GPIO 1 | 16×16 | 1 | `true` | `GRB` | 32 | 330 | `true`, `true` |

The external panels are wired with three Dupont wires: 5V, GND and data to GPIO 1. The schematic [2] routes IO1–IO7 and IO33–IO40 to the pin headers; GPIO 1 is the first of them and has no other use on the board. Likely, not certain: check the pin labelled 1 on the board's pinout (step 13). The built-in matrix stays dark, never driven. Rigid 8×8 panels are usually wired row by row in GRB; flexible 16×16 ones zig-zag, so mount the panel with its zig-zag along the rows (or choose Custom). Another panel or board: Custom.

### Power budget (500 mA USB port)

| | Built-in 8×8 | External 8×8 | External 16×16 |
|---|---|---|---|
| USB port | 500 mA | 500 mA | 500 mA |
| ESP32-S3 board, USB active, no radio (estimate) | 100 mA | 100 mA | 100 mA |
| Built-in matrix left dark (64 × 1 mA) | | 64 mA | 64 mA |
| `current_cap_ma`: the matrix | 300 mA | 330 mA | 330 mA |
| of which its LEDs dark | 64 mA | 64 mA | 256 mA |
| left for light | 236 mA | 266 mA | 74 mA |
| margin on the port | 100 mA | 6 mA | 6 mA |

- **External 8×8**, cap 64: a whole-matrix Takeover in red is 64 × 16 × 64/255 = 257 mA of light, inside 266: Codino's own brightest rendering goes out as it is. A white frame (675 mA by the model) is dimmed.
- **External 16×16**, cap 32: with only 74 mA left for light, the brightness cap alone would have to be 1 to keep a white frame inside the port (256 × 42 × c/255 ≤ 74), and 4 for a red Takeover (256 × 16 × c/255 ≤ 74). So the cap is 32, which a sparse frame (the mascot, a few Tiles) reaches, and the current limit dims whatever lights much of the matrix: a red Takeover ends up near 4/255, a white frame near 1/255. A 16×16 on a USB port is dim when it is full. For more, power the panel from a 5 V supply of 1 A or more (its GND joined to the board's) and flash Custom with a higher current cap.

If the board disagrees with the table (step 4), fix the profile in all of these places in one commit: `referenceHardwareProfile()`, `protocol/fixtures/hello-reply.jsonl`, `referenceProfile` in `internal/host/watch_test.go`, the reference build in `device/test/device_core_test.cpp`, and the web flasher's preselected preset: `device/flasher/hardware_profile.mjs`, its line in `device/test/golden/hardware-profiles.tsv` and the image `device/test/golden/hwprofile-esp32-s3-matrix.bin` (the line, `\n`, then `0xFF` to 4096 bytes). Then run both done-checks.

## Build

```sh
make -C device firmware
```

This builds `device/firmware/` (PlatformIO, Arduino core 2.0.17 via `espressif32@6.9.0`, FastLED 3.9.4) with the unchanged Device core and the shell's portable parts (`device/shell/`). If `pio` isn't on the PATH, the target installs PlatformIO 6.2.0 into a venv under `~/.cache/codino-toolchain/platformio/`, with its packages next to it. The first build downloads the ESP32 platform.

The images land in `device/build/firmware/` (build output, not committed). The web flasher (below) picks them up from there:

| File | Offset |
|---|---|
| `bootloader.bin` | `0x0` |
| `partitions.bin` | `0x8000` |
| `firmware.bin` | `0x10000` |
| `firmware-merged.bin` | `0x0` (the three above in one image) |

### Flash layout (`device/firmware/partitions.csv`, 4 MB)

| Partition | Offset | Size | Holds |
|---|---|---|---|
| `nvs` | `0x9000` | 20 KB | unused by the shell; the Arduino core expects it |
| `factory` | `0x10000` | 2 MB | the firmware |
| `hwprofile` (data, subtype `0x40`) | `0x210000` | 4 KB | the Hardware Profile: one line of JSON, the `hardware_profile` object of a `hello_reply`, then `\n`, then erased bytes |
| `littlefs` | `0x211000` | 1980 KB | `/preferences.jsonl` (the cached Preferences) and `/installed/{theme,sprite_pack}/NAME.jsonl` (installed Themes and Sprite Packs) |

At boot the firmware reads `hwprofile`. A blank partition gets the reference profile written into it. A profile that doesn't parse (`device::parseHardwareProfile`), or whose `data_pin` this firmware can't drive, is left in place, and the Device runs with the reference profile; its `hello_reply` reports the profile it actually uses. Every install from the web flasher writes the chosen profile (the page starts from the reference preset, so an update needs the same choice again) by flashing a 4 KB image at `0x210000` that holds that line padded with `0xFF`, built in the browser by `device/flasher/hardware_profile.mjs` and checked against the same golden files as the firmware's decoder (`device/test/golden/hardware-profiles.tsv`, `hwprofile-esp32-s3-matrix.bin`, and `hardware-profiles-refused.tsv`, the profiles both refuse).

## Web flasher

`device/flasher/` is a static page: choose a Hardware Profile preset or Custom, put the board in download mode, press Install. It uses ESP Web Tools 10.4.0 (esptool-js inside), pinned in `device/Makefile` with the SHA-256 of its npm release and vendored into the page at build time, so the page loads nothing from a CDN and can be hosted as plain files (GitHub Pages, say). Build and try it locally:

```sh
make -C device flasher          # builds the firmware too; the page lands in device/build/flasher/
python3 -m http.server -d device/build/flasher 8000
```

Open <http://localhost:8000> in desktop Chrome or Edge: Web Serial needs one of them, and HTTPS or `localhost` (not a LAN address). The page builds an ESP Web Tools manifest in the browser: `firmware/firmware-merged.bin` at `0x0` and the profile image at `0x210000`, both by absolute URL, the profile as a `blob:`. ESP Web Tools asks whether to erase first: yes on a first install; no keeps the cached Preferences and installed Themes (in LittleFS) when updating.

The flasher is published at <https://codino-labs.github.io/codino/> by `.github/workflows/flasher.yml` (on pushes to `main` that touch `device/` or `protocol/`); `devicelink.FlasherURL`, which `codino status` and the web UI show when a Device's firmware is too old, points there. After a flash the board stays in download mode until RESET (checked on the board, 2026-10-16), and ESP Web Tools 10.4.0 leaves its dialog on "Connecting" after a failed install until the page is reloaded: the page says both.

**Download mode by hand.** The firmware never reboots into the bootloader on its own (USB, below), so ESP Web Tools can't put a running Device into download mode: the page says to hold BOOT, press RESET, release BOOT, and to choose the port that then appears (`303a:1001`, "USB JTAG/serial debug unit"), not "Codino". In download mode the ROM speaks through the USB-Serial/JTAG peripheral, whose DTR/RTS do drive the chip's reset, so esptool-js can reset the board into the new firmware when it is done. Whether it does on this board is to check (step 12).

### USB

The firmware runs USB through TinyUSB (`ARDUINO_USB_MODE=0`), not the chip's USB-Serial/JTAG peripheral, because that peripheral's descriptors are fixed. The CDC port enumerates as vendor `0x303a` (Espressif, which is what `devicelink.USBSerial` looks for), product `0x0002`, manufacturer and product string "Codino", and the chip's MAC as serial number.

**DTR/RTS and reset.** In TinyUSB mode the Arduino core's CDC reboots into the ROM bootloader in two cases: on a line coding of 1200 baud, and on the DTR/RTS sequence (0,1) → (1,1) → (1,0) → (0,0), which is esptool's reset. The Host never sets 1200 baud, and Linux raises and drops DTR and RTS together. But a driver that raises RTS before DTR on open and drops them in the other order on close would leave the Device in download mode: dark and silent until someone presses RESET. The firmware therefore turns this reboot off (`enableReboot(false)`). Opening and closing the port never resets a Device. In TinyUSB mode DTR/RTS aren't wired to the chip's reset either, as they are on the USB-Serial/JTAG peripheral. The cost: neither esptool nor a web flasher can put a running Device into download mode by itself. To flash it again, **hold BOOT, press and release RESET, then release BOOT**. Plugging it in with BOOT held also works. The board then shows up as Espressif's USB JTAG/serial port (`303a:1001`) until it is reset.

**Auto-reset, reconsidered for the web flasher (#14).** The options, and why none is taken:

- *USB-Serial/JTAG mode* (`ARDUINO_USB_MODE=1`): esptool's DTR/RTS reset works in hardware, but the same lines reset the chip whenever a program toggles them, the hazard #5 turned off, and its fixed descriptors lose the "Codino" product string.
- *Turning the Arduino core's reboot back on* (`enableReboot(true)`): a driver that sequences DTR/RTS like esptool on open or close would strand the Device in download mode, as above.
- *A protocol message that reboots into the bootloader* (the firmware calling the core's `usb_persist_restart(RESTART_BOOTLOADER)` on, say, `{"type":"enter_bootloader"}`): safe, since no Host sends it by accident, but ESP Web Tools can't send it. The page would have to open the "Codino" port itself first, then make the human choose the new download-mode port in a second dialog; and how the ROM enumerates after that restart can only be checked on the board. A candidate once the board is here, not before.

So flashing needs BOOT + RESET, every time, and the page says so up front.

The Host already opens ports at 115200 baud with DTR and RTS on (`internal/devicelink/serial_*.go`). TinyUSB sends only while DTR is up, so it needs nothing new.

## Manual verification (when the board arrives)

You need the board, a data USB-C cable, and ideally a USB power meter, a second ESP32 board running anything else, and an 8×8 or 16×16 WS2812B panel with three Dupont wires. Build the Host with `go build -o codino ./cmd/codino` and the firmware with `make -C device firmware`. Set `ESPTOOL` to the esptool PlatformIO installed:

```sh
P=~/.cache/codino-toolchain/platformio
ESPTOOL="env PLATFORMIO_CORE_DIR=$P/core $P/venv/bin/pio pkg exec -p tool-esptoolpy -- esptool.py --chip esp32s3"
```

On Linux, install the udev rule first (README, *Linux: serial port permissions*).

1. **Flash.** Hold BOOT, press RESET, release BOOT. `lsusb` shows `303a:1001`. Run:
   ```sh
   $ESPTOOL erase_flash
   $ESPTOOL write_flash 0x0 device/build/firmware/firmware-merged.bin
   ```
   Press RESET.
   - [ ] The matrix goes dark at boot. About 5 s later it shows Disconnected: a dim diagonal on a dark matrix.
2. **Enumeration.**
   - [x] `lsusb` shows `303a:0002 Codino Codino`.
   - [x] Linux: `/dev/serial/by-id/usb-Codino_Codino_<MAC>-if00` exists.
   - [ ] macOS: a `/dev/cu.usbmodem…` port appears.
   - [ ] Windows: a COM port named "Codino" (or "USB Serial Device") appears.
   - [ ] Nothing but protocol lines comes out of the port: no boot log.
3. **Handshake and claim.** Run the daemon in the foreground with `./codino daemon` (no `--virtual-device`). From another terminal: `./codino event --session a --state awaiting_input --project .`.
   - [x] The daemon logs the claim of the board's port.
   - [x] `./codino status` shows `Device: <port> (firmware 0.1.0, protocol 0)`.
   - [x] The matrix pulses, then escalates (faster after 30 s, Takeover after 2 min).
   - [x] `./codino event --session a --end` darkens it.
   - [x] Ctrl-C on the daemon sends `goodbye`: the matrix goes dark, not Disconnected.
4. **Wiring and color order.** Show the Raw Mode fixture, a frame where the LED at (x, y) is R = 32x, G = 32y, B = 128:
   ```sh
   $P/venv/bin/python - /dev/ttyACM0 <<'EOF'   # the board's port
   import sys, time, serial
   s = serial.Serial(sys.argv[1], 115200)
   s.write(b'{"type":"hello","protocol":0,"host_time":0}\n'); print(s.readline())
   s.write(b'{"type":"raw_mode_enter"}\n' + open("protocol/fixtures/raw-frame.bin", "rb").read())
   for i in range(30): s.write(b'{"type":"heartbeat","host_time":%d}\n' % (i * 1000)); time.sleep(1)
   EOF
   ```
   - [x] One corner LED is blue (0, 0, 128): that is the top-left of the picture. Note which way round the board is then (e.g. where the USB-C port is) and record it on this page, so "up" is known for rotation and #13.
     **Up is the USB-C edge:** with the port at the top, a picture shows upright (rotation 0).
     Checked on 2026-10-16 with an 8×8 PNG through `codino show` rather than this fixture: pure blue at (0, 0), pure green along the rest of row 0, white at (0, 1), pure red elsewhere. Red and green came out as sent (so blue is the third channel), and the white LED sat next to the blue one. The gradient below is still to be seen.
   - [ ] Colors grow redder to the right and greener downwards, with the bottom-right LED whitish (224, 224, 128).
   - [x] No row runs backwards. If odd rows run backwards, `serpentine` is `true`.
   - [x] Red and green are not swapped. If they are, `color_order` is `"GRB"`.
   - [x] The picture isn't transposed. A transposed one means column wiring, which the Hardware Profile can't describe: report it.
   Fix the profile as described above if anything is off.
5. **Ignoring another ESP32.** With the daemon running and the Device claimed, plug in another ESP32-S3 (or S2/C3) with native USB that runs other firmware (e.g. a Waveshare demo, or one in download mode).
   - [ ] The daemon tries it once, refuses it (no valid `hello_reply`) and leaves it alone: no reopening for `devicelink.RefusedRetry` (1 min) or until it is replugged.
   - [ ] The other board keeps running undisturbed.
   - [ ] The Codino stays claimed.
   - [ ] With only the other board plugged in, nothing is claimed.
6. **Replug recovery.** With a session Awaiting Input, unplug the Device.
   - [x] `codino status` shows no Device.
   - [x] Plug it back in: the Host claims it again within a few seconds.
   - [x] The session shows again, with Escalation counted from when it really started waiting.
   - [x] The same works on a different USB port.
7. **Opening the port never resets the Device.** With the daemon stopped, run:
   ```sh
   $P/venv/bin/python - /dev/ttyACM0 <<'EOF'
   import sys, time, serial
   s = serial.Serial(sys.argv[1], 115200)
   for dtr, rts in [(0, 1), (1, 1), (1, 0), (0, 0)]:  # esptool's reset sequence
       s.dtr, s.rts = dtr, rts; time.sleep(0.1)
   s.baudrate = 1200; time.sleep(0.2); s.close()
   EOF
   ```
   - [x] `lsusb` still shows `303a:0002`, not `303a:1001`.
   - [x] The matrix shows Disconnected, not a reboot. (`lsusb` kept the same device number: no re-enumeration.)
   - [x] Start and stop the daemon ten times: the Device answers every handshake.
8. **Flashing again.** Holding BOOT and pressing RESET enters download mode (`303a:1001`). esptool's closing "Hard resetting via RTS pin" leaves the board in download mode (seen after `read_flash`, 2026-10-16): press RESET.
   - [x] `$ESPTOOL write_flash 0x10000 device/build/firmware/firmware.bin` then RESET boots the new firmware.
   - [ ] It keeps the Hardware Profile, Preferences and Themes, since `write_flash` touches only the app.
9. **Brightness and heat.**
   - [ ] Hold a Takeover (`codino event --session a --state awaiting_input`, wait 2 min) at `codino set brightness 100` for 10 minutes. The board stays comfortable to touch, under about 50 °C on an IR thermometer.
   - [ ] Repeat with a full-white Raw Mode frame: `codino show` a white PNG. The current limit dims it (570 mA by the model at the cap of 48). The USB meter stays under `current_cap_ma` (300 mA) above the board's draw with a dark matrix. If it doesn't, the model is off for these LEDs: lower `current_cap_ma`. If it stays far under and the white looks dim, the board's LEDs draw less than the model says: raise it (keeping 500 mA in all).
   - [x] `codino set brightness 10` visibly dims everything, and dim states stay lit.
   - [ ] If the board runs hot, lower `brightness_cap` (and the fixture) as described above.
10. **Persistence across reboot.**
    - [x] `codino set brightness 20`, `codino set rotation 90`, then stop the daemon and press RESET. The Disconnected diagonal at boot is dim and runs from top-left to bottom-right before any Host connects. (Not 180: the diagonal turned 180° is the same line.)
    - [x] `codino theme install themes/examples/my-pack.json` and `codino theme install themes/examples/my-character.json`, then press RESET. `codino theme list` still shows them.
    - [x] After `codino theme uninstall`, press RESET: they stay gone.
    - [x] `$ESPTOOL read_flash 0x210000 0x1000 hw.bin; head -1 hw.bin` shows the reference profile line the first boot wrote.
11. **A custom Hardware Profile.** Write a profile with another data pin. Check that it is used, and that an invalid one falls back to the reference profile:
    ```sh
    printf '{"width":8,"height":8,"data_pin":14,"serpentine":true,"color_order":"GRB","brightness_cap":32,"current_cap_ma":300,"button":false,"imu":true}\n' > hw.json
    $P/venv/bin/python -c "import sys; d=open('hw.json','rb').read(); open('hw.bin','wb').write(d+b'\xff'*(4096-len(d)))"
    $ESPTOOL write_flash 0x210000 hw.bin
    ```
    - [ ] After a RESET, the `hello_reply` the step 4 script prints first reports `serpentine` `true`, `GRB` and cap 32, and the matrix shows it: odd rows reversed, red and green swapped.
    - [ ] A profile with `data_pin` 19 (USB D−) leads to the reference profile in the `hello_reply` instead.
    - [ ] Restore with `$ESPTOOL erase_region 0x210000 0x1000`.
12. **Web flasher.** `make -C device flasher`, serve it (Web flasher, above) and open <http://localhost:8000> in Chrome.
    - [x] Without download mode, Install and the "Codino" port end in ESP Web Tools' "Failed to initialize": the page's step 2 explains why. Nothing on the board changed.
    - [x] With the reference preset, BOOT + RESET, Install, the `303a:1001` port and "erase": it flashes. Note whether the board starts the new firmware by itself or needs RESET, and make the page's last hint say so.
    - [x] The matrix shows Disconnected 5 s after boot; the Host claims it (`codino status`).
    - [x] `$ESPTOOL read_flash 0x210000 0x1000 hw.bin; head -1 hw.bin` is the line the page showed under "What gets written".
    - [x] Choose the 16×16 preset (no panel needed), install without erasing: the `hello_reply` (step 4's script) reports 16×16, GPIO 1, `GRB`, cap 32 and current cap 330, and a Theme installed before is still listed (LittleFS kept).
    - [x] Custom with 64×64 is refused on the page, Install disabled.
    - [x] Flash the reference preset again.
    - [x] Hosted on HTTPS (once there is a host): the same works, and `devicelink.FlasherURL` is set to its address.
13. **External panels.** Check on the board's pinout that the header pin labelled 1 is GPIO 1. Wire an 8×8 panel (5V, GND, data to GPIO 1) and flash its preset; then a 16×16 panel with its preset.
    - [ ] Step 4's frame shows right on each (fix the preset's `serpentine` or `color_order` if not, here and in `hardware-profiles.tsv`).
    - [ ] The built-in matrix stays dark.
    - [ ] With a USB meter: a white `codino show` and a Takeover each stay under 500 mA in all; the 16×16 Takeover is dim but readable. If the board itself draws more than the 100 mA budgeted, lower the presets' `current_cap_ma`.
14. **Update me.** With the daemon stopped, run step 4's script with `"protocol":1` in the hello.
    - [x] It prints the Device's `hello_reply` (protocol 0), and the matrix shows an amber arrow pointing up, not Disconnected, and keeps showing it after the script ends.
    - [x] Start the daemon: its `hello` (protocol 0) ends the arrow and the Device is claimed.
    - [x] With `codino set rotation 180` pushed before, the arrow points down on the board as mounted upright (it points up to whoever turned the Device).
15. **Button, double-tap and auto-rotation** (#13). Erase the stored profile first if the board was flashed before #13 (Button and IMU, above). Run the daemon; `codino status` shows `Orientation: upright turned by 0°` with the USB-C edge up.
    - [x] With a session Done, a press of BOOT clears it (`codino status` shows it Idle); with one Awaiting Input, a press restarts its Escalation.
    - [ ] Holding BOOT is one Acknowledge; pressing BOOT while pressing RESET still enters download mode.
    - [x] Two taps on the board's face, a quarter second apart, Acknowledge. One tap doesn't, nor two taps a second apart.
    - [x] Tapping the desk beside it, putting a mug down next to it, typing on the same desk and sliding the board along it Acknowledge nothing, over a few minutes. If they do, raise `TapDetector::kTapMg`; if real taps are missed, lower it.
    - [x] Stand the board on each edge in turn. After half a second the picture turns upright, and `codino status` says by how much: USB-C up 0, USB-C on the left 90 (the picture's top on the board's right edge), USB-C down 180, USB-C on the right 270. If a turn goes the wrong way or swaps 90 and 270, fix the axis mapping in `sampleImu()` (`firmware/src/main.cpp`) and record it here.
    - [ ] Laid flat, the picture keeps the last rotation. Held near 45°, it doesn't flicker between two.
    - [x] `codino set rotation 0` pins the picture whatever way the board stands; `codino unset rotation` returns to auto.
