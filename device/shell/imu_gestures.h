// What the human does to a Device with an IMU, read from its accelerometer:
// a double-tap (Acknowledge) and which way up it stands (auto-rotation).
// Samples are in milli-g along the IMU's axes, with the clock time they were
// read at; the firmware reads them every few ms (QMI8658 on the reference
// board).
#pragma once

#include <cstdint>

namespace device {

// Finds double-taps: two sharp jolts, each over within kMaxTapMs, from
// kMinGapMs to kMaxGapMs apart. A push that lasts longer (the Device shoved,
// picked up) is no tap, and neither is a jolt under kTapMg (a bump on the
// desk). Gravity is followed while the Device is still, and taken afresh once
// it has been moved, so a jolt is measured from however the Device stands.
class TapDetector {
 public:
  // How hard a tap jolts the Device, and how still it is again after.
  static constexpr int kTapMg = 1200;
  static constexpr int kQuietMg = 300;
  static constexpr uint32_t kMaxTapMs = 50;
  static constexpr uint32_t kMinGapMs = 80;
  static constexpr uint32_t kMaxGapMs = 500;
  // After a double-tap, taps count again only once this has passed, so a
  // third tap doesn't start the next double-tap.
  static constexpr uint32_t kLockoutMs = 500;
  // A jolt, or a smaller change, that lasts this long is the Device moved or
  // turned over: jolts count from how it stands now.
  static constexpr uint32_t kRestMs = 200;

  // True when this sample completes a double-tap.
  bool update(int x, int y, int z, uint32_t nowMs);

 private:
  // A tap that started at `at`; true when it is the second of a double-tap.
  bool tapped(uint32_t at);

  bool started_ = false;
  int gx_ = 0, gy_ = 0, gz_ = 0;  // gravity, as seen while still
  bool jolting_ = false;
  uint32_t joltStart_ = 0;
  // Moved by more than stillness but less than a tap, since unsettledSince_.
  bool unsettled_ = false;
  uint32_t unsettledSince_ = 0;
  bool haveTap_ = false;
  uint32_t lastTap_ = 0;
  bool lockedOut_ = false;
  uint32_t lockoutStart_ = 0;
};

// Which way up a Device stands: the rotation (0, 90, 180, 270 degrees
// clockwise) that shows the picture upright, for auto-rotation
// (DeviceCore::orient). It reads the accelerometer along the matrix's
// right and down, as the picture lies unturned; at rest that points up. A new
// rotation takes kSettleMs of the Device standing that way, and another edge
// must be up by kMarginMg more than the current one, so a Device near 45
// degrees, or jolted by a tap, doesn't flip back and forth. Lying flat (its
// face up or down) keeps the rotation it had.
class OrientationSensor {
 public:
  static constexpr uint32_t kSettleMs = 500;
  static constexpr int kMarginMg = 300;
  // Under this along the matrix, the Device lies flat.
  static constexpr int kStandingMg = 600;

  // The reading along the matrix's right and down, read at clock time
  // `nowMs`. True when the rotation changed.
  bool update(int right, int down, uint32_t nowMs);
  uint16_t rotation() const { return rotation_; }

 private:
  uint16_t rotation_ = 0;
  bool pending_ = false;
  uint16_t candidate_ = 0;
  uint32_t candidateSince_ = 0;
};

}  // namespace device
