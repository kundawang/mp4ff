package mp4_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

func TestStz2EncDec(t *testing.T) {
	cases := []struct {
		name      string
		fieldSize uint8
		sizes     []uint16
	}{
		{"4-bit empty", mp4.Stz2FieldSize4, []uint16(nil)},
		{"4-bit odd count keeps padding nibble", mp4.Stz2FieldSize4, []uint16{1, 2, 15}},
		{"4-bit even count", mp4.Stz2FieldSize4, []uint16{0, 15, 7, 8}},
		{"8-bit", mp4.Stz2FieldSize8, []uint16{1, 2, 15, 255}},
		{"16-bit", mp4.Stz2FieldSize16, []uint16{1, 2, 15, 65535}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stz2 := mp4.Stz2Box{
				FieldSize:    tc.fieldSize,
				SampleNumber: uint32(len(tc.sizes)),
				SampleSize:   tc.sizes,
			}
			boxDiffAfterEncodeAndDecode(t, &stz2)
		})
	}
}

// TestStz2FourBitPacking checks the high-nibble-first byte layout and that the
// padding nibble after an odd number of samples is never read as a sample.
func TestStz2FourBitPacking(t *testing.T) {
	// Box with three sizes 1, 2, 15 packs into two bytes 0x12 0xf0.
	raw := mustHex(t, "0000001673747a3200000000000000040000000312f0")
	box, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(raw))
	if err != nil {
		t.Fatalf("decode stz2: %v", err)
	}
	stz2 := box.(*mp4.Stz2Box)
	if stz2.FieldSize != mp4.Stz2FieldSize4 || stz2.SampleNumber != 3 {
		t.Fatalf("got fieldSize=%d sampleNumber=%d", stz2.FieldSize, stz2.SampleNumber)
	}
	want := []uint16{1, 2, 15}
	for i, size := range want {
		if got := stz2.GetSampleSize(i + 1); got != uint32(size) {
			t.Errorf("sample %d: got size %d, want %d", i+1, got, size)
		}
	}
	// The padding nibble does not become a fourth sample.
	if got := stz2.GetNrSamples(); got != 3 {
		t.Errorf("got %d samples, want 3", got)
	}
	if got := stz2.GetSampleSize(4); got != 0 {
		t.Errorf("padding nibble read as sample: got size %d, want 0", got)
	}
	var buf bytes.Buffer
	if err := stz2.Encode(&buf); err != nil {
		t.Fatalf("encode stz2: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), raw) {
		t.Errorf("rewritten stz2 = %x, want %x", buf.Bytes(), raw)
	}
}

func TestStz2GetTotalSampleSize(t *testing.T) {
	stz2 := mp4.Stz2Box{
		FieldSize:    mp4.Stz2FieldSize8,
		SampleNumber: 3,
		SampleSize:   []uint16{1, 2, 15},
	}
	total, err := stz2.GetTotalSampleSize(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 18 {
		t.Errorf("total size %d, want 18", total)
	}
	middle, err := stz2.GetTotalSampleSize(2, 2)
	if err != nil || middle != 2 {
		t.Errorf("single sample size %d, err %v; want 2, nil", middle, err)
	}
	if _, err := stz2.GetTotalSampleSize(1, 4); err == nil {
		t.Error("expected error for endNr beyond the table, got nil")
	}
}

// TestStz2DecodeErrors covers empty-table acceptance plus truncated, illegal and
// inconsistent encodings on both decode paths.
func TestStz2DecodeErrors(t *testing.T) {
	base := mustHex(t, "0000001673747a3200000000000000040000000312f0")

	t.Run("empty table decodes", func(t *testing.T) {
		// Header (8) + version/flags (4) + reserved/fieldSize (4) + count (4), no entries.
		empty := mustHex(t, "0000001473747a32000000000000000400000000")
		box, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(empty))
		if err != nil {
			t.Fatalf("empty stz2: %v", err)
		}
		if got := box.(*mp4.Stz2Box).GetNrSamples(); got != 0 {
			t.Errorf("got %d samples, want 0", got)
		}
	})

	t.Run("illegal field size", func(t *testing.T) {
		// Layout: size(4) type(4) version/flags(4) reserved(3) fieldSize(1) count(4) entries.
		raw := bytes.Clone(base)
		raw[15] = 12 // field size byte sits after the three reserved bytes
		assertStz2DecodeError(t, raw)
	})

	t.Run("truncated entry table", func(t *testing.T) {
		raw := bytes.Clone(base)
		// Shrink the declared box size by one byte while the entries stay.
		raw[2] -= 1
		assertStz2DecodeError(t, raw)
	})

	t.Run("declared size larger than body", func(t *testing.T) {
		raw := bytes.Clone(base)
		raw[2] += 1
		assertStz2DecodeError(t, raw)
	})

	t.Run("sample count beyond packed bytes", func(t *testing.T) {
		// A fresh box declaring four 4-bit samples (two entry bytes) but with the
		// last entry byte cut off, so the table cannot be read in full.
		raw := mustHex(t, "0000001673747a3200000000000000040000000412")
		assertStz2DecodeError(t, raw)
	})
}

