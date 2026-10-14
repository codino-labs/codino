# Thin wrapper around CMake so the commands are easy to remember.
BUILD := build

.PHONY: all test update-goldens virtual-device clean

all: $(BUILD)/CMakeCache.txt
	cmake --build $(BUILD) -j

$(BUILD)/CMakeCache.txt:
	cmake -S . -B $(BUILD) -DCMAKE_BUILD_TYPE=Debug

test: all
	ctest --test-dir $(BUILD) --output-on-failure

# Regenerates the Theme snapshot golden frames (test/golden/). Run it only
# when a change is meant to change how a built-in Theme looks, and review the
# diff before committing it.
update-goldens: all
	UPDATE_GOLDENS=1 $(BUILD)/device_core_test -tc="snapshot:*"

virtual-device: all

clean:
	rm -rf $(BUILD)
