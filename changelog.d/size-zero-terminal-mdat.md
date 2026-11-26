### Added

- `DecodeFile`, `DecodeFileSR`, `DecodeBox`, `DecodeBoxSR` and lazy `MdatBox` decoding read a final
  top-level `mdat` with size 0 as extending to EOF. `DecodeHeaderSeek` resolves such a size with an
  `io.ReadSeeker`, while nested or non-`mdat` size-0 boxes still give an error.
