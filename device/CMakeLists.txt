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
add_library(device_core STATIC core/device_core.cpp)
target_include_directories(device_core PUBLIC core)
target_include_directories(device_core SYSTEM PUBLIC third_party)

# Native Device core tests (doctest).
enable_testing()
add_executable(device_core_test test/main.cpp test/device_core_test.cpp)
target_link_libraries(device_core_test PRIVATE device_core)
target_compile_definitions(device_core_test PRIVATE
  PROTOCOL_FIXTURES_DIR="${REPO_ROOT}/protocol/fixtures")
add_test(NAME device_core_test COMMAND device_core_test)

# Terminal Virtual Device.
add_executable(virtual-device virtual/terminal/main.cpp)
target_link_libraries(virtual-device PRIVATE device_core)
