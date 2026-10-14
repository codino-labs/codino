# Thin wrapper around CMake so the commands are easy to remember.
BUILD := build

.PHONY: all test virtual-device clean

all: $(BUILD)/CMakeCache.txt
	cmake --build $(BUILD) -j

$(BUILD)/CMakeCache.txt:
	cmake -S . -B $(BUILD) -DCMAKE_BUILD_TYPE=Debug

test: all
	ctest --test-dir $(BUILD) --output-on-failure

virtual-device: all

clean:
	rm -rf $(BUILD)
