package avc_test

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/sei"
)

// ccData builds a cc_data() structure (ANSI/CTA-708-E Section 4.3) with one cc entry per pair.
// field is avc.CTA608Field1 or avc.CTA608Field2. cc_count is the low 5 bits of the first byte.
func ccData(field int, pairs ...[2]byte) []byte {
	data := make([]byte, 0, 2+3*len(pairs))
	data = append(data, byte(len(pairs))&0x1f, 0xff) // cc_count, reserved
	ccType := byte(0)
	if field == avc.CTA608Field2 {
		ccType = 1
	}
	for _, p := range pairs {
		data = append(data, 0x04|ccType) // cc_valid=1, cc_type
		data = append(data, p[0], p[1])
	}
	return data
}

// cta608SEINalu creates an AVC SEI NAL unit carrying one CTA-608 type-4 message with ccData.
func cta608SEINalu(t *testing.T, ccData []byte) []byte {
	t.Helper()
	nalu, err := avc.CreateSEINalu([]sei.SEIMessage{sei.CreateCTA608SEIMessage(ccData)})
	if err != nil {
		t.Fatalf("CreateSEINalu failed: %v", err)
	}
	return nalu
}

// lengthPrefixedSample builds an AVCC sample from NAL units with 4-byte length fields.
func lengthPrefixedSample(nalus ...[]byte) []byte {
	size := 4 * len(nalus)
	for _, nalu := range nalus {
		size += len(nalu)
	}
	sample := make([]byte, 0, size)
	var lenBuf [4]byte
	for _, nalu := range nalus {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(nalu)))
		sample = append(sample, lenBuf[:]...)
		sample = append(sample, nalu...)
	}
	return sample
}

func TestExtractCTA608PairsFieldsAndOrder(t *testing.T) {
	field1Pairs := [][2]byte{{0x94, 0x20}, {0x94, 0xae}}
	field2Pairs := [][2]byte{{0x01, 0x80}, {0x02, 0x81}}
	// One SEI with a field-1 cc_data and another with a field-2 cc_data, plus an unrelated SEI.
	seif1 := cta608SEINalu(t, ccData(avc.CTA608Field1, field1Pairs...))
	seif2 := cta608SEINalu(t, ccData(avc.CTA608Field2, field2Pairs...))
	otherSEI, err := avc.CreateSEINalu([]sei.SEIMessage{
		sei.NewSEIData(sei.SEIUserDataUnregisteredType, []byte("0123456789abcdef")),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A non-SEI NAL unit (AUD) between them must be ignored as well.
	aud := []byte{byte(avc.NALU_AUD) << 3}
	sample := lengthPrefixedSample(seif1, aud, otherSEI, seif2)

	got, err := avc.ExtractCTA608Pairs(sample)
	if err != nil {
		t.Fatalf("ExtractCTA608Pairs failed: %v", err)
	}
	want := []avc.CTA608Pair{
		{Field: avc.CTA608Field1, Data1: 0x94, Data2: 0x20},
		{Field: avc.CTA608Field1, Data1: 0x94, Data2: 0xae},
		{Field: avc.CTA608Field2, Data1: 0x01, Data2: 0x80},
		{Field: avc.CTA608Field2, Data1: 0x02, Data2: 0x81},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d pairs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pair %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestExtractCTA608PairsSkipsNonCTA608RegisteredSEI(t *testing.T) {
	// A type-4 message with another country code must be ignored, not produce pairs or an error.
	other := sei.NewSEIData(sei.SEIUserDataRegisteredITUtT35Type,
		[]byte{0xb5, 0x00, 0x31, 0x11, 0x22, 0x33, 0x44, 0x55})
	nalu, err := avc.CreateSEINalu([]sei.SEIMessage{other})
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := avc.ExtractCTA608Pairs(lengthPrefixedSample(nalu))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("got %d pairs, want 0", len(pairs))
	}
}

func TestExtractCTA608PairsEmptyAndInvalid(t *testing.T) {
	if pairs, err := avc.ExtractCTA608Pairs(nil); err != nil || pairs != nil {
		t.Errorf("empty sample: got pairs=%v err=%v, want nil, nil", pairs, err)
	}
	// A sample whose NALU length field points past the sample is truncated.
	truncatedSample := []byte{0, 0, 0, 10, 0x06, 0x05}
	if _, err := avc.ExtractCTA608Pairs(truncatedSample); err == nil {
		t.Error("truncated NALU: expected error, got nil")
	}
	// A complete length-prefixed SEI NALU whose CTA-608 cc_data is malformed.
	badSEI := cta608SEINalu(t, []byte{0x01, 0xff}) // cc_count=1 but no cc entry follows
	if _, err := avc.ExtractCTA608Pairs(lengthPrefixedSample(badSEI)); err == nil {
		t.Error("invalid cc_data payload: expected error, got nil")
	}
	// A truncated SEI NALU with a declared type-4 payload shorter than the T.35 header.
	shortType4, err := avc.CreateSEINalu([]sei.SEIMessage{
		sei.NewSEIData(sei.SEIUserDataRegisteredITUtT35Type, []byte{0xb5, 0x00, 0x31}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := avc.ExtractCTA608Pairs(lengthPrefixedSample(shortType4)); err == nil {
		t.Error("truncated type-4 SEI: expected error, got nil")
	}
}

func TestExtractCTA608PairsKeepsSampleBytes(t *testing.T) {
	seif1 := cta608SEINalu(t, ccData(avc.CTA608Field1, [2]byte{0x94, 0x20}))
	sample := lengthPrefixedSample(seif1)
	original := make([]byte, len(sample))
	copy(original, sample)
	if _, err := avc.ExtractCTA608Pairs(sample); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(sample) != hex.EncodeToString(original) {
		t.Error("sample data was modified by ExtractCTA608Pairs")
	}
}

// TestExtractCTA608PairsRealWorldSEI uses the CTA-608 SEI of the sei package tests, which carries
// multiple field-1 byte pairs, to check integration with the full SEI parsing path.
func TestExtractCTA608PairsRealWorldSEI(t *testing.T) {
	// SEI NAL unit = NAL header 0x06 followed by the EBSP of seiCTA608Hex.
	ebsp, err := hex.DecodeString("0434b500314741393403cefffc9420fc94aefc9162fce56efc67bafc91b9" +
		"fcb0b0fcbab0fcb0bafcb031fcbab0fcb080fc942cfc942f80")
	if err != nil {
		t.Fatal(err)
	}
	nalu := append([]byte{0x06}, ebsp...)
	pairs, err := avc.ExtractCTA608Pairs(lengthPrefixedSample(nalu))
	if err != nil {
		t.Fatalf("ExtractCTA608Pairs failed: %v", err)
	}
	// Field1 of this SEI is "942094ae9162e56e67ba91b9b0b0bab0b0bab031bab0b080942c942f": 14 pairs.
	if len(pairs) != 14 {
		t.Fatalf("got %d pairs, want 14", len(pairs))
	}
	for _, p := range pairs {
		if p.Field != avc.CTA608Field1 {
			t.Errorf("got field %d, want field 1", p.Field)
		}
	}
	if pairs[0] != (avc.CTA608Pair{Field: avc.CTA608Field1, Data1: 0x94, Data2: 0x20}) {
		t.Errorf("first pair = %+v, want {1 0x94 0x20}", pairs[0])
	}
}
