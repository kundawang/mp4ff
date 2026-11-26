### Added

- Decode and encode the compact sample size box `Stz2Box` (stz2) with 4, 8 and 16-bit entries, and read its samples
  through the existing track sample APIs; `StblBox.Stz2` and `StblBox.SampleSize` access it alongside stsz
- `StblBox.CheckSampleCounts` verifies that the stsz/stz2 sample count agrees with the stts and stsc/stco tables
