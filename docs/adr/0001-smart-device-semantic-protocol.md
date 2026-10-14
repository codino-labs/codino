# Smart Device with a semantic protocol, plus Raw Mode

The Host sends the Device semantic messages ("session X is now Awaiting Input") and the firmware owns all rendering: Themes, animations, Escalation and the brightness cap. We rejected a dumb pixel pipe driven by the Host. A smart Device keeps animating smoothly when the Host stalls, shows Disconnected on its own, and needs only a thin Host. Raw Mode exists as an escape hatch for custom pixels, but Awaiting Input always pre-empts it: the Device must never hide the one state it exists to show.
