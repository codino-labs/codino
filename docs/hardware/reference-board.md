# Reference board: Waveshare ESP32-S3-Matrix

The reference Device is the Waveshare ESP32-S3-Matrix: an ESP32-S3FH4R2 (4 MB flash and 2 MB PSRAM in the package) with an 8×8 WS2812B matrix, a QMI8658 IMU, BOOT and RESET buttons and a USB-C port wired to the chip's native USB. This page covers its Hardware Profile, how the firmware is built and laid out in flash, and the checks to run by hand on a real board. Automated tests can't cover the firmware shell (LED driver, USB, flash).

## Hardware Profile

The firmware reports this profile in its `hello_reply`, and `protocol/fixtures/hello-reply.jsonl` uses the same one. In code it is `device::referenceHardwareProfile()` in `device/shell/hardware_profile.cpp`.

| Field | Value | Source | Confidence |
|---|---|---|---|
| `width`, `height` | 8, 8 | Wiki: "onboard 8\*8 beads matrix of 64 RGB beads" [1]. | Certain |
| `data_pin` | 14 | Schematic [2]: IO14 drives `LED_DIN` through the level shifter Q1 (DMG1012T). Every Waveshare demo uses `RGB_Control_PIN 14` [3]. | Certain |
| `serpentine` | `false` | Schematic [2]: U1…U64 are chained row by row (`DOUT_1`…`DOUT_7` link the rows). Waveshare's demos address LED `row*8+col` [3] and use `NEO_MATRIX_ROWS + NEO_MATRIX_PROGRESSIVE` (HTTP demo). | Likely. The schematic shows the electrical chain, not the placement on the PCB. One community ESPHome config maps the matrix as serpentine *columns* turned 180° [5]. Check it on the board (step 4). |
| `color_order` | `"RGB"` | Waveshare's Color and Game demos use `NEO_RGB` [3]. Robojax, writing for this board, says "The matrix on this board expects RGB order" [4]. | Uncertain. Waveshare's Font and HTTP demos use `NEO_GRB` [3]. Check it on the board (step 4). |
| `brightness_cap` | 48 | Waveshare warns that high brightness heats the board fast and can damage it [1][3]. Its demos cap brightness at 40–60 of 255 [3]. | A conservative guess. Check the heat (step 9). |
| `current_cap_ma` | 300 | An estimate. 64 LEDs at a cap of 48/255 stay well under a 500 mA USB port. The firmware also passes this value to FastLED's power limiter. | Measure it (step 9). |
| `button` | `false` | The board has a BOOT button on IO0 [2], but nothing reads it yet. #13 makes it Acknowledge and should then turn this field to `true`. | |
| `imu` | `true` | QMI8658 on I2C: SDA IO11, SCL IO12, INT1 IO10, INT2 IO13 [2][3]. #13 uses it. | Certain |

Sources:

1. Waveshare wiki, ESP32-S3-Matrix: <https://www.waveshare.com/wiki/ESP32-S3-Matrix> (also at <https://docs.waveshare.com/ESP32-S3-Matrix>)
2. Schematic: <https://files.waveshare.com/wiki/ESP32-S3-Matrix/ESP32-S3-Matrix-Sch.pdf>
3. Demo code: <https://files.waveshare.com/wiki/ESP32-S3-Matrix/ESP32-S3-Matrix-Demo.zip> (`Color/WS_Matrix.*`, `Game/WS_Matrix.*`, `Game/WS_QMI8658.cpp`, `Font/WS_Flow.cpp`, `HTTP/WS_Flow.cpp`)
4. Robojax, ESP32-S3 RGB LED Matrix projects: <https://robojax.com/RJT829?lang=en>, <https://robojax.com/tutorial_view.php?lang=en&id=841>
5. ESPHome devices, Waveshare ESP32-S3 Matrix: <https://devices.esphome.io/devices/waveshare-esp32s3-matrix/>

If the board disagrees with the table (step 4), fix the profile in all of these places in one commit: `referenceHardwareProfile()`, `protocol/fixtures/hello-reply.jsonl`, `referenceProfile` in `internal/host/watch_test.go`, and the reference build in `device/test/device_core_test.cpp`. Then run both done-checks.

