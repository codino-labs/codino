#include "imu_gestures.h"

namespace device {

namespace {

int64_t squared(int64_t x, int64_t y, int64_t z) { return x * x + y * y + z * z; }

}  // namespace

bool TapDetector::update(int x, int y, int z, uint32_t nowMs) {
  if (!started_) {
    started_ = true;
    gx_ = x, gy_ = y, gz_ = z;
  }
  const int64_t jolt = squared(x - gx_, y - gy_, z - gz_);
  const int64_t quiet = int64_t{kQuietMg} * kQuietMg;

  if (jolting_) {
    if (jolt >= quiet) {
      if (nowMs - joltStart_ >= kRestMs) {
        jolting_ = false;
        gx_ = x, gy_ = y, gz_ = z;
      }
      return false;
    }
    // It has rung out: a tap if it was short.
    jolting_ = false;
    return nowMs - joltStart_ <= kMaxTapMs && tapped(joltStart_);
  }
  if (jolt > int64_t{kTapMg} * kTapMg) {
    unsettled_ = false;
    jolting_ = true;
    joltStart_ = nowMs;
    return false;
  }
  if (jolt < quiet) {
    // Follows a slow change of how the Device stands (an eighth per sample).
    unsettled_ = false;
    gx_ += (x - gx_) / 8;
    gy_ += (y - gy_) / 8;
    gz_ += (z - gz_) / 8;
    return false;
  }
  // Moved by less than a tap: once that lasts, the Device stands another way.
  if (!unsettled_) {
    unsettled_ = true;
    unsettledSince_ = nowMs;
  } else if (nowMs - unsettledSince_ >= kRestMs) {
    unsettled_ = false;
    gx_ = x, gy_ = y, gz_ = z;
  }
  return false;
}

bool TapDetector::tapped(uint32_t at) {
  if (lockedOut_ && at - lockoutStart_ < kLockoutMs) return false;
  lockedOut_ = false;
  const uint32_t gap = at - lastTap_;
  if (haveTap_ && gap >= kMinGapMs && gap <= kMaxGapMs) {
    haveTap_ = false;
    lockedOut_ = true;
    lockoutStart_ = at;
    return true;
  }
  haveTap_ = true;
  lastTap_ = at;
  return false;
}

bool OrientationSensor::update(int right, int down, uint32_t nowMs) {
  if (int64_t{right} * right + int64_t{down} * down < int64_t{kStandingMg} * kStandingMg) {
    pending_ = false;
    return false;
  }
  // How far up each edge is, by the rotation that brings it to the top of
  // the picture: the top edge (0), the right edge (90), the bottom (180), the
  // left (270).
  const int up[4] = {-down, right, down, -right};
  int best = 0;
  for (int i = 1; i < 4; ++i)
    if (up[i] > up[best]) best = i;
  const uint16_t degrees = static_cast<uint16_t>(best * 90);
  if (degrees == rotation_ || up[best] - up[rotation_ / 90] < kMarginMg) {
    pending_ = false;
    return false;
  }
  if (!pending_ || candidate_ != degrees) {
    pending_ = true;
    candidate_ = degrees;
    candidateSince_ = nowMs;
  }
  if (nowMs - candidateSince_ < kSettleMs) return false;
  pending_ = false;
  rotation_ = degrees;
  return true;
}

}  // namespace device
