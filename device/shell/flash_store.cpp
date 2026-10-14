#include "flash_store.h"

namespace device {

namespace {

constexpr std::string_view kExtension = ".jsonl";

// A stored file holds one line and its newline.
std::string readLine(FileSystem& files, const std::string& path) {
  std::string content;
  if (!files.read(path, content)) return {};
  return content.substr(0, content.find('\n'));
}

}  // namespace

std::string FilePreferencesCache::load() { return readLine(files_, path_); }

void FilePreferencesCache::save(std::string_view line) {
  files_.write(path_, std::string(line) + '\n');
}

std::vector<std::string> FileThemeStore::names(InstallKind kind) {
  std::vector<std::string> names;
  for (const std::string& file : files_.list(folder(kind))) {
    // Only NAME.jsonl: not a temporary file a write cut short left behind.
    if (file.size() > kExtension.size() && file.compare(file.size() - kExtension.size(), kExtension.size(), kExtension) == 0) {
      names.push_back(file.substr(0, file.size() - kExtension.size()));
    }
  }
  return names;
}

std::string FileThemeStore::load(InstallKind kind, std::string_view name) { return readLine(files_, path(kind, name)); }

bool FileThemeStore::save(InstallKind kind, std::string_view name, std::string_view line) {
  return files_.write(path(kind, name), std::string(line) + '\n');
}

bool FileThemeStore::erase(InstallKind kind, std::string_view name) { return files_.remove(path(kind, name)); }

std::string FileThemeStore::folder(InstallKind kind) const { return dir_ + "/" + wireName(kind); }

std::string FileThemeStore::path(InstallKind kind, std::string_view name) const {
  return folder(kind) + "/" + std::string(name) + std::string(kExtension);
}

}  // namespace device
