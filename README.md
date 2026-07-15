# amberFOCUS Setup

A standalone calibration tool that creates **localisation-correction (LoCo) FIR
filters** for the amberSUITE audio pipeline (and any pipeline that can load an
`F64_LE` `.dbl` filter for Mid/Side processing).

You calibrate the stereo width per frequency until a moving test tone tracks a
reference tone; the tool then computes a FIR filter and saves it as a `.dbl`
file. You import that file into amberDSP (or another host) which applies it.

## Run it

It's a small cross-platform web app. Build and run from source:

```
go build -o amberfocus-setup ./cmd/amberfocus-setup
./amberfocus-setup            # opens http://localhost:8099 in your browser
```

Flags: `--port` (default 8099), `--out` (output directory for the `.dbl` files,
default current dir), `--no-browser`.

In the UI, pick an **output**:

- **Network renderer (UPnP)** — streams the test tones to a discovered renderer.
- **This computer (USB DAC / local)** — plays on the machine's default audio
  device; set your USB DAC as the system default output.

Then calibrate the stereo width per frequency until each test tone tracks the
moving reference, and export. The resulting `FHR-<rate>.dbl` filters can be
uploaded into amberDSP (amberFOCUS filter section).

Prebuilt binaries: see **Releases** (once published).

## License

**GPL-3.0-or-later.** This program is free software.

This tool is a derivative work of **frankl's stereo utilities** by **frankl**
(active on aktives-hoeren.de) — specifically a Go port of `locotest.c` and it
ships frankl's calibration tone data. Original:
<http://frankl.luebecknet.de/stereoutils/> ·
<https://github.com/frankl-audio/frankl_stereo> (GPL-3.0-or-later).

Because it derives from GPL code, amberFOCUS Setup is itself GPL — which is why
it lives in its own public repository, separate from the proprietary amberSUITE.
The suite consumes only the resulting `.dbl` filter files (data), not this code.

See `LICENSE` for the full GNU General Public License v3.
