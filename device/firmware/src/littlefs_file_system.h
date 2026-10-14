// The shell's FileSystem (../shell/flash_store.h) on LittleFS.
#pragma once

#include "flash_store.h"

namespace device {

class LittleFsFileSystem : public FileSystem {
 public:
  bool read(const std::string& path, std::string& content) override;
  // Writes PATH.tmp, then renames it over PATH: LittleFS renames atomically,
  // so a power cut leaves the old content or the new. Should the rename
  // refuse to replace PATH, it removes PATH first, and then a power cut in
  // between loses the old content.
  bool write(const std::string& path, std::string_view content) override;
  bool remove(const std::string& path) override;
  std::vector<std::string> list(const std::string& dir) override;
};

}  // namespace device
