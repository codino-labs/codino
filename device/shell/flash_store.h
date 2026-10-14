// The Device's flash as the Device core's storage seams see it: the
// Preferences cache and the Theme store, kept in files of a FileSystem
// (LittleFS on the firmware, an in-memory one in the tests).
#pragma once

#include <string>
#include <string_view>
#include <vector>

#include "device_core.h"

namespace device {

// The few file operations the stores need. Paths are absolute, '/'-separated.
class FileSystem {
 public:
  virtual ~FileSystem() = default;
  // The whole content of the file at `path`; false when there is none.
  virtual bool read(const std::string& path, std::string& content) = 0;
  // Replaces the file's content, creating the directories it is in, so that
  // a reboot or a power cut mid-write leaves the old content or the new,
  // never half. False when it couldn't be written (the old content stays).
  virtual bool write(const std::string& path, std::string_view content) = 0;
  // False when there was nothing to remove or it couldn't be removed.
  virtual bool remove(const std::string& path) = 0;
  // The names (not paths) of the files in the directory `dir`; none when
  // there is no such directory.
  virtual std::vector<std::string> list(const std::string& dir) = 0;
};

// The Preferences cache in one file: the canonical `preferences` line.
class FilePreferencesCache : public PreferencesCache {
 public:
  FilePreferencesCache(FileSystem& files, std::string path) : files_(files), path_(std::move(path)) {}
  std::string load() override;
  void save(std::string_view line) override;

 private:
  FileSystem& files_;
  std::string path_;
};

// Installed Themes and Sprite Packs, one `install` line per file:
// DIR/theme/NAME.jsonl and DIR/sprite_pack/NAME.jsonl. The Device core hands
// over only valid names (1-32 of a-z, 0-9, - and _), so a name is a safe
// file name.
class FileThemeStore : public ThemeStore {
 public:
  FileThemeStore(FileSystem& files, std::string dir) : files_(files), dir_(std::move(dir)) {}
  std::vector<std::string> names(InstallKind kind) override;
  std::string load(InstallKind kind, std::string_view name) override;
  bool save(InstallKind kind, std::string_view name, std::string_view line) override;
  bool erase(InstallKind kind, std::string_view name) override;

 private:
  std::string folder(InstallKind kind) const;
  std::string path(InstallKind kind, std::string_view name) const;
  FileSystem& files_;
  std::string dir_;
};

}  // namespace device
