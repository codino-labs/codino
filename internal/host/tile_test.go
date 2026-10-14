package host_test

import (
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/host"
)

// Tile slots (#10). The adaptive Layout has room for 1 Tile with one
// session, 2 (halves) with two, 4 (quadrants) with three or four, 8 (columns)
// with five to eight, and 7 plus the Overflow Tile with nine or more. A
// session keeps its Tile slot while it fits; a session that doesn't (the
// Layout shrank, or it was in the Overflow Tile) and a newcomer take the
// first free slot, in order of session start time. Sessions beyond the room
// get slots from 7 up: the Overflow Tile.

// tiles returns the latest Tile slot the Device was sent for each session.
func tiles(t *testing.T, dev *fakeDevice) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, u := range upserts(t, dev.Received()) {
		got[u.ID] = u.Tile
	}
	return got
}

// claimedHost is a Host that has claimed a reference fake Device, and its
// clock.
func claimedHost(t *testing.T, opts ...host.Option) (*host.Host, *fakeDevice, *fakeClock) {
	t.Helper()
	clock := clockAt(1760432400000)
	dev := newReferenceDevice(t)
	h := host.New(clock, devices(dev), opts...)
	t.Cleanup(h.Close)
	h.Tick()
	return h, dev, clock
}

// start starts sessions one after the other, a second apart.
func start(t *testing.T, h *host.Host, clock *fakeClock, ids ...string) {
	t.Helper()
	for _, id := range ids {
		clock.Advance(time.Second)
		apply(t, h, host.Event{SessionID: id, State: host.Working})
	}
}

func end(t *testing.T, h *host.Host, ids ...string) {
	t.Helper()
	for _, id := range ids {
		apply(t, h, host.Event{SessionID: id, End: true})
	}
}

func assertTiles(t *testing.T, dev *fakeDevice, alive []string, want map[string]int) {
	t.Helper()
	got := tiles(t, dev)
	for _, id := range alive {
		if tile, ok := got[id]; !ok || tile != want[id] {
			t.Errorf("tiles %v, want %v", got, want)
			return
		}
	}
}

func TestWhenTheLayoutShrinksSessionsThatNoLongerFitMoveToFreeTiles(t *testing.T) {
	h, dev, clock := claimedHost(t)
	start(t, h, clock, "a", "b", "c") // quadrants: 0 1 2

	sent := len(dev.Received())

	end(t, h, "b") // halves: c can't stay in slot 2

	assertMessages(t, since(dev, sent), []string{
		`{"type":"session_remove","id":"b"}`,
		`{"type":"session_upsert","id":"c","state":"working","project_color":"#ff3030","state_entered":1760432403000,"tile":1}`,
	})
}

func TestSessionsThatMoveTakeTheFreeTilesInOrderOfSessionStartTime(t *testing.T) {
	h, dev, clock := claimedHost(t)
	start(t, h, clock, "a", "b", "c", "d") // quadrants 0 1 2 3
	end(t, h, "c")
	start(t, h, clock, "e") // started after d, takes slot 2
	assertTiles(t, dev, []string{"a", "b", "d", "e"}, map[string]int{"a": 0, "b": 1, "d": 3, "e": 2})

	end(t, h, "a", "b") // halves: d and e both move

	assertTiles(t, dev, []string{"d", "e"}, map[string]int{"d": 0, "e": 1})
}

func TestFiveToEightSessionsKeepTheirTilesAsColumns(t *testing.T) {
	h, dev, clock := claimedHost(t)

	start(t, h, clock, "a", "b", "c", "d", "e", "f", "g", "h")

	assertTiles(t, dev, []string{"a", "b", "c", "d", "e", "f", "g", "h"},
		map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4, "f": 5, "g": 6, "h": 7})
	if n := len(upserts(t, dev.Received())); n != 8 {
		t.Errorf("%d upserts, want one per session: nobody moved", n)
	}
}

func TestBeyondEightSessionsTheNewestGoToTheOverflowTile(t *testing.T) {
	h, dev, clock := claimedHost(t)
	ids := []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9"}
	start(t, h, clock, ids...)

	// Seven columns and the Overflow Tile: s7, s8 and s9 are in it.
	want := map[string]int{}
	for i, id := range ids {
		want[id] = i
	}
	assertTiles(t, dev, ids, want)
	if n := len(upserts(t, dev.Received())); n != len(ids) {
		t.Errorf("%d upserts, want one per session", n)
	}

	// A column frees up: the earliest session in the Overflow Tile takes it.
	end(t, h, "s2")
	want["s7"] = 2
	assertTiles(t, dev, []string{"s0", "s1", "s3", "s4", "s5", "s6", "s7", "s8", "s9"}, want)

	// Eight sessions fit in columns: s8 and s9 leave the Overflow Tile for
	// the free columns, the earlier one first.
	end(t, h, "s0")
	want["s8"], want["s9"] = 0, 7
	assertTiles(t, dev, []string{"s1", "s3", "s4", "s5", "s6", "s7", "s8", "s9"}, want)
}

func TestAReapedSessionFreesItsTileLikeAnEndedOne(t *testing.T) {
	liveness := &fakeLiveness{exits: map[int]time.Time{}}
	h, dev, clock := claimedHost(t, host.WithLivenessProbe(liveness))
	liveness.clock = clock
	for i, id := range []string{"a", "b", "c"} {
		clock.Advance(time.Second)
		apply(t, h, host.Event{SessionID: id, State: host.Working, PID: 100 + i})
	}

	liveness.exits[101] = clock.Now() // b's Agent was killed
	clock.Advance(host.ProbeInterval)
	h.Tick()

	assertTiles(t, dev, []string{"a", "c"}, map[string]int{"a": 0, "c": 1})
	if got := h.Status().Sessions; len(got) != 2 {
		t.Errorf("sessions %v, want a and c", got)
	}
}