func assertStz2DecodeError(t *testing.T, raw []byte) {
	t.Helper()
	if _, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(raw)); err == nil {
		t.Error("DecodeBoxSR: expected error, got nil")
	}
	if _, err := mp4.DecodeBox(0, bytes.NewReader(raw)); err == nil {
		t.Error("DecodeBox: expected error, got nil")
	}
}

func TestStz2EncodeErrors(t *testing.T) {
	t.Run("illegal field size", func(t *testing.T) {
		b := &mp4.Stz2Box{FieldSize: 12, SampleNumber: 1, SampleSize: []uint16{1}}
		if err := b.Encode(&bytes.Buffer{}); err == nil {
			t.Error("expected error for field size 12, got nil")
		}
	})
	t.Run("size does not fit field size", func(t *testing.T) {
		b := &mp4.Stz2Box{FieldSize: mp4.Stz2FieldSize4, SampleNumber: 1, SampleSize: []uint16{16}}
		if err := b.Encode(&bytes.Buffer{}); err == nil {
			t.Error("expected error for a 4-bit value of 16, got nil")
		}
	})
	t.Run("sample count mismatch", func(t *testing.T) {
		b := &mp4.Stz2Box{FieldSize: mp4.Stz2FieldSize8, SampleNumber: 2, SampleSize: []uint16{1}}
		if err := b.Encode(&bytes.Buffer{}); err == nil {
			t.Error("expected error for mismatched sample count, got nil")
		}
	})
}

