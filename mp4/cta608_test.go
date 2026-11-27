package mp4_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/Eyevinn/mp4ff/sei"
)

// cta608CCData builds a cc_data() structure with one entry per byte pair.
func cta608CCData(field int, pairs ...[2]byte) []byte {
	data := make([]byte, 0, 2+3*len(pairs))
	data = append(data, byte(len(pairs))&0x1f, 0xff) // cc_count and reserved byte
	ccType := byte(0)
	if field == avc.CTA608Field2 {
		ccType = 1
	}
	for _, p := range pairs {
		data = append(data, 0x04|ccType, p[0], p[1]) // cc_valid=1, cc_type, two data bytes
	}
	return data
}

// cta608AVCSample builds an AVC sample carrying an SEI NAL unit with the given CTA-608 cc_data,
// plus an AUD NAL unit to show that non-SEI NAL units are skipped.
func cta608AVCSample(t *testing.T, field int, pairs ...[2]byte) []byte {
	t.Helper()
	seiNalu, err := avc.CreateSEINalu([]sei.SEIMessage{sei.CreateCTA608SEIMessage(cta608CCData(field, pairs...))})
	if err != nil {
		t.Fatalf("CreateSEINalu: %v", err)
	}
	nalus := [][]byte{{0x09}, seiNalu} // AUD then SEI
	sample := make([]byte, 0, 4*len(nalus))
	var lenBuf [4]byte
	for _, nalu := range nalus {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(nalu)))
		sample = append(sample, lenBuf[:]...)
		sample = append(sample, nalu...)
	}
	return sample
}

// cta608InitSegment creates a one-track AVC (avc1) init segment with the given timescale.
func cta608InitSegment(t *testing.T, timescale uint32) *mp4.InitSegment {
	t.Helper()
	sps, err := hex.DecodeString(sps1nalu)
	if err != nil {
		t.Fatal(err)
	}
	pps, err := hex.DecodeString(pps1nalu)
	if err != nil {
		t.Fatal(err)
	}
	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(timescale, "video", "und")
	if err := trak.SetAVCDescriptor("avc1", [][]byte{sps}, [][]byte{pps}, true); err != nil {
		t.Fatal(err)
	}
	return init
}

// TestCTA608PresentationTime verifies the time computation of the task's reference case:
// timescale 1000, DTS 100 and composition offset 20 give a presentation time of 120ms.
func TestCTA608PresentationTime(t *testing.T) {
	const timescale = 1000
	init := cta608InitSegment(t, timescale)
	sampleData := cta608AVCSample(t, avc.CTA608Field1, [2]byte{0x94, 0x20})
	frag, err := mp4.CreateFragment(1, mp4.DefaultTrakID)
	if err != nil {
		t.Fatal(err)
	}
	frag.AddFullSample(mp4.FullSample{
		Sample:     mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 100, Size: uint32(len(sampleData)), CompositionTimeOffset: 20},
		DecodeTime: 100,
		Data:       sampleData,
	})
	trex, _ := init.Moov.Mvex.GetTrex(mp4.DefaultTrakID)
	events, err := frag.AppendCTA608Events(nil, trex)
	if err != nil {
		t.Fatalf("AppendCTA608Events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.TrackID != mp4.DefaultTrakID || ev.Field != avc.CTA608Field1 ||
		ev.Data1 != 0x94 || ev.Data2 != 0x20 {
		t.Errorf("event = %+v, want track 1 field 1 pair 9420", ev)
	}
	if ev.PresentationTime != 120 {
		t.Errorf("presentation time = %d, want 120 (DTS 100 + composition offset 20)", ev.PresentationTime)
	}

	// The same fragment must also work without a trex (first traf selected), and Append must
	// reuse caller-provided storage instead of allocating a new slice.
	reused := make([]mp4.CTA608Event, 0, 4)
	eventsNil, err := frag.AppendCTA608Events(reused, nil)
	if err != nil {
		t.Fatalf("AppendCTA608Events with nil trex: %v", err)
	}
	if len(eventsNil) != 1 || eventsNil[0] != ev {
		t.Errorf("nil trex: got %+v, want %+v", eventsNil, ev)
	}
}

// TestCTA608TimelineAcrossFragments builds a fragmented file with several fragments and checks
// the full timeline: field ordering, pairs at the same time staying ordered, multi-fragment
// accumulation, and times derived from tfdt, durations and composition offsets.
func TestCTA608TimelineAcrossFragments(t *testing.T) {
	const (
		timescale uint32 = 1000
		trackID          = mp4.DefaultTrakID
	)
	init := cta608InitSegment(t, timescale)

	// Fragment 1: one sample at DTS 0 with two field-1 pairs and one field-2 pair.
	sample1 := cta608AVCSample(t, avc.CTA608Field1, [2]byte{0x94, 0x20}, [2]byte{0x94, 0xae})
	sample1 = append(sample1, cta608AVCSample(t, avc.CTA608Field2, [2]byte{0x01, 0x80})...)
	frag1, err := mp4.CreateFragment(1, trackID)
	if err != nil {
		t.Fatal(err)
	}
	frag1.AddFullSample(mp4.FullSample{
		Sample: mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 40, Size: uint32(len(sample1)), CompositionTimeOffset: 10},
		Data:   sample1,
	})

	// Fragment 2 starts at tfdt 100 (two 40ms samples plus a gap handled by tfdt): one sample
	// at DTS 100, offset -5, with one field-1 pair.
	sample2 := cta608AVCSample(t, avc.CTA608Field1, [2]byte{0x91, 0x62})
	frag2, err := mp4.CreateFragment(2, trackID)
	if err != nil {
		t.Fatal(err)
	}
	frag2.AddFullSample(mp4.FullSample{
		Sample:     mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 40, Size: uint32(len(sample2)), CompositionTimeOffset: -5},
		DecodeTime: 100,
		Data:       sample2,
	})

	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	seg := mp4.NewMediaSegment()
	seg.AddFragment(frag1)
	seg.AddFragment(frag2)
	if err := seg.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	parsed, err := mp4.DecodeFile(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	events, err := parsed.CTA608Events(trackID)
	if err != nil {
		t.Fatalf("CTA608Events: %v", err)
	}
	want := []mp4.CTA608Event{
		{TrackID: trackID, Field: avc.CTA608Field1, Data1: 0x94, Data2: 0x20, PresentationTime: 10},
		{TrackID: trackID, Field: avc.CTA608Field1, Data1: 0x94, Data2: 0xae, PresentationTime: 10},
		{TrackID: trackID, Field: avc.CTA608Field2, Data1: 0x01, Data2: 0x80, PresentationTime: 10},
		{TrackID: trackID, Field: avc.CTA608Field1, Data1: 0x91, Data2: 0x62, PresentationTime: 95},
	}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event %d: got %+v, want %+v", i, events[i], want[i])
		}
	}

	// The media samples must be unchanged: re-encode the decoded file and decode it again.
	var out bytes.Buffer
	if err := parsed.Encode(&out); err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	reparsed, err := mp4.DecodeFile(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("decode re-encoded file: %v", err)
	}
	eventsAgain, err := reparsed.CTA608Events(trackID)
	if err != nil {
		t.Fatalf("CTA608Events after re-encode: %v", err)
	}
	if len(eventsAgain) != len(want) {
		t.Fatalf("after re-encode got %d events, want %d", len(eventsAgain), len(want))
	}
	for i := range want {
		if eventsAgain[i] != want[i] {
			t.Errorf("after re-encode event %d: got %+v, want %+v", i, eventsAgain[i], want[i])
		}
	}
}

