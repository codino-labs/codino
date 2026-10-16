// The firmware shell's physical inputs, tested natively: the button and the
// IMU's gestures (double-tap, orientation). The hardware glue reading GPIO 0
// and the QMI8658 (device/firmware) is verified by hand on the reference
// board.
#include <doctest.h>

#include <cstdint>

#include "button.h"
#include "imu_gestures.h"

namespace {

// Feeds the button `down` from `from` to `to` (exclusive), one reading per
// ms as the firmware's loop does, and counts the presses.
int pressesWhile(device::Button& button, bool down, uint32_t from, uint32_t to) {
  int presses = 0;
  for (uint32_t t = from; t < to; ++t) presses += button.update(down, t) ? 1 : 0;
  return presses;
}

// Acceleration in milli-g along the IMU's axes.
struct Accel {
  int x, y, z;
};

// A Device on the desk, fed accelerometer samples every kSampleMs as the
// firmware reads them. Gravity is 1000 mg along `rest` (the Device lying
// flat: +z).
struct Motion {
  static constexpr uint32_t kSampleMs = 5;
  device::TapDetector taps;
  uint32_t now = 0;
  Accel rest{0, 0, 1000};
  int doubleTaps = 0;

  void sample(Accel a) {
    doubleTaps += taps.update(a.x, a.y, a.z, now) ? 1 : 0;
    now += kSampleMs;
  }
  // Lying still for `ms`.
  void still(uint32_t ms) {
    for (uint32_t t = 0; t < ms; t += kSampleMs) sample(rest);
  }
  // A finger's tap: a sharp jolt of `mg` that rings out within 15 ms.
  void tap(int mg = 2500) {
    sample({rest.x, rest.y, rest.z + mg});
    sample({rest.x, rest.y, rest.z - mg / 2});
    sample({rest.x, rest.y, rest.z + mg / 5});
  }
};

// A Device standing somewhere, fed its accelerometer reading along the
// matrix's right and down every kSampleMs. At rest the reading points up,
// away from the ground: standing upright it is {0, -1000, 0}.
struct Standing {
  static constexpr uint32_t kSampleMs = 5;
  device::OrientationSensor sensor;
  uint32_t now = 0;
  int changes = 0;

  void hold(Accel a, uint32_t ms) {
    for (uint32_t t = 0; t < ms; t += kSampleMs) {
      changes += sensor.update(a.x, a.y, now) ? 1 : 0;
      now += kSampleMs;
    }
  }
};

// Readings of a Device standing on each of its edges.
constexpr Accel kUpright{0, -1000, 0};      // its top edge up
constexpr Accel kOnLeftEdge{1000, 0, 0};    // its right edge up
constexpr Accel kUpsideDown{0, 1000, 0};    // its bottom edge up
constexpr Accel kOnRightEdge{-1000, 0, 0};  // its left edge up
constexpr Accel kFlat{0, 0, 1000};          // lying on its back

}  // namespace

// --- Button -----------------------------------------------------------------

TEST_CASE("holding the button down is one press, once it has settled") {
  device::Button button;
  CHECK(pressesWhile(button, false, 0, 100) == 0);

  CHECK(pressesWhile(button, true, 100, device::Button::kSettleMs + 100) == 0);
  CHECK(pressesWhile(button, true, device::Button::kSettleMs + 100, 2000) == 1);
}

TEST_CASE("a button whose contacts bounce is still one press") {
  device::Button button;
  int presses = 0;
  // 10 ms of chatter, a reading per ms, then held down.
  for (uint32_t t = 0; t < 10; ++t) presses += button.update(t % 2 == 0, t) ? 1 : 0;
  presses += pressesWhile(button, true, 10, 500);
  // It bounces on release too.
  for (uint32_t t = 500; t < 510; ++t) presses += button.update(t % 2 == 1, t) ? 1 : 0;
  presses += pressesWhile(button, false, 510, 1000);

  CHECK(presses == 1);
}

TEST_CASE("a glitch shorter than the settle time is no press") {
  device::Button button;

  CHECK(pressesWhile(button, true, 0, device::Button::kSettleMs - 1) == 0);
  CHECK(pressesWhile(button, false, device::Button::kSettleMs - 1, 500) == 0);
}

