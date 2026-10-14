package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/protocol"
)

// ErrNoDevice means no Device is claimed to install on.
var ErrNoDevice = errors.New("no Device connected")

// Install installs a Theme or Sprite Pack on the claimed Device, which keeps
// it in its flash, and returns the Device's reply: whether it took it, why
// not, and the Themes and Sprite Packs it has now. It waits for the reply
// until ctx is done.
func (h *Host) Install(ctx context.Context, install protocol.Install) (protocol.InstallReply, error) {
	return h.askDevice(ctx, install)
}

// Uninstall removes an installed Theme or Sprite Pack from the claimed
// Device, as Install.
func (h *Host) Uninstall(ctx context.Context, kind protocol.Kind, name string) (protocol.InstallReply, error) {
	return h.askDevice(ctx, protocol.NewUninstall(kind, name))
}

// askDevice sends an install or uninstall and waits for the Device's reply,
// without h.mu: a Device writing its flash takes a while.
func (h *Host) askDevice(ctx context.Context, msg any) (protocol.InstallReply, error) {
	h.installMu.Lock()
	defer h.installMu.Unlock()
	h.mu.Lock()
	dev := h.device
	if dev == nil {
		h.unlock()
		return protocol.InstallReply{}, ErrNoDevice
	}
	select {
	case <-dev.InstallReplies(): // a late reply to an earlier request
	default:
	}
	h.send(msg)
	sent := h.device == dev
	h.unlock()
	if !sent {
		return protocol.InstallReply{}, errors.New("the Device went away")
	}
	select {
	case reply := <-dev.InstallReplies():
		h.mu.Lock()
		if h.device == dev {
			h.deviceHas = has{themes: reply.Themes, spritePacks: reply.SpritePacks}
		}
		h.unlock()
		return reply, nil
	case <-dev.Gone():
		return protocol.InstallReply{}, errors.New("the Device went away")
	case <-ctx.Done():
		return protocol.InstallReply{}, fmt.Errorf("no reply from the Device: %w", ctx.Err())
	}
}

// has is what the claimed Device has installed, as it last said.
type has struct {
	themes, spritePacks []string
}

func hasOf(dev *devicelink.Device) has {
	return has{themes: dev.HelloReply.Themes, spritePacks: dev.HelloReply.SpritePacks}
}
