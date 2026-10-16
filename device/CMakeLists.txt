cmake_minimum_required(VERSION 3.16)
project(device CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
  add_compile_options(-Wall -Wextra -Wpedantic -Werror)
endif()

set(REPO_ROOT ${CMAKE_CURRENT_SOURCE_DIR}/..)

# Device core: portable C++17, no Arduino or FastLED dependencies.
add_library(device_core STATIC core/device_core.cpp core/device_core_themes.cpp core/theme.cpp core/builtin_themes.cpp)
target_include_directories(device_core PUBLIC core)
target_include_directories(device_core SYSTEM PUBLIC third_party)

# The firmware shell's portable parts: line framing, LED wiring, the stored
# Hardware Profile, flash stores. No Arduino or FastLED either.
add_library(device_shell STATIC shell/flash_store.cpp shell/hardware_profile.cpp shell/imu_gestures.cpp
  shell/led_wiring.cpp)
target_include_directories(device_shell PUBLIC shell)
target_link_libraries(device_shell PUBLIC device_core)

# Native Device core tests (doctest).
enable_testing()
add_executable(device_core_test test/main.cpp test/device_core_test.cpp test/theme_test.cpp
  test/theme_snapshot_test.cpp test/shell_test.cpp test/input_test.cpp)
target_link_libraries(device_core_test PRIVATE device_core device_shell)
target_compile_definitions(device_core_test PRIVATE
  PROTOCOL_FIXTURES_DIR="${REPO_ROOT}/protocol/fixtures"
  GOLDEN_DIR="${CMAKE_CURRENT_SOURCE_DIR}/test/golden"
  THEME_EXAMPLES_DIR="${REPO_ROOT}/themes/examples")
add_test(NAME device_core_test COMMAND device_core_test)

# Terminal Virtual Device.
add_executable(virtual-device virtual/terminal/main.cpp)
find_package(Threads REQUIRED)
target_link_libraries(virtual-device PRIVATE device_core device_shell Threads::Threads)

# The web flasher's Hardware Profile encoder (flasher/), against the golden
# files the C++ tests also check. Needs Node; skipped without it.
find_program(NODE node)
if(NODE)
  add_test(NAME flasher_test COMMAND ${NODE} --test ${CMAKE_CURRENT_SOURCE_DIR}/flasher/hardware_profile.test.mjs)
else()
  message(STATUS "node not found: the web flasher's encoder tests are skipped")
endif()