// TestCTA608EventsTruncatedAndInvalid checks that truncated NAL units and invalid caption
// payloads are reported as errors rather than dropped.
func TestCTA608EventsTruncatedAndInvalid(t *testing.T) {
	const timescale uint32 = 1000
	init := cta608InitSegment(t, timescale)
	trex, _ := init.Moov.Mvex.GetTrex(mp4.DefaultTrakID)

	// Truncated NAL unit: the length field declares more bytes than the sample carries.
	truncated := []byte{0, 0, 0, 10, 0x06, 0x05}
	frag, err := mp4.CreateFragment(1, mp4.DefaultTrakID)
	if err != nil {
		t.Fatal(err)
	}
	frag.AddFullSample(mp4.FullSample{
		Sample: mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 40, Size: uint32(len(truncated))},
		Data:   truncated,
	})
	if _, err := frag.AppendCTA608Events(nil, trex); err == nil {
		t.Error("truncated NAL unit: expected error, got nil")
	}

	// Malformed CTA-608 cc_data: cc_count 1 but no cc entry follows.
	badSEI, err := avc.CreateSEINalu([]sei.SEIMessage{sei.CreateCTA608SEIMessage([]byte{0x01, 0xff})})
	if err != nil {
		t.Fatal(err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(badSEI)))
	badSample := append(append([]byte{}, lenBuf[:]...), badSEI...)
	frag2, err := mp4.CreateFragment(2, mp4.DefaultTrakID)
	if err != nil {
		t.Fatal(err)
	}
	frag2.AddFullSample(mp4.FullSample{
		Sample:     mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: 40, Size: uint32(len(badSample))},
		DecodeTime: 40,
		Data:       badSample,
	})
	if _, err := frag2.AppendCTA608Events(nil, trex); err == nil {
		t.Error("invalid cc_data: expected error, got nil")
	}
}

// TestCTA608EventsTrackSelection checks AVC track validation and absent tracks.
func TestCTA608EventsTrackSelection(t *testing.T) {
	init := cta608InitSegment(t, 1000)
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	parsed, err := mp4.DecodeFile(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if events, err := parsed.CTA608Events(mp4.DefaultTrakID); err != nil || len(events) != 0 {
		t.Errorf("file without fragments: got events=%v err=%v, want no events", events, err)
	}
	if _, err := parsed.CTA608Events(42); err == nil {
		t.Error("unknown trackID: expected error, got nil")
	}
}
