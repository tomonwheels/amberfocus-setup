// Package calib is the LoCo calibration tone engine.
//
// Port of frankl's locotest.c (Frank Brenner, GPL-3.0-or-later), decoupled from
// amberSUITE so it runs standalone. Generates the moving reference tone plus the
// per-frequency test tones with a live-adjustable Mid/Side width multiplier, and
// streams them as a WAV to the UPnP renderer.
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
}

// New returns an Engine initialised like frankl's locotest defaults.
func New() *Engine {
	e := &Engine{nrtones: 100, tonelen: 35280, rate: 44100, maxm: 9.0, vol: 0.4}
	for i := range e.mults {
		e.mults[i] = 1.0
	}
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

// Start loads the tone bank (once) and begins the stream loop.
func (e *Engine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return
	}
	e.load()
	e.running = true
	e.stopCh = make(chan struct{})
}

// Stop ends the stream loop.
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

// Stream serves the live test-tone WAV (44100 Hz, stereo, S32_LE).
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

	delta := math.Pow(1.1220184543019633, 1.0/float64(e.tonelen))
	blen := 1024
	outbuf := make([]byte, blen*2*4)
	var pos int
	m := e.maxm
	f := delta

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		e.mu.Lock()
		vol := e.vol
		var activeList [100]bool
		var multList [100]float64
		copy(activeList[:], e.active[:])
		copy(multList[:], e.mults[:])
		e.mu.Unlock()

		for i := 0; i < blen; i++ {
			if pos >= e.tonelen {
				pos = 0
			}
			m *= f
			if e.maxm*m < 1 {
				m = 1.0 / e.maxm
				f = 1 / delta
			}
			if m > e.maxm {
				m = e.maxm
				f = delta
			}
			a := m / (1 + m)
			b := 1 - a

			var left, right float32
			for j := 0; j < e.nrtones; j++ {
				if !activeList[j] {
					continue
				}
				c := (1.0 + multList[j]) / 2.0
				d := (1.0 - multList[j]) / 2.0
				sample := e.tones[j*e.tonelen+pos]
				left += float32(c*a+d*b) * sample
				right += float32(d*a+c*b) * sample
			}
			left *= float32(vol)
			right *= float32(vol)

			sl := int32(float64(left) * 2147483647.0)
			sr := int32(float64(right) * 2147483647.0)
			binary.LittleEndian.PutUint32(outbuf[i*8:], uint32(sl))
			binary.LittleEndian.PutUint32(outbuf[i*8+4:], uint32(sr))
			pos++
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