// TestStz2SampleReadRoundTrip builds a complete progressive track whose sample
// sizes come from an stz2 box and checks the data boundaries, the rewritten
// re-read and the stbl-level count checks.
func TestStz2SampleReadRoundTrip(t *testing.T) {
	sizes := []uint16{1, 2, 15}
	stz2 := &mp4.Stz2Box{
		FieldSize:    mp4.Stz2FieldSize4,
		SampleNumber: uint32(len(sizes)),
		SampleSize:   sizes,
	}
	// 18 bytes of payload laid out as one chunk, one sample per size entry.
	payload := bytes.Repeat([]byte{0xab}, 18)
	stbl := buildStz2SampleTable(t, stz2, payload)

	if got := stbl.SampleSize(); got != stz2 {
		t.Fatalf("StblBox.SampleSize returned %T, want *Stz2Box", got)
	}
	if stbl.Stsz != nil {
		t.Error("stbl with stz2 must not expose an stsz box")
	}
	if err := stbl.CheckSampleCounts(); err != nil {
		t.Fatalf("consistent stz2 table: %v", err)
	}

	// Encode the whole stbl and decode it through both paths.
	var buf bytes.Buffer
	if err := stbl.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	decBox, err := mp4.DecodeBox(0, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decStbl := decBox.(*mp4.StblBox)
	if decStbl.Stz2 == nil || decStbl.Stsz != nil {
		t.Fatalf("decoded table has stz2=%v stsz=%v", decStbl.Stz2 != nil, decStbl.Stsz)
	}
	for i, want := range sizes {
		if got := decStbl.Stz2.GetSampleSize(i + 1); got != uint32(want) {
			t.Errorf("decoded sample %d size %d, want %d", i+1, got, want)
		}
	}

	// Data boundaries: the ranges cover 0-1, 1-3 and 3-18.
	ranges, err := stbl.GetContainingChunks(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("got %d chunks, want 1", len(ranges))
	}
	var offset uint64
	for i, want := range sizes {
		size, err := decStbl.Stz2.GetTotalSampleSize(uint32(i+1), uint32(i+1))
		if err != nil {
			t.Fatal(err)
		}
		if size != uint64(want) {
			t.Errorf("sample %d range size %d, want %d", i+1, size, want)
		}
		offset += size
	}
	if offset != 18 {
		t.Errorf("ranges span %d bytes, want the full 18-byte payload", offset)
	}
}

// TestStz2CountMismatchIsError checks that an stz2 whose count disagrees with
// stts or the chunk layout is rejected when the samples are used, not silently.
func TestStz2CountMismatchIsError(t *testing.T) {
	stz2 := &mp4.Stz2Box{
		FieldSize:    mp4.Stz2FieldSize8,
		SampleNumber: 3,
		SampleSize:   []uint16{1, 2, 15},
	}
	stbl := buildStz2SampleTable(t, stz2, bytes.Repeat([]byte{0}, 18))

	t.Run("stts mismatch", func(t *testing.T) {
		stbl.Stts.SampleCount = []uint32{2}
		if err := stbl.CheckSampleCounts(); err == nil {
			t.Error("expected stts count mismatch error, got nil")
		}
		stbl.Stts.SampleCount = []uint32{3}
	})

	t.Run("stsc mismatch", func(t *testing.T) {
		stbl.Stsc.Entries = []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 2, FirstSampleNr: 1}}
		if err := stbl.CheckSampleCounts(); err == nil {
			t.Error("expected stsc count mismatch error, got nil")
		}
	})

	t.Run("both stsz and stz2 rejected on decode", func(t *testing.T) {
		good := buildStz2SampleTable(t, &mp4.Stz2Box{
			FieldSize:    mp4.Stz2FieldSize8,
			SampleNumber: 1,
			SampleSize:   []uint16{1},
		}, []byte{0})
		good.AddChild(&mp4.StszBox{SampleNumber: 1, SampleUniformSize: 1})
		var buf bytes.Buffer
		if err := good.Encode(&buf); err != nil {
			t.Fatal(err)
		}
		if _, err := mp4.DecodeBox(0, bytes.NewReader(buf.Bytes())); err == nil {
			t.Error("expected error for an stbl with both stsz and stz2, got nil")
		}
	})
}

