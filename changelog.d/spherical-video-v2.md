### Added

- `Stereo3DBox` (st3d), `SphericalVideoBox` (sv3d) with svhd, proj, prhd and
  `EquirectangularProjectionBox` (equi) decode and encode the Spherical Video V2
  metadata of a `VisualSampleEntryBox`, which also gains Stereo3D, SphericalVideo,
  ProjectionHeader and EquirectangularProjection query methods. Unknown child
  boxes, including cbmp and mshp projections, round-trip without decoding.
