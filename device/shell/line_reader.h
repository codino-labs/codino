// Splits the bytes of the serial link into protocol lines: JSON messages and
// binary Raw Mode frames alike, since no frame byte is a newline
// (protocol/README.md, Framing).
#pragma once

#include <cstddef>
#include <string>
#include <string_view>

#include "device_core.h"

namespace device {

class LineReader {
 public:
  // The buffer is allocated once, at its full size, so a long session of
  // installs and frames never fragments the heap.
  LineReader() { line_.reserve(kMaxContent); }

  // Feeds the bytes of one read, as they come; calls onLine(std::string_view)
  // for every line they complete, without its newline and otherwise byte for
  // byte (a trailing '\r' stays: the Device core tolerates it in JSON and
  // must see it in a frame). A line longer than the Device's line buffer
  // (DeviceCore::kMaxLineLength, newline included) is dropped up to its
  // newline, as the protocol says a Device ignores it.
  template <typename OnLine>
  void feed(const char* data, size_t size, OnLine&& onLine) {
    for (size_t i = 0; i < size; ++i) {
      const char byte = data[i];
      if (byte == '\n') {
        if (!overflowed_) onLine(std::string_view(line_));
        line_.clear();
        overflowed_ = false;
      } else if (overflowed_) {
        // Skipping the rest of a line too long to keep.
      } else if (line_.size() == kMaxContent) {
        line_.clear();
        overflowed_ = true;
      } else {
        line_ += byte;
      }
    }
  }

 private:
  static constexpr size_t kMaxContent = DeviceCore::kMaxLineLength - 1;
  std::string line_;
  bool overflowed_ = false;
};

}  // namespace device
