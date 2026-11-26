package mp4

import (
	"fmt"
	"io"

	"github.com/Eyevinn/mp4ff/bits"
)

// Field sizes of the compact sample size table of an Stz2Box, in bits per entry.
const (
	Stz2FieldSize4  = 4
	Stz2FieldSize8  = 8
	Stz2FieldSize16 = 16
)

// Stz2Box - Compact Sample Size Box (stz2)
//
// Contained in : Sample Table box (stbl)
//
// Defined in ISO/IEC 14496-12 Ed.8, 8.7.23. It is the compact variant of StszBox:
// every sample has an individual size stored in 4, 8 or 16 bits. A track carries
// either an stsz or an stz2 box.
//
// With a 4-bit field size, two sizes share each byte, the first sample in the
// high-order nibble. An odd sample count leaves a padding nibble in the last byte
// which is not a sample.
type Stz2Box struct {
	Version      byte
	Flags        uint32
	FieldSize    uint8
	SampleNumber uint32
	SampleSize   []uint16
}

// DecodeStz2 - box-specific decode
func DecodeStz2(hdr BoxHeader, startPos uint64, r io.Reader) (Box, error) {
	data, err := readBoxBody(r, hdr)
	if err != nil {
		return nil, err
	}
	sr := bits.NewFixedSliceReader(data)
	return DecodeStz2SR(hdr, startPos, sr)
}

// DecodeStz2SR - box-specific decode
func DecodeStz2SR(hdr BoxHeader, startPos uint64, sr bits.SliceReader) (Box, error) {
	versionAndFlags := sr.ReadUint32()
	// Three reserved bytes followed by the one-byte field size.
	sr.ReadUint24()
	fieldSize := sr.ReadUint8()
	sampleNumber := sr.ReadUint32()

	b := Stz2Box{
		Version:      byte(versionAndFlags >> 24),
		Flags:        versionAndFlags & flagsMask,
		FieldSize:    fieldSize,
		SampleNumber: sampleNumber,
	}

	if fieldSize != Stz2FieldSize4 && fieldSize != Stz2FieldSize8 &&
		fieldSize != Stz2FieldSize16 {
		return nil, fmt.Errorf("stz2: unsupported field size %d, only 4, 8 and 16 are valid", fieldSize)
	}
	if hdr.Size != b.expectedSize() {
		return nil, fmt.Errorf("stz2: expected size %d, got %d", b.expectedSize(), hdr.Size)
	}
	if sampleNumber == 0 {
		if err := sr.AccError(); err != nil {
			return nil, err
		}
		return &b, nil
	}

	b.SampleSize = make([]uint16, sampleNumber)
	switch fieldSize {
	case Stz2FieldSize4:
		nrBytes := (int(sampleNumber) + 1) / 2
		data := sr.ReadBytes(nrBytes)
		if err := sr.AccError(); err != nil {
			return nil, err
		}
		for i := range b.SampleSize {
			if i%2 == 0 {
				b.SampleSize[i] = uint16(data[i/2] >> 4)
			} else {
				b.SampleSize[i] = uint16(data[i/2] & 0x0f)
			}
		}
	case Stz2FieldSize8:
		data := sr.ReadBytes(int(sampleNumber))
		if err := sr.AccError(); err != nil {
			return nil, err
		}
		for i := range b.SampleSize {
			b.SampleSize[i] = uint16(data[i])
		}
	case Stz2FieldSize16:
		data := sr.ReadBytes(2 * int(sampleNumber))
		if err := sr.AccError(); err != nil {
			return nil, err
		}
		for i := range b.SampleSize {
			b.SampleSize[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
		}
	}
	return &b, sr.AccError()
}

// Type - box-specific type
func (b *Stz2Box) Type() string {
	return "stz2"
}

// Size - box-specific size
func (b *Stz2Box) Size() uint64 {
	return b.expectedSize()
}

// expectedSize - calculate size from the field size and number of samples.
// Beyond the box header (8), the fixed body holds version and flags (4), the
// three reserved bytes plus field size (4) and the sample count (4), i.e. 12
// bytes. The packed entry table follows.
func (b *Stz2Box) expectedSize() uint64 {
	nrTableBytes := uint64(0)
	switch b.FieldSize {
	case Stz2FieldSize4:
		nrTableBytes = (uint64(b.SampleNumber) + 1) / 2
	case Stz2FieldSize8:
		nrTableBytes = uint64(b.SampleNumber)
	case Stz2FieldSize16:
		nrTableBytes = 2 * uint64(b.SampleNumber)
	}
	return uint64(boxHeaderSize) + 12 + nrTableBytes
}

// Encode - write box to w
func (b *Stz2Box) Encode(w io.Writer) error {
	sw := bits.NewFixedSliceWriter(int(b.Size()))
	err := b.EncodeSW(sw)
	if err != nil {
		return err
	}
	_, err = w.Write(sw.Bytes())
	return err
}

// EncodeSW - box-specific encode to slicewriter
func (b *Stz2Box) EncodeSW(sw bits.SliceWriter) error {
	if b.FieldSize != Stz2FieldSize4 && b.FieldSize != Stz2FieldSize8 &&
		b.FieldSize != Stz2FieldSize16 {
		return fmt.Errorf("stz2: unsupported field size %d, only 4, 8 and 16 are valid", b.FieldSize)
	}
	if err := b.validateSizes(); err != nil {
		return err
	}
	if err := EncodeHeaderSW(b, sw); err != nil {
		return err
	}
	versionAndFlags := (uint32(b.Version) << 24) + b.Flags
	sw.WriteUint32(versionAndFlags)
	sw.WriteUint24(0) // Reserved
	sw.WriteUint8(b.FieldSize)
	sw.WriteUint32(b.SampleNumber)
	switch b.FieldSize {
	case Stz2FieldSize4:
		nrBytes := (len(b.SampleSize) + 1) / 2
		for i := 0; i < nrBytes; i++ {
			var packed byte
			packed = byte(b.SampleSize[2*i]&0x0f) << 4
			if 2*i+1 < len(b.SampleSize) {
				packed |= byte(b.SampleSize[2*i+1] & 0x0f)
			}
			sw.WriteUint8(packed)
		}
	case Stz2FieldSize8:
		for _, size := range b.SampleSize {
			sw.WriteUint8(byte(size))
		}
	case Stz2FieldSize16:
		for _, size := range b.SampleSize {
			sw.WriteUint16(size)
		}
	}
	return sw.AccError()
}

// Info - write box-specific information
func (b *Stz2Box) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	bd := newInfoDumper(w, indent, b, int(b.Version), b.Flags)
	bd.write(" - fieldSize: %d", b.FieldSize)
	bd.write(" - sampleCount: %d", b.SampleNumber)
	level := getInfoLevel(b, specificBoxLevels)
	if level >= 1 {
		for i := range b.SampleSize {
			bd.write(" - sample[%d] size=%d", i+1, b.SampleSize[i])
		}
	}
	return bd.err
}

