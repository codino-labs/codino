#include "littlefs_file_system.h"

#include <LittleFS.h>

namespace device {

namespace {

// Makes every directory above `path`.
void makeParents(const std::string& path) {
  for (size_t slash = path.find('/', 1); slash != std::string::npos; slash = path.find('/', slash + 1)) {
    const std::string dir = path.substr(0, slash);
    if (!LittleFS.exists(dir.c_str())) LittleFS.mkdir(dir.c_str());
  }
}

}  // namespace

bool LittleFsFileSystem::read(const std::string& path, std::string& content) {
  if (!LittleFS.exists(path.c_str())) return false;
  File file = LittleFS.open(path.c_str(), "r");
  if (!file || file.isDirectory()) return false;
  content.resize(file.size());
  const size_t got = file.read(reinterpret_cast<uint8_t*>(&content[0]), content.size());
  content.resize(got);
  return true;
}

bool LittleFsFileSystem::write(const std::string& path, std::string_view content) {
  makeParents(path);
  const std::string temporary = path + ".tmp";
  {
    File file = LittleFS.open(temporary.c_str(), "w");
    if (!file) return false;
    const size_t wrote = file.write(reinterpret_cast<const uint8_t*>(content.data()), content.size());
    file.close();
    if (wrote != content.size()) {
      LittleFS.remove(temporary.c_str());
      return false;
    }
  }
  if (LittleFS.rename(temporary.c_str(), path.c_str())) return true;
  // Should the rename not replace an existing file, replace it in two steps
  // (not atomic: a power cut in between loses the old content).
  LittleFS.remove(path.c_str());
  if (LittleFS.rename(temporary.c_str(), path.c_str())) return true;
  LittleFS.remove(temporary.c_str());
  return false;
}

bool LittleFsFileSystem::remove(const std::string& path) {
  return LittleFS.exists(path.c_str()) && LittleFS.remove(path.c_str());
}

std::vector<std::string> LittleFsFileSystem::list(const std::string& dir) {
  std::vector<std::string> names;
  if (!LittleFS.exists(dir.c_str())) return names;
  File folder = LittleFS.open(dir.c_str());
  if (!folder || !folder.isDirectory()) return names;
  for (File file = folder.openNextFile(); file; file = folder.openNextFile()) {
    if (file.isDirectory()) continue;
    std::string name = file.name();
    const size_t slash = name.rfind('/');  // some cores give the whole path
    names.push_back(slash == std::string::npos ? name : name.substr(slash + 1));
  }
  return names;
}

}  // namespace device
