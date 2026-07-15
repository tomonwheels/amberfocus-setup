// Package localout plays the calibration stream on the local audio device
// (the machine's default output — typically a USB DAC or built-in output),
// for users without a UPnP renderer.
//
// Uses ebitengine/oto (MIT, purego — no cgo). oto is a permissively-licensed
// dependency, compatible with this GPL tool.
package localout

import (
	"io"
	"sync"

	"github.com/ebitengine/oto/v3"
)

// Player wraps a single oto context + player. The oto context is created once
// (creating multiple contexts is not supported) and reused across sessions.
type Player struct {
	mu     sync.Mutex
	ctx    *oto.Context
	player *oto.Player
}

func (p *Player) ensureCtx(rate int) error {
	if p.ctx != nil {
		return nil
	}
	opts := &oto.NewContextOptions{
		SampleRate:   rate,
		ChannelCount: 2,
		Format:       oto.FormatFloat32LE,
	}
	ctx, ready, err := oto.NewContext(opts)
	if err != nil {
		return err
	}
	<-ready
	p.ctx = ctx
	return nil
}

// Start (re)starts playback of src on the default output device. src must emit
// interleaved float32 LE stereo at the given rate.
func (p *Player) Start(src io.Reader, rate int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureCtx(rate); err != nil {
		return err
	}
	if p.player != nil {
		_ = p.player.Close()
		p.player = nil
	}
	pl := p.ctx.NewPlayer(src)
	pl.Play()
	p.player = pl
	return nil
}

// Stop stops playback (the context stays alive for reuse).
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.player != nil {
		_ = p.player.Close()
		p.player = nil
	}
}
