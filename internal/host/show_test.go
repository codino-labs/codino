package host_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/host"
	"github.com/codino-labs/codino/internal/picture"
	"github.com/codino-labs/codino/internal/preferences"
)

// decode decodes an encoded image or GIF for host.Show.
func decode(t *testing.T, data []byte) *picture.Picture {
	t.Helper()
	p, err := picture.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return p
}

// encodePNG encodes img as a PNG.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// rawFixtureImage is the 8x8 picture protocol/fixtures/raw-frame.bin carries.
func rawFixtureImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{uint8(x * 32), uint8(y * 32), 0x80, 0xff})
		}
	}
	return img
}

// show shows p, failing the test when the Host refuses.
func show(t *testing.T, h *host.Host, p *picture.Picture) *host.Showing {
	t.Helper()
	s, err := h.Show(p)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	return s
}

// assertLines compares protocol lines byte for byte (Raw Mode frames are not
// JSON).
func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\n got: %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d\n got: %q\nwant: %q", i, got[i], want[i])
		}
	}
}

func TestShowPutsThePictureOnTheClaimedDeviceInRawMode(t *testing.T) {
	h, dev, _ := claimedHost(t)

	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))

	assertLines(t, since(dev, 2), []string{
		fixture(t, "raw-mode-enter.jsonl"),
		fixture(t, "raw-frame.bin"),
	})
}

var _ = host.New

func TestStoppingTheShowReturnsTheDeviceToItsOwnRendering(t *testing.T) {
	h, dev, _ := claimedHost(t)
	showing := show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	n := len(dev.Received())

	showing.Stop()

	assertLines(t, since(dev, n), []string{fixture(t, "raw-mode-exit.jsonl")})
	select {
	case <-showing.Ended():
	default:
		t.Error("a stopped show has not ended")
	}
}

func TestADeviceClaimedDuringTheShowGetsItAfterTheSessions(t *testing.T) {
	clock := clockAt(1760432395000)
	dev := newReferenceDevice(t)
	plugged := false
	h := host.New(clock, devicelink.DiscovererFunc(func() []devicelink.Candidate {
		if !plugged {
			return nil
		}
		return devices(dev).Candidates()
	}))
	defer h.Close()
	apply(t, h, host.Event{SessionID: fixtureSession, State: host.AwaitingInput})
	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))

	clock.Advance(5 * time.Second)
	plugged = true
	h.Tick()

	assertLines(t, dev.Received()[3:], []string{
		fixture(t, "raw-mode-enter.jsonl"),
		fixture(t, "raw-frame.bin"),
	})
	assertMessages(t, dev.Received()[:3], []string{
		fixture(t, "hello.jsonl"),
		defaultPreferences,
		fixture(t, "session-upsert.jsonl"),
	})
}

// solidFrame is the Raw Mode frame of an 8x8 matrix all in c.
func solidFrame(c color.RGBA) string {
	return "\xff" + strings.Repeat(string([]byte{c.R, c.G, c.B}), 64)
}

var (
	red   = color.RGBA{0xff, 0, 0, 0xff}
	green = color.RGBA{0, 0xff, 0, 0xff}
	blue  = color.RGBA{0, 0, 0xff, 0xff}
)