TEST_CASE("pressing again after a release is another press") {
  device::Button button;

  CHECK(pressesWhile(button, true, 0, 200) == 1);
  CHECK(pressesWhile(button, false, 200, 400) == 0);
  CHECK(pressesWhile(button, true, 400, 600) == 1);
}

// --- Double-tap -------------------------------------------------------------

TEST_CASE("two taps in quick succession are a double-tap") {
  Motion motion;
  motion.still(1000);

  motion.tap();
  motion.still(250);
  motion.tap();
  motion.still(1000);

  CHECK(motion.doubleTaps == 1);
}

TEST_CASE("one tap, or two far apart, is no double-tap") {
  Motion motion;
  motion.still(1000);
  motion.tap();
  motion.still(1000);
  motion.tap();
  motion.still(1000);

  CHECK(motion.doubleTaps == 0);
}

TEST_CASE("bumps on the desk are no double-tap") {
  Motion motion;
  motion.still(1000);
  for (int i = 0; i < 10; ++i) {
    motion.tap(600);
    motion.still(150);
  }

  CHECK(motion.doubleTaps == 0);
}

TEST_CASE("shoving the Device twice is no double-tap") {
  Motion motion;
  motion.still(1000);
  for (int i = 0; i < 2; ++i) {
    for (uint32_t t = 0; t < 150; t += Motion::kSampleMs) motion.sample({1500, 0, 1000});
    motion.still(100);
  }
  motion.still(1000);

  CHECK(motion.doubleTaps == 0);
}

TEST_CASE("three quick taps are one double-tap") {
  Motion motion;
  motion.still(1000);
  for (int i = 0; i < 3; ++i) {
    motion.tap();
    motion.still(200);
  }
  motion.still(1000);

  CHECK(motion.doubleTaps == 1);
}

TEST_CASE("a Device set down on a sloped stand still takes a double-tap") {
  Motion motion;
  motion.still(1000);
  // Set down at about 35 degrees at once: gravity moves by 600 mg, more than
  // stillness and less than a tap.
  motion.rest = {0, 574, 819};
  motion.still(1000);

  motion.tap();
  motion.still(250);
  motion.tap();
  motion.still(1000);

  CHECK(motion.doubleTaps == 1);
}

TEST_CASE("a Device stood up on its edge still takes a double-tap") {
  Motion motion;
  motion.still(1000);
  // Picked up and stood on its edge at once: gravity moves from z to x.
  motion.rest = {1000, 0, 0};
  motion.still(1000);

  motion.tap();
  motion.still(250);
  motion.tap();
  motion.still(1000);

  CHECK(motion.doubleTaps == 1);
}

// --- Orientation ------------------------------------------------------------

TEST_CASE("a Device stood on its left edge turns the picture by 90 degrees") {
  Standing device;
  device.hold(kUpright, 1000);

  device.hold(kOnLeftEdge, 1000);

  CHECK(device.sensor.rotation() == 90);
  CHECK(device.changes == 1);
}

TEST_CASE("each edge the Device stands on has its rotation") {
  const struct {
    Accel reading;
    uint16_t rotation;
  } edges[] = {{kOnLeftEdge, 90}, {kUpsideDown, 180}, {kOnRightEdge, 270}, {kUpright, 0}};
  Standing device;
  for (const auto& edge : edges) {
    CAPTURE(edge.rotation);
    device.hold(edge.reading, 1000);
    CHECK(device.sensor.rotation() == edge.rotation);
  }
}

TEST_CASE("lying flat keeps the rotation the Device had") {
  Standing device;
  device.hold(kUpsideDown, 1000);

  device.hold(kFlat, 2000);
  device.hold({0, 0, -1000}, 2000);  // face down

  CHECK(device.sensor.rotation() == 180);
}

TEST_CASE("standing another way for less than the settle time turns nothing") {
  Standing device;
  device.hold(kUpright, 1000);

  device.hold(kOnLeftEdge, device::OrientationSensor::kSettleMs - 100);
  device.hold(kUpright, 1000);

  CHECK(device.sensor.rotation() == 0);
  CHECK(device.changes == 0);
}

TEST_CASE("a Device standing near 45 degrees doesn't flip back and forth") {
  Standing device;
  device.hold(kUpright, 1000);
  // Wobbling between 40 and 50 degrees towards its left edge.
  for (int i = 0; i < 10; ++i) {
    device.hold({643, -766, 0}, 700);
    device.hold({766, -643, 0}, 700);
  }

  CHECK(device.changes == 0);
}
