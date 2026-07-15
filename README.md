# amberFOCUS Setup

A standalone calibration tool that creates **localisation-correction (LoCo) FIR
filters** for the amberSUITE audio pipeline (and any pipeline that can load an
`F64_LE` `.dbl` filter for Mid/Side processing).

You calibrate the stereo width per frequency until a moving test tone tracks a
reference tone; the tool then computes a FIR filter and saves it as a `.dbl`
file. You import that file into amberDSP (or another host) which applies it.

## Status

Cross-platform web app: run the binary, open `http://localhost:PORT` in your
browser, pick your UPnP renderer, calibrate, export the filter.

## License

**GPL-3.0-or-later.** This program is free software.

This tool is a derivative work of **frankl's stereo utilities** by
**Frank Brenner** ("frankl") — specifically a Go port of `locotest.c` and it
ships frankl's calibration tone data. Original:
<http://frankl.luebecknet.de/stereoutils/> ·
<https://github.com/frankl-audio/frankl_stereo> (GPL-3.0-or-later).

Because it derives from GPL code, amberFOCUS Setup is itself GPL — which is why
it lives in its own public repository, separate from the proprietary amberSUITE.
The suite consumes only the resulting `.dbl` filter files (data), not this code.

See `LICENSE` for the full GNU General Public License v3.