// encodeGIF encodes an 8x8 animation: frame i covers rects[i] in colors[i]
// and shows for delays[i] hundredths of a second.
func encodeGIF(t *testing.T, rects []image.Rectangle, colors []color.RGBA, delays []int, disposal []byte) []byte {
	t.Helper()
	palette := color.Palette{color.RGBA{}, red, green, blue}
	g := &gif.GIF{Config: image.Config{Width: 8, Height: 8, ColorModel: palette}}
	for i, r := range rects {
		img := image.NewPaletted(r, palette)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.Set(x, y, colors[i])
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, delays[i])
		g.Disposal = append(g.Disposal, disposal[i])
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestAnAnimatedGIFLoopsByItsFrameDelays(t *testing.T) {
	h, dev, clock := claimedHost(t)
	whole := image.Rect(0, 0, 8, 8)
	show(t, h, decode(t, encodeGIF(t,
		[]image.Rectangle{whole, whole, whole},
		[]color.RGBA{red, green, blue},
		[]int{10, 20, 30},
		[]byte{gif.DisposalNone, gif.DisposalNone, gif.DisposalNone})))
	n := len(dev.Received())

	var frames []string
	for range 12 { // 1.2 s, ticking every 100 ms like the Host Daemon
		clock.Advance(100 * time.Millisecond)
		h.Tick()
		for _, line := range since(dev, n) {
			if strings.HasPrefix(line, "\xff") {
				frames = append(frames, line)
			}
		}
		n = len(dev.Received())
	}

	// Red showed from the start; green at 100 ms, blue at 300 ms, then red
	// again at 600 ms, and so on; the heartbeat at 1 s resends blue.
	assertLines(t, frames, []string{
		solidFrame(green), solidFrame(blue), solidFrame(red),
		solidFrame(green), solidFrame(blue), solidFrame(blue), solidFrame(red),
	})
}

// halves is the Raw Mode frame of an 8x8 matrix with its left half in left
// and its right half in right.
func halves(left, right color.RGBA) string {
	row := strings.Repeat(string([]byte{left.R, left.G, left.B}), 4) + strings.Repeat(string([]byte{right.R, right.G, right.B}), 4)
	return "\xff" + strings.Repeat(row, 8)
}

func TestAGIFFrameDrawsOverWhatThePreviousFrameLeft(t *testing.T) {
	whole, left, right := image.Rect(0, 0, 8, 8), image.Rect(0, 0, 4, 8), image.Rect(4, 0, 8, 8)
	black := color.RGBA{}
	for name, tc := range map[string]struct {
		disposal byte
		want     []string
	}{
		"kept":                 {gif.DisposalNone, []string{solidFrame(red), halves(green, red), halves(green, blue)}},
		"cleared to unlit":     {gif.DisposalBackground, []string{solidFrame(red), halves(green, red), halves(black, blue)}},
		"restored to previous": {gif.DisposalPrevious, []string{solidFrame(red), halves(green, red), halves(red, blue)}},
	} {
		t.Run(name, func(t *testing.T) {
			h, dev, clock := claimedHost(t)
			// The green left half is disposed of as the case says.
			show(t, h, decode(t, encodeGIF(t,
				[]image.Rectangle{whole, left, right},
				[]color.RGBA{red, green, blue},
				[]int{10, 10, 10},
				[]byte{gif.DisposalNone, tc.disposal, gif.DisposalNone})))
			for range 2 {
				clock.Advance(100 * time.Millisecond)
				h.Tick()
			}

			assertLines(t, since(dev, 3), tc.want)
		})
	}
}

// pixel is the color of LED (x, y) in a Raw Mode frame for a matrix width
// LEDs wide.
func pixel(frame string, width, x, y int) color.RGBA {
	i := 1 + (y*width+x)*3
	return color.RGBA{frame[i], frame[i+1], frame[i+2], 0xff}
}

func TestThePictureIsScaledToTheClaimedDevicesMatrix(t *testing.T) {
	clock := clockAt(1760432400000)
	dev := newFakeDevice(strings.Replace(fixture(t, "hello-reply.jsonl"), `"width":8,"height":8`, `"width":16,"height":16`, 1))
	h := host.New(clock, devices(dev))
	defer h.Close()
	h.Tick()

	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))

	frame := since(dev, 3)[0]
	if len(frame) != 1+16*16*3 {
		t.Fatalf("frame of %d bytes, want %d", len(frame), 1+16*16*3)
	}
	source := rawFixtureImage()
	for y := range 16 {
		for x := range 16 {
			if got, want := pixel(frame, 16, x, y), source.At(x/2, y/2); got != want {
				t.Errorf("LED (%d, %d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestALargerPictureIsAveragedAndFitsTheMatrixKeepingItsAspectRatio(t *testing.T) {
	h, dev, _ := claimedHost(t)
	// 16x8, in red and blue columns: half as tall as wide.
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := range 8 {
		for x := range 16 {
			img.Set(x, y, []color.RGBA{red, blue}[x%2])
		}
	}

	show(t, h, decode(t, encodePNG(t, img)))

	frame := since(dev, 3)[0]
	purple, unlit := color.RGBA{0x7f, 0, 0x7f, 0xff}, color.RGBA{0, 0, 0, 0xff}
	for y := range 8 {
		want := purple
		if y < 2 || y >= 6 {
			want = unlit // 8x4 centered on the 8x8 matrix
		}
		for x := range 8 {
			if got := pixel(frame, 8, x, y); got != want {
				t.Errorf("LED (%d, %d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestANewerShowReplacesTheOneOnTheDevice(t *testing.T) {
	h, dev, _ := claimedHost(t)
	whole := image.Rect(0, 0, 8, 8)
	still := func(c color.RGBA) *picture.Picture {
		return decode(t, encodeGIF(t, []image.Rectangle{whole}, []color.RGBA{c}, []int{0}, []byte{gif.DisposalNone}))
	}
	first := show(t, h, still(red))

	second := show(t, h, still(green))
	first.Stop()

	select {
	case <-first.Ended():
	default:
		t.Error("the replaced show has not ended")
	}
	assertLines(t, since(dev, 4), []string{
		fixture(t, "raw-mode-enter.jsonl"),
		solidFrame(green),
	})
	second.Stop()
	assertLines(t, since(dev, 6), []string{fixture(t, "raw-mode-exit.jsonl")})
}

func TestAShowKeepsTheHostDaemonUpAndTheExitGraceStartsWhenItStops(t *testing.T) {
	const grace = time.Minute
	clock := clockAt(1760432400000)
	h := host.New(clock, noDevices, host.WithExitGrace(grace))
	defer h.Close()

	showing := show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	clock.Advance(10 * grace)
	h.Tick()
	if retired(h) {
		t.Fatal("retired during a show")
	}
	showing.Stop()
	clock.Advance(grace - time.Millisecond)
	h.Tick()
	if retired(h) {
		t.Fatal("retired before the exit grace after the show")
	}
	clock.Advance(time.Millisecond)
	h.Tick()

	if !retired(h) {
		t.Error("not retired after the exit grace with no show and no sessions")
	}
}

func TestNoPixelByteIsANewlineSoAFrameIsOneLine(t *testing.T) {
	h, dev, _ := claimedHost(t)
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = map[bool]byte{true: 0xff, false: '\n'}[i%4 == 3] // gray 10, opaque
	}

	show(t, h, decode(t, encodePNG(t, img)))

	assertLines(t, since(dev, 3), []string{solidFrame(color.RGBA{11, 11, 11, 0xff})})
}

func TestShowRejectsWhatIsNotAnImage(t *testing.T) {
	for name, data := range map[string][]byte{
		"text":      []byte("hello"),
		"truncated": encodePNG(t, rawFixtureImage())[:40],
		"huge":      encodePNG(t, image.NewGray(image.Rect(0, 0, 5000, 5000))),
	} {
		if _, err := picture.Decode(data); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestTheShowComesBackAfterTheHostWentSilentForTheDisconnectedTimeout(t *testing.T) {
	h, dev, clock := claimedHost(t)
	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	n := len(dev.Received())

	// A laptop asleep: no Tick, so the Device showed Disconnected and left
	// Raw Mode.
	clock.Advance(preferences.Defaults().DisconnectedTimeout)
	h.Tick()

	assertLines(t, since(dev, n+1), []string{
		fixture(t, "raw-mode-enter.jsonl"),
		fixture(t, "raw-frame.bin"),
	})
}

func TestEveryHeartbeatResendsTheFrameShown(t *testing.T) {
	h, dev, clock := claimedHost(t)
	show(t, h, decode(t, encodePNG(t, rawFixtureImage())))
	n := len(dev.Received())

	clock.Advance(host.HeartbeatInterval)
	h.Tick()

	// A frame lost or garbled on the way is good again a second later.
	assertLines(t, since(dev, n+1), []string{fixture(t, "raw-frame.bin")})
}
