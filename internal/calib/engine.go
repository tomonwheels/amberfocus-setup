// Package calib is the LoCo calibration tone engine.
//
// Port of frankl's locotest.c (by frankl, GPL-3.0-or-later), decoupled from
// amberSUITE so it runs standalone. Generates the moving reference tone plus the
// per-frequency test tones with a live-adjustable Mid/Side width multiplier.
//
// The same sample generator (genStereo) feeds either the WAV HTTP stream (for a
// UPnP renderer) or the local audio device (Read implements io.Reader for oto).
package calib

import (
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"sync"

	"github.com/tomonwheels/amberfocus-setup/internal/data"
)

// Engine holds calibration state and renders the test-tone stream.
type Engine struct {
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	tones   []float32
	nrtones int
	tonelen int
	rate    int
	maxm    float64
	active  [100]bool
	mults   [100]float64
	vol     float64

	// panning-envelope state (shared by the single active output)
	delta float64
	pos   int
	envM  float64
	envF  float64
}

// New returns an Engine initialised like frankl's locotest defaults.
func New() *Engine {
	e := &Engine{nrtones: 100, tonelen: 35280, rate: 44100, maxm: 9.0, vol: 0.4}
	for i := range e.mults {
		e.mults[i] = 1.0
	}
	e.delta = math.Pow(1.1220184543019633, 1.0/float64(e.tonelen))
	return e
}

func (e *Engine) load() {
	if e.tones != nil {
		return
	}
	raw := data.LocotestRaw
	n := len(raw) / 4 // float32
	e.tones = make([]float32, n)
	for i := 0; i < n; i++ {
		e.tones[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
}

// Start loads the tone bank (once), resets the envelope and begins output.
func (e *Engine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return
	}
	e.load()
	e.pos = 0
	e.envM = e.maxm
	e.envF = e.delta
	e.running = true
	e.stopCh = make(chan struct{})
}

// Stop ends output.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return
	}
	e.running = false
	close(e.stopCh)
}

// Running reports whether the engine is active.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Rate is the stream sample rate (Hz).
func (e *Engine) Rate() int { return e.rate }

// SetTone enables/disables an individual tone by index.
func (e *Engine) SetTone(i int, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i >= 0 && i < e.nrtones {
		e.active[i] = on
	}
}

// SetMult sets the Side-channel width multiplier for a tone.
func (e *Engine) SetMult(i int, m float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i >= 0 && i < e.nrtones {
		e.mults[i] = m
	}
}

// SetVol sets the overall level.
func (e *Engine) SetVol(v float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vol = v
}

// Status returns the current mults/active/vol/running state.
func (e *Engine) Status() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return map[string]any{
		"running": e.running,
		"mults":   e.mults[:],
		"active":  e.active[:],
		"vol":     e.vol,
	}
}

// genStereo fills out (interleaved L,R float32) with the next block, advancing
// the panning envelope. Produces silence when the engine is not running.
func (e *Engine) genStereo(out []float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	nframes := len(out) / 2
	if !e.running {
		for i := range out {
			out[i] = 0
		}
		return
	}
	vol := float32(e.vol)
	for i := 0; i < nframes; i++ {
		if e.pos >= e.tonelen {
			e.pos = 0
		}
		e.envM *= e.envF
		if e.maxm*e.envM < 1 {
			e.envM = 1.0 / e.maxm
			e.envF = 1 / e.delta
		}
		if e.envM > e.maxm {
			e.envM = e.maxm
			e.envF = e.delta
		}
		a := e.envM / (1 + e.envM)
		b := 1 - a
		var left, right float32
		for j := 0; j < e.nrtones; j++ {
			if !e.active[j] {
				continue
			}
			c := (1.0 + e.mults[j]) / 2.0
			d := (1.0 - e.mults[j]) / 2.0
			sample := e.tones[j*e.tonelen+e.pos]
			left += float32(c*a+d*b) * sample
			right += float32(d*a+c*b) * sample
		}
		out[2*i] = left * vol
		out[2*i+1] = right * vol
		e.pos++
	}
}

// Read implements io.Reader, emitting interleaved float32 LE stereo — for the
// local audio device (oto). Always returns data (silence when not running).
func (e *Engine) Read(p []byte) (int, error) {
	nframes := len(p) / 8 // stereo float32 = 8 bytes/frame
	if nframes == 0 {
		return 0, nil
	}
	buf := make([]float32, nframes*2)
	e.genStereo(buf)
	for i, s := range buf {
		binary.LittleEndian.PutUint32(p[i*4:], math.Float32bits(s))
	}
	return nframes * 8, nil
}

// Stream serves the live test-tone WAV (44100 Hz, stereo, S32_LE) for a UPnP
// renderer.
func (e *Engine) Stream(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		http.Error(w, "engine not running", http.StatusServiceUnavailable)
		return
	}
	stopCh := e.stopCh
	e.mu.Unlock()

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", "2147483647")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	writeWAVHeader(w, e.rate)
	flusher.Flush()

	const blen = 1024
	fbuf := make([]float32, blen*2)
	outbuf := make([]byte, blen*2*4)
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		e.genStereo(fbuf)
		for i, s := range fbuf {
			v := int32(float64(s) * 2147483647.0)
			binary.LittleEndian.PutUint32(outbuf[i*4:], uint32(v))
		}
		if _, err := w.Write(outbuf); err != nil {
			return
		}
		flusher.Flush()
	}
}

func writeWAVHeader(w io.Writer, sampleRate int) {
	le := binary.LittleEndian
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	le.PutUint32(hdr[4:8], 0x7FFFFFFF)
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	le.PutUint32(hdr[16:20], 16)
	le.PutUint16(hdr[20:22], 1) // PCM
	le.PutUint16(hdr[22:24], 2) // stereo
	le.PutUint32(hdr[24:28], uint32(sampleRate))
	le.PutUint32(hdr[28:32], uint32(sampleRate*2*4))
	le.PutUint16(hdr[32:34], 8)  // blockAlign
	le.PutUint16(hdr[34:36], 32) // bits
	copy(hdr[36:40], "data")
	le.PutUint32(hdr[40:44], 0x7FFFFFFF)
	_, _ = w.Write(hdr)
}
