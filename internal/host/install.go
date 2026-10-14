package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/codino-labs/codino/internal/devicelink"
	"github.com/codino-labs/codino/internal/protocol"
)

// ErrNoDevice means no Device is claimed to install on.
var ErrNoDevice = errors.New("no Device connected")

// errDeviceGone means the Device went away before it replied.
var errDeviceGone = errors.New("the Device went away")

// Install installs a Theme or Sprite Pack on the claimed Device, which keeps
// it in its flash, and returns the Device's reply: whether it took it, why
// not, and the Themes and Sprite Packs it has now. It waits for the reply
// until ctx is done.
func (h *Host) Install(ctx context.Context, install protocol.Install) (protocol.InstallReply, error) {
	return h.askDevice(ctx, install, install.Kind, install.Name)
}

// Uninstall removes an installed Theme or Sprite Pack from the claimed
// Device, as Install.
func (h *Host) Uninstall(ctx context.Context, kind protocol.Kind, name string) (protocol.InstallReply, error) {
	return h.askDevice(ctx, protocol.NewUninstall(kind, name), kind, name)
}

// askDevice sends an install or uninstall of kind and name, and waits for
// the Device's reply to it without h.mu: a Device writing its flash takes a
// while. A late reply to an earlier request, which its caller stopped
// waiting for, still tells what the Device has, but isn't the answer.
func (h *Host) askDevice(ctx context.Context, msg any, kind protocol.Kind, name string) (protocol.InstallReply, error) {
	h.installMu.Lock()
	defer h.installMu.Unlock()
	h.mu.Lock()
	dev := h.device
	if dev == nil {
		h.unlock()
		return protocol.InstallReply{}, ErrNoDevice
	}
	h.send(msg)
	sent := h.device == dev
	h.unlock()
	if !sent {
		return protocol.InstallReply{}, errDeviceGone
	}
	for {
		select {
		case reply := <-dev.InstallReplies():
			h.mu.Lock()
			if h.device == dev {
				h.onDevice = installedOn{themes: reply.Themes, spritePacks: reply.SpritePacks}
			}
			h.unlock()
			if reply.Kind == kind && reply.Name == name {
				if reply.OK {
					h.keepCopy(msg, kind, name)
				}
				return reply, nil
			}
		case <-dev.Gone():
			return protocol.InstallReply{}, errDeviceGone
		case <-ctx.Done():
			return protocol.InstallReply{}, fmt.Errorf("no reply from the Device: %w", ctx.Err())
		}
	}
}

// installedOn is what the claimed Device has installed, as it last said:
// its Themes and Sprite Packs, built-in ones first.
type installedOn struct {
	themes, spritePacks []string
}

func installedOnDevice(dev *devicelink.Device) installedOn {
	return installedOn{themes: dev.HelloReply.Themes, spritePacks: dev.HelloReply.SpritePacks}
}

// keepCopy keeps a copy of what the Device just installed, or forgets the
// copy of what it uninstalled, for the web UI's preview. The I/O runs
// without h.mu.
func (h *Host) keepCopy(msg any, kind protocol.Kind, name string) {
	if h.copies == nil {
		return
	}
	install, installed := msg.(protocol.Install)
	var err error
	if installed {
		err = h.copies.Save(install)
	} else {
		err = h.copies.Remove(kind, name)
	}
	if err != nil {
		slog.Warn("keep a copy of a Theme or Sprite Pack for the preview", "kind", kind, "name", name, "err", err)
	}
	h.mu.Lock()
	defer h.unlock()
	h.themeCopies = slices.DeleteFunc(h.themeCopies, func(i protocol.Install) bool { return i.Kind == kind && i.Name == name })
	if installed {
		h.themeCopies = append(h.themeCopies, install)
	}
}
