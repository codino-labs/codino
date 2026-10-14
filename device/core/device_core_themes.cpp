// The Device core's Themes and Sprite Packs: installing and uninstalling
// them in the ThemeStore, and selecting the Theme to render by.
#include <ArduinoJson.h>

#include <algorithm>

#include "device_core.h"

namespace device {

namespace {

std::pair<InstallKind, std::string> key(InstallKind kind, std::string_view name) { return {kind, std::string(name)}; }

}  // namespace

std::vector<std::string> MemoryThemeStore::names(InstallKind kind) {
  std::vector<std::string> out;
  for (const auto& [k, line] : lines_)
    if (k.first == kind) out.push_back(k.second);
  return out;
}

std::string MemoryThemeStore::load(InstallKind kind, std::string_view name) {
  auto it = lines_.find(key(kind, name));
  return it == lines_.end() ? std::string() : it->second;
}

bool MemoryThemeStore::save(InstallKind kind, std::string_view name, std::string_view line) {
  lines_[key(kind, name)] = std::string(line);
  return true;
}

bool MemoryThemeStore::erase(InstallKind kind, std::string_view name) { return lines_.erase(key(kind, name)) == 1; }

bool DeviceCore::lookUp(InstallKind kind, std::string_view name, JsonDocument& doc) const {
  if (const BuiltIn* builtIn = findBuiltIn(kind, name)) return !deserializeJson(doc, builtIn->json);
  const std::string line = store_->load(kind, name);
  return !line.empty() && !deserializeJson(doc, line) && doc.is<JsonObjectConst>();
}

std::vector<std::string> DeviceCore::available(InstallKind kind) const {
  std::vector<std::string> out;
  for (const BuiltIn& b : builtIns(kind)) out.emplace_back(b.name);
  std::vector<std::string> installed = store_->names(kind);
  std::sort(installed.begin(), installed.end());
  for (auto& name : installed)
    if (findBuiltIn(kind, name) == nullptr && isValidName(name)) out.push_back(std::move(name));
  return out;
}

void DeviceCore::addAvailable(JsonDocument& reply) const {
  JsonArray themes = reply["themes"].to<JsonArray>();
  for (const auto& name : available(InstallKind::Theme)) themes.add(name);
  JsonArray packs = reply["sprite_packs"].to<JsonArray>();
  for (const auto& name : available(InstallKind::SpritePack)) packs.add(name);
}

// The reply to an install or uninstall: refused when `error` says why.
std::string DeviceCore::installReply(JsonVariantConst message, const std::string& error) const {
  JsonDocument reply;
  reply["type"] = "install_reply";
  reply["kind"] = message["kind"];
  reply["name"] = message["name"];
  reply["ok"] = error.empty();
  if (!error.empty()) reply["error"] = error;
  addAvailable(reply);
  std::string line;
  serializeJson(reply, line);
  return line;
}

std::string DeviceCore::install(JsonVariantConst message) {
  auto answer = [&](const std::string& error) { return installReply(message, error); };
  InstallKind kind;
  if (!parseInstallKind(message["kind"] | "", kind)) return answer("kind must be theme or sprite_pack");
  std::string error, name;
  if (kind == InstallKind::Theme) {
    Theme theme;
    if (!parseTheme(message, theme, error)) return answer(error);
    name = theme.name;
  } else {
    SpritePack pack;
    if (!parseSpritePack(message, pack, error)) return answer(error);
    name = pack.name;
  }
  if (findBuiltIn(kind, name) != nullptr) return answer("\"" + name + "\" is built in: choose another name");
  const std::vector<std::string> installed = store_->names(kind);
  if (std::find(installed.begin(), installed.end(), name) == installed.end() && installed.size() >= kMaxInstalled)
    return answer("the Device holds " + std::to_string(kMaxInstalled) + " already: uninstall one first");
  std::string line;
  serializeJson(message, line);
  if (!store_->save(kind, name, line)) return answer("the Device couldn't save it");
  selectTheme();
  return answer("");
}

std::string DeviceCore::uninstall(JsonVariantConst message) {
  auto answer = [&](const std::string& error) { return installReply(message, error); };
  InstallKind kind;
  if (!parseInstallKind(message["kind"] | "", kind)) return answer("kind must be theme or sprite_pack");
  const std::string_view name = message["name"] | "";
  if (findBuiltIn(kind, name) != nullptr) return answer("built-in ones can't be uninstalled");
  const std::vector<std::string> installed = store_->names(kind);
  if (!isValidName(name) || std::find(installed.begin(), installed.end(), name) == installed.end())
    return answer("not installed");
  if (!store_->erase(kind, name)) return answer("the Device couldn't remove it");
  selectTheme();
  return answer("");
}

void DeviceCore::selectTheme() {
  JsonDocument doc;
  std::string error;
  Theme theme;
  if (!lookUp(InstallKind::Theme, preferences_.theme, doc) || !parseTheme(doc.as<JsonVariantConst>(), theme, error)) {
    lookUp(InstallKind::Theme, kDefaultTheme, doc);
    parseTheme(doc.as<JsonVariantConst>(), theme, error);
  }
  theme_ = std::move(theme);
  poses_ = {};
  // Each Sprite Pack is read once; an empty one stands for a missing pack.
  std::map<std::string, SpritePack> packs;
  for (size_t i = 0; i < kSessionStateCount; ++i) {
    const ThemeState& state = theme_.states[i];
    if (!state.usesSprite) continue;
    auto it = packs.find(state.sprite.pack);
    if (it == packs.end()) {
      SpritePack pack;
      JsonDocument packDoc;
      if (!lookUp(InstallKind::SpritePack, state.sprite.pack, packDoc) ||
          !parseSpritePack(packDoc.as<JsonVariantConst>(), pack, error))
        pack = SpritePack{};
      it = packs.emplace(state.sprite.pack, std::move(pack)).first;
    }
    const SpritePack& pack = it->second;
    Pose pose;
    pose.width = pack.width;
    pose.height = pack.height;
    pose.frameMs = state.sprite.frameMs;
    for (const auto& name : state.sprite.frames) {
      auto frame = pack.frames.find(name);
      if (frame == pack.frames.end()) {
        pose.frames.clear();  // a pose with a frame missing can't show
        break;
      }
      pose.frames.push_back(frame->second);
    }
    poses_[i] = std::move(pose);
  }
}

}  // namespace device