## Build

```sh
make -C device firmware
```

This builds `device/firmware/` (PlatformIO, Arduino core 2.0.17 via `espressif32@6.9.0`, FastLED 3.9.4) with the unchanged Device core and the shell's portable parts (`device/shell/`). If `pio` isn't on the PATH, the target installs PlatformIO 6.2.0 into a venv under `~/.cache/codino-toolchain/platformio/`, with its packages next to it. The first build downloads the ESP32 platform.

The images land in `device/build/firmware/` (build output, not committed). The web flasher (#14) picks them up from there:

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

At boot the firmware reads `hwprofile`. A blank partition gets the reference profile written into it. A profile that doesn't parse (`device::parseHardwareProfile`), or whose `data_pin` this firmware can't drive, is left in place, and the Device runs with the reference profile; its `hello_reply` reports the profile it actually uses. The web flasher writes the chosen profile by flashing a 4 KB image at `0x210000` that holds that line padded with `0xFF`.

### USB

The firmware runs USB through TinyUSB (`ARDUINO_USB_MODE=0`), not the chip's USB-Serial/JTAG peripheral, because that peripheral's descriptors are fixed. The CDC port enumerates as vendor `0x303a` (Espressif, which is what `devicelink.USBSerial` looks for), product `0x0002`, manufacturer and product string "Codino", and the chip's MAC as serial number.

**DTR/RTS and reset.** In TinyUSB mode the Arduino core's CDC reboots into the ROM bootloader in two cases: on a line coding of 1200 baud, and on the DTR/RTS sequence (0,1) → (1,1) → (1,0) → (0,0), which is esptool's reset. The Host never sets 1200 baud, and Linux raises and drops DTR and RTS together. But a driver that raises RTS before DTR on open and drops them in the other order on close would leave the Device in download mode: dark and silent until someone presses RESET. The firmware therefore turns this reboot off (`enableReboot(false)`). Opening and closing the port never resets a Device. In TinyUSB mode DTR/RTS aren't wired to the chip's reset either, as they are on the USB-Serial/JTAG peripheral. The cost: neither esptool nor a web flasher can put a running Device into download mode by itself. To flash it again, **hold BOOT, press and release RESET, then release BOOT**. Plugging it in with BOOT held also works. The board then shows up as Espressif's USB JTAG/serial port (`303a:1001`) until it is reset.

The Host already opens ports at 115200 baud with DTR and RTS on (`internal/devicelink/serial_*.go`). TinyUSB sends only while DTR is up, so it needs nothing new.

## Manual verification (when the board arrives)

You need the board, a data USB-C cable, and ideally a USB power meter and a second ESP32 board running anything else. Build the Host with `go build -o codino ./cmd/codino` and the firmware with `make -C device firmware`. Set `ESPTOOL` to the esptool PlatformIO installed:

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
   - [ ] `lsusb` shows `303a:0002 Codino Codino`.
   - [ ] Linux: `/dev/serial/by-id/usb-Codino_Codino_<MAC>-if00` exists.
   - [ ] macOS: a `/dev/cu.usbmodem…` port appears.
   - [ ] Windows: a COM port named "Codino" (or "USB Serial Device") appears.
   - [ ] Nothing but protocol lines comes out of the port: no boot log.
3. **Handshake and claim.** Run the daemon in the foreground with `./codino daemon` (no `--virtual-device`). From another terminal: `./codino event --session a --state awaiting_input --project .`.
   - [ ] The daemon logs the claim of the board's port.
   - [ ] `./codino status` shows `Device: <port> (firmware 0.1.0, protocol 0)`.
   - [ ] The matrix pulses, then escalates (faster after 30 s, Takeover after 2 min).
   - [ ] `./codino event --session a --end` darkens it.
   - [ ] Ctrl-C on the daemon sends `goodbye`: the matrix goes dark, not Disconnected.
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
   - [ ] One corner LED is blue (0, 0, 128): that is the top-left of the picture. Note which way round the board is then (e.g. where the USB-C port is) and record it on this page, so "up" is known for rotation and #13.
   - [ ] Colors grow redder to the right and greener downwards, with the bottom-right LED whitish (224, 224, 128).
   - [ ] No row runs backwards. If odd rows run backwards, `serpentine` is `true`.
   - [ ] Red and green are not swapped. If they are, `color_order` is `"GRB"`.
   - [ ] The picture isn't transposed. A transposed one means column wiring, which the Hardware Profile can't describe: report it.
   Fix the profile as described above if anything is off.
5. **Ignoring another ESP32.** With the daemon running and the Device claimed, plug in another ESP32-S3 (or S2/C3) with native USB that runs other firmware (e.g. a Waveshare demo, or one in download mode).
   - [ ] The daemon tries it once, refuses it (no valid `hello_reply`) and leaves it alone: no reopening for `devicelink.RefusedRetry` (1 min) or until it is replugged.
   - [ ] The other board keeps running undisturbed.
   - [ ] The Codino stays claimed.
   - [ ] With only the other board plugged in, nothing is claimed.
6. **Replug recovery.** With a session Awaiting Input, unplug the Device.
   - [ ] `codino status` shows no Device.
   - [ ] Plug it back in: the Host claims it again within a few seconds.
   - [ ] The session shows again, with Escalation counted from when it really started waiting.
   - [ ] The same works on a different USB port.
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
   - [ ] `lsusb` still shows `303a:0002`, not `303a:1001`.
   - [ ] The matrix shows Disconnected, not a reboot.
   - [ ] Start and stop the daemon ten times: the Device answers every handshake.
8. **Flashing again.** Holding BOOT and pressing RESET enters download mode (`303a:1001`).
   - [ ] `$ESPTOOL write_flash 0x10000 device/build/firmware/firmware.bin` then RESET boots the new firmware.
   - [ ] It keeps the Hardware Profile, Preferences and Themes, since `write_flash` touches only the app.
9. **Brightness and heat.**
   - [ ] Hold a Takeover (`codino event --session a --state awaiting_input`, wait 2 min) at `codino set brightness 100` for 10 minutes. The board stays comfortable to touch, under about 50 °C on an IR thermometer.
   - [ ] Repeat with a full-white Raw Mode frame: `codino show` a white PNG. The USB meter stays under `current_cap_ma` (300 mA) above the board's idle draw.
   - [ ] `codino set brightness 10` visibly dims everything, and dim states stay lit.
   - [ ] If the board runs hot, lower `brightness_cap` (and the fixture) as described above.
10. **Persistence across reboot.**
    - [ ] `codino set brightness 20`, `codino set rotation 180`, then stop the daemon and press RESET. The Disconnected diagonal at boot is dim and turned before any Host connects.
    - [ ] `codino theme install themes/examples/my-pack.json` and `codino theme install themes/examples/my-character.json`, then press RESET. `codino theme list` still shows them.
    - [ ] After `codino theme uninstall`, press RESET: they stay gone.
    - [ ] `$ESPTOOL read_flash 0x210000 0x1000 hw.bin; head -1 hw.bin` shows the reference profile line the first boot wrote.
11. **A custom Hardware Profile.** Write a profile with another data pin. Check that it is used, and that an invalid one falls back to the reference profile:
    ```sh
    printf '{"width":8,"height":8,"data_pin":14,"serpentine":true,"color_order":"GRB","brightness_cap":32,"current_cap_ma":300,"button":false,"imu":true}\n' > hw.json
    $P/venv/bin/python -c "import sys; d=open('hw.json','rb').read(); open('hw.bin','wb').write(d+b'\xff'*(4096-len(d)))"
    $ESPTOOL write_flash 0x210000 hw.bin
    ```
    - [ ] After a RESET, the `hello_reply` the step 4 script prints first reports `serpentine` `true`, `GRB` and cap 32, and the matrix shows it: odd rows reversed, red and green swapped.
    - [ ] A profile with `data_pin` 19 (USB D−) leads to the reference profile in the `hello_reply` instead.
    - [ ] Restore with `$ESPTOOL erase_region 0x210000 0x1000`.
