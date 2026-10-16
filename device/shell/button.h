// A push button read by polling (the reference board's BOOT button on GPIO 0):
// its contacts bounce for a few ms, so a level counts once it has held for
// kSettleMs, and each settled press is one press, however long it is held.
#pragma once

#include <cstdint>

namespace device {

class Button {
 public:
  static constexpr uint32_t kSettleMs = 20;

  // The button's level (`down`: pressed) read at clock time `nowMs`. True
  // once per press: when it has settled down.
  bool update(bool down, uint32_t nowMs) {
    if (down != level_) {
      level_ = down;
      since_ = nowMs;
    }
    if (level_ == settled_ || nowMs - since_ < kSettleMs) return false;
    settled_ = level_;
    return settled_;
  }

 private:
  bool level_ = false;    // the last reading
  uint32_t since_ = 0;    // when the reading last changed
  bool settled_ = false;  // the level that has held for kSettleMs
};

}  // namespace device