// TestStz2ProgressiveFileRead builds a complete in-memory progressive file whose
// audio samples (1, 2 and 15 bytes) are described by an stz2 box, then checks the
// sample metadata and byte boundaries after an encode/decode round trip.
func TestStz2ProgressiveFileRead(t *testing.T) {
	sizes := []uint16{1, 2, 15}
	payload := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	payload = append(payload, bytes.Repeat([]byte{0xab}, 13)...) // 18 bytes total

	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(48000, "audio", "und")
	// A progressive file has no mvex box; the empty init creates one.
	init.Moov.Mvex = nil
	init.Moov.Children = removeBoxByType(init.Moov.Children, "mvex")
	stbl := trak.Mdia.Minf.Stbl
	// The empty track ships an stsz; replace the sample-size boxes with an stz2.
	oldChildren := make([]mp4.Box, 0, len(stbl.Children))
	for _, child := range stbl.Children {
		if child.Type() != "stsz" {
			oldChildren = append(oldChildren, child)
		}
	}
	stz2 := &mp4.Stz2Box{
		FieldSize:    mp4.Stz2FieldSize4,
		SampleNumber: uint32(len(sizes)),
		SampleSize:   sizes,
	}
	stbl.Children = append(oldChildren, stz2)
	stbl.Stsz = nil
	stbl.Stz2 = stz2
	stbl.Stts.SampleCount = []uint32{uint32(len(sizes))}
	stbl.Stts.SampleTimeDelta = []uint32{512}
	stbl.Stsc.Entries = nil
	if err := stbl.Stsc.AddEntry(1, uint32(len(sizes)), 1); err != nil {
		t.Fatal(err)
	}

	// Reserve a one-entry stco up front so adding the real offset later does not
	// change any box size and shift the layout.
	stbl.Stco.ChunkOffset = []uint32{0}
	f := mp4.NewFile()
	f.AddChild(init.Ftyp, 0)
	moovStart := init.Ftyp.Size()
	f.AddChild(init.Moov, moovStart)
	mdatStart := moovStart + init.Moov.Size()
	mdat := &mp4.MdatBox{}
	mdat.SetData(payload)
	f.AddChild(mdat, mdatStart)

	// Probe the encoded layout, then write the real mdat payload offset. With the
	// stco entry already present, this keeps every box size and position stable.
	var first bytes.Buffer
	if err := f.Encode(&first); err != nil {
		t.Fatal(err)
	}
	probed, err := mp4.DecodeFile(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatalf("decode file with stz2 track: %v", err)
	}
	probed.Moov.Traks[0].Mdia.Minf.Stbl.Stco.ChunkOffset =
		[]uint32{uint32(probed.Mdat.PayloadAbsoluteOffset())}

	var encoded bytes.Buffer
	if err := probed.Encode(&encoded); err != nil {
		t.Fatal(err)
	}
	decoded, err := mp4.DecodeFile(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("re-decode corrected file: %v", err)
	}
	decTrak := decoded.Moov.Traks[0]
	decStbl := decTrak.Mdia.Minf.Stbl
	if decStbl.Stz2 == nil {
		t.Fatal("decoded track lost its stz2 box")
	}

	// Sample metadata comes from the stz2 table through the normal track API.
	samples, err := decTrak.GetSampleData(1, 3)
	if err != nil {
		t.Fatalf("GetSampleData: %v", err)
	}
	for i, sample := range samples {
		if sample.Size != uint32(sizes[i]) {
			t.Errorf("sample %d size %d, want %d", i+1, sample.Size, sizes[i])
		}
	}

	// Byte boundaries: reading each sample alone gives exactly its declared bytes,
	// and together they reconstruct the full 18-byte payload.
	for i, size := range sizes {
		data, err := decoded.ReadSampleData(decTrak, uint32(i+1), uint32(i+1), nil, nil)
		if err != nil {
			t.Fatalf("read sample %d: %v", i+1, err)
		}
		if len(data) != int(size) {
			t.Errorf("sample %d returned %d bytes, want %d", i+1, len(data), size)
		}
	}
	all, err := decoded.ReadSampleData(decTrak, 1, 3, nil, nil)
	if err != nil {
		t.Fatalf("read all samples: %v", err)
	}
	if !bytes.Equal(all, payload) {
		t.Errorf("joined samples = %x, want %x", all, payload)
	}

	// A re-encoded file decodes again and keeps the same compact box.
	var rewritten bytes.Buffer
	if err := decoded.Encode(&rewritten); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rewritten.Bytes(), encoded.Bytes()) {
		t.Error("re-encoded stz2 file differs from the original bytes")
	}
}

// buildStz2SampleTable constructs a one-chunk stbl whose stts/stsc/stco match the
// stz2 sample count, with chunkOffset pointing at the start of payload.
func buildStz2SampleTable(t *testing.T, stz2 *mp4.Stz2Box, payload []byte) *mp4.StblBox {
	t.Helper()
	nrSamples := stz2.SampleNumber
	stbl := mp4.NewStblBox()
	stbl.AddChild(&mp4.SttsBox{
		SampleCount:     []uint32{nrSamples},
		SampleTimeDelta: []uint32{512},
	})
	stsc := &mp4.StscBox{}
	if err := stsc.AddEntry(1, nrSamples, 1); err != nil {
		t.Fatal(err)
	}
	stbl.AddChild(stsc)
	stbl.AddChild(stz2)
	stbl.AddChild(&mp4.StcoBox{ChunkOffset: []uint32{0}})
	return stbl
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// removeBoxByType returns children without the boxes of the given type.
func removeBoxByType(children []mp4.Box, boxType string) []mp4.Box {
	out := make([]mp4.Box, 0, len(children))
	for _, child := range children {
		if child.Type() != boxType {
			out = append(out, child)
		}
	}
	return out
}