// GetNrSamples - get number of samples
func (b *Stz2Box) GetNrSamples() uint32 {
	return b.SampleNumber
}

// GetSampleSize returns the size (in bytes) of a sample. The sample number i is
// one-based. It returns 0 for a sample number outside the table.
func (b *Stz2Box) GetSampleSize(i int) uint32 {
	if i < 1 || i > len(b.SampleSize) { // One-based
		return 0
	}
	return uint32(b.SampleSize[i-1])
}

// GetTotalSampleSize - get total size of a range [startNr, endNr] of samples
func (b *Stz2Box) GetTotalSampleSize(startNr, endNr uint32) (uint64, error) {
	if startNr <= 0 || endNr > b.GetNrSamples() {
		return 0, fmt.Errorf("startNr or calculated endNr outside range 1-%d", b.GetNrSamples())
	}
	if endNr < startNr {
		return 0, nil
	}
	size := uint64(0)
	for _, s := range b.SampleSize[startNr-1 : endNr] { // 1-based numbers
		size += uint64(s)
	}
	return size, nil
}

// maxStz2SampleSize returns the biggest value that one entry of fieldSize can hold.
func maxStz2SampleSize(fieldSize uint8) uint16 {
	return uint16(1)<<fieldSize - 1
}

// validateSizes checks that the individual sizes fit their field size and that the
// table length matches the declared sample count.
func (b *Stz2Box) validateSizes() error {
	if uint32(len(b.SampleSize)) != b.SampleNumber {
		return fmt.Errorf("stz2: sample count %d does not match %d listed sizes",
			b.SampleNumber, len(b.SampleSize))
	}
	maxSize := maxStz2SampleSize(b.FieldSize)
	for i, size := range b.SampleSize {
		if size > maxSize {
			return fmt.Errorf("stz2: sample %d size %d does not fit in %d bits (max %d)",
				i+1, size, b.FieldSize, maxSize)
		}
	}
	return nil
}
