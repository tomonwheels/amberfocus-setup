// Package data embeds frankl's calibration tone data.
//
// locotestdata.raw and freq originate from frankl's stereo utilities
// (by frankl, GPL-3.0-or-later). See ../../README.md and ../../LICENSE.
package data

import _ "embed"

// LocotestRaw is frankl's calibration tone bank: 100 tones of 35280 mono
// float32 (little-endian) samples each.
//
//go:embed locotestdata.raw
var LocotestRaw []byte

// Freq is the newline-separated list of the 100 calibration frequencies (Hz).
//
//go:embed freq
var Freq string
