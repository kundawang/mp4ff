package mp4_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
)

// testCue is one cue carried by a vttc box of a fixture.
type testCue struct {
	id       string
	settings string
	text     string
}

// vttSample is one wvtt sample of a fixture: its timing and cue boxes.
type vttSample struct {
	dts  uint64 // decode time in the track timescale
	dur  uint32 // duration in the track timescale
	cto  int32  // composition time offset
	vttc []testCue
	vtte bool // the sample is a vtte box
	vttn bool // the sample is a vttn box
}

// overlappingCueSamples is two segments of cues that overlap. One cue continues over two
// samples, one starts inside it, and a vtte sample ends the cues active at its time early.
var overlappingCueSamples = [][]vttSample{
	{
		{dts: 0, dur: 1000, vttc: []testCue{{id: "girl", settings: "line:20%", text: "Hello"}}},
		{dts: 1000, dur: 1000, vttc: []testCue{{id: "girl", settings: "line:20%", text: "Hello"}}},
		{dts: 2000, dur: 1000, vttc: []testCue{{text: "Middle"}}},
	},
	{
		{dts: 3000, dur: 2000, vttc: []testCue{
			{id: "boy", text: "Two"},
			{id: "girl", settings: "align:start", text: "Bye"},
		}},
		{dts: 5000, dur: 1000, vtte: true},
		{dts: 6000, dur: 1000, vttc: []testCue{{id: "after", text: "After"}}},
	},
}

// encodeWvttSample encodes the boxes of one wvtt sample.
func encodeWvttSample(t *testing.T, s vttSample) []byte {
	t.Helper()
	var buf bytes.Buffer
	if s.vtte {
		if err := (&mp4.VtteBox{}).Encode(&buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	if s.vttn {
		if err := (&mp4.VttnBox{}).Encode(&buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	for _, c := range s.vttc {
		vttc := &mp4.VttcBox{}
		if c.id != "" {
			vttc.AddChild(&mp4.IdenBox{CueID: c.id})
		}
		if c.settings != "" {
			vttc.AddChild(&mp4.SttgBox{Settings: c.settings})
		}
		vttc.AddChild(&mp4.PaylBox{CueText: c.text})
		if err := vttc.Encode(&buf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// sttsEntries returns run-length stts entries for the given sample durations.
func sttsEntries(durs []uint32) (counts, deltas []uint32) {
	for _, d := range durs {
		if n := len(deltas); n > 0 && deltas[n-1] == d {
			counts[n-1]++
			continue
		}
		counts = append(counts, 1)
		deltas = append(deltas, d)
	}
	return counts, deltas
}

// buildProgressiveWvtt builds a progressive file with one wvtt track.
func buildProgressiveWvtt(t *testing.T, timescale uint32, samples []vttSample) []byte {
	t.Helper()
	moov := mp4.NewMoovBox()
	moov.AddChild(mp4.CreateMvhd())
	trak := mp4.CreateEmptyTrak(1, timescale, "text", "eng")
	if err := trak.SetWvttDescriptor(""); err != nil {
		t.Fatal(err)
	}
	moov.AddChild(trak)
	stbl := trak.Mdia.Minf.Stbl
	var sizes []uint32
	var durs []uint32
	var offsets []int32
	var payload []byte
	hasCTO := false
	for _, s := range samples {
		data := encodeWvttSample(t, s)
		sizes = append(sizes, uint32(len(data)))
		durs = append(durs, s.dur)
		offsets = append(offsets, s.cto)
		hasCTO = hasCTO || s.cto != 0
		payload = append(payload, data...)
	}
	counts, deltas := sttsEntries(durs)
	stbl.Stts.SampleCount = counts
	stbl.Stts.SampleTimeDelta = deltas
	stbl.Stsz.SampleNumber = uint32(len(sizes))
	stbl.Stsz.SampleSize = sizes
	if err := stbl.Stsc.AddEntry(1, uint32(len(sizes)), 1); err != nil {
		t.Fatal(err)
	}
	if hasCTO {
		ctts := &mp4.CttsBox{}
		counts := make([]uint32, len(offsets))
		for i := range counts {
			counts[i] = 1
		}
		if err := ctts.AddSampleCountsAndOffset(counts, offsets); err != nil {
			t.Fatal(err)
		}
		stbl.AddChild(ctts)
	}
	ftyp := encodeBox(t, mp4.CreateFtyp())
	// The moov size does not depend on the chunk offset value, so two encodes converge.
	stbl.Stco.ChunkOffset = []uint32{0}
	moovBytes := encodeBox(t, moov)
	stbl.Stco.ChunkOffset = []uint32{uint32(len(ftyp) + len(moovBytes) + 8)}
	moovBytes = encodeBox(t, moov)
	var file bytes.Buffer
	file.Write(ftyp)
	file.Write(moovBytes)
	file.Write(boxHeader(uint32(8+len(payload)), "mdat"))
	file.Write(payload)
	return file.Bytes()
}

// boxHeader returns an 8-byte box header.
func boxHeader(size uint32, boxType string) []byte {
	return []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size),
		boxType[0], boxType[1], boxType[2], boxType[3]}
}

// buildFragmentedWvtt builds a fragmented file with one wvtt track, one fragment per sample
// list.
func buildFragmentedWvtt(t *testing.T, timescale uint32, frags [][]vttSample) []byte {
	return buildFragmentedWvttWithEntry(t, timescale, frags, false)
}

// buildFragmentedWvttWithEntry builds the same file with a wvtt or a wvtc sample entry, since
// the experimental paint-model wvtc entry also allows vttn samples.
func buildFragmentedWvttWithEntry(t *testing.T, timescale uint32, frags [][]vttSample, paintModel bool) []byte {
	t.Helper()
	init := mp4.CreateEmptyInit()
	init.Moov.Mvhd.Timescale = timescale
	trak := init.AddEmptyTrack(timescale, "text", "eng")
	if paintModel {
		if err := trak.SetWvtcDescriptor(""); err != nil {
			t.Fatal(err)
		}
	} else if err := trak.SetWvttDescriptor(""); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	for i, samples := range frags {
		frag, err := mp4.CreateFragment(uint32(i+1), trak.Tkhd.TrackID)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range samples {
			data := encodeWvttSample(t, s)
			frag.AddFullSample(mp4.FullSample{
				Sample: mp4.Sample{
					Dur:                   s.dur,
					Size:                  uint32(len(data)),
					CompositionTimeOffset: s.cto,
				},
				DecodeTime: s.dts,
				Data:       data,
			})
		}
		if err := frag.Encode(&buf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// checkRoundTrip writes the cues and parses them back, which checks that the output is valid
// WebVTT text that reads back to the same cues.
func checkRoundTrip(t *testing.T, vtt *mp4.WebVTT) {
	t.Helper()
	var buf bytes.Buffer
	if err := vtt.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	parsed, err := mp4.ParseWebVTT(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse back %q: %v", buf.String(), err)
	}
	if fmt.Sprint(parsed.Cues) != fmt.Sprint(vtt.Cues) {
		t.Errorf("parsed cues\n%+v\nwant\n%+v\ntext %q", parsed.Cues, vtt.Cues, buf.String())
	}
}

func decodeFixture(t *testing.T, data []byte) *mp4.File {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return f
}

// fixtureWant is the export of testdata/wvtt_overlap_prog.mp4 and wvtt_overlap_frag.mp4.
// The two files carry the same cues with the same presentation times, so the export from
// either gives the same WebVTT text.
var fixtureWant = []mp4.Cue{
	{ID: "intro", Start: 0, End: 2000, Settings: "line:20%", Text: "First line\nSecond line"},
	{Start: 2000, End: 2500, Settings: "align:start", Text: "Overlapping cue"},
	{ID: "short", Start: 2500, End: 3000, Text: "Ends early"},
	{ID: "long", Start: 2500, End: 4000, Settings: "line:80%", Text: "Continues"},
	{ID: "tail", Start: 4500, End: 5000, Text: "After the empty sample"},
}

// fixtureWantText is the WebVTT text of fixtureWant.
const fixtureWantText = `WEBVTT

intro
00:00.000 --> 00:02.000 line:20%
First line
Second line

00:02.000 --> 00:02.500 align:start
Overlapping cue

short
00:02.500 --> 00:03.000
Ends early

long
00:02.500 --> 00:04.000 line:80%
Continues

tail
00:04.500 --> 00:05.000
After the empty sample

`

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExportWebVTTProgressive(t *testing.T) {
	f := decodeFixture(t, readFixture(t, "wvtt_overlap_prog.mp4"))
	if f.IsFragmented() {
		t.Fatal("fixture is fragmented")
	}
	vtt, err := f.ExportWebVTT(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(vtt.Cues) != fmt.Sprint(fixtureWant) {
		t.Errorf("got %+v, want %+v", vtt.Cues, fixtureWant)
	}
	if vtt.TrackID != 1 {
		t.Errorf("track ID %d, want 1", vtt.TrackID)
	}
	var buf bytes.Buffer
	if err := vtt.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != fixtureWantText {
		t.Errorf("got text %q, want %q", buf.String(), fixtureWantText)
	}
	checkRoundTrip(t, vtt)
}

func TestExportWebVTTFragmented(t *testing.T) {
	f := decodeFixture(t, readFixture(t, "wvtt_overlap_frag.mp4"))
	if !f.IsFragmented() {
		t.Fatal("fixture is not fragmented")
	}
	vtt, err := f.ExportWebVTT(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(vtt.Cues) != fmt.Sprint(fixtureWant) {
		t.Errorf("got %+v, want %+v", vtt.Cues, fixtureWant)
	}
	var buf bytes.Buffer
	if err := vtt.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != fixtureWantText {
		t.Errorf("got text %q,\nwant %q", buf.String(), fixtureWantText)
	}
	checkRoundTrip(t, vtt)
}

func TestExportWebVTTLazyMdat(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"progressive", readFixture(t, "wvtt_overlap_prog.mp4")},
		{"fragmented", readFixture(t, "wvtt_overlap_frag.mp4")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := bytes.NewReader(tc.data)
			f, err := mp4.DecodeFile(rs, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.ExportWebVTT(0, nil); err == nil {
				t.Error("export without a ReadSeeker must fail for a lazy mdat")
			}
			rs.Seek(0, 0)
			vtt, err := f.ExportWebVTT(0, rs)
			if err != nil {
				t.Fatal(err)
			}
			checkRoundTrip(t, vtt)
		})
	}
}

// TestExportWebVTTVttn checks that a vttn sample of a wvtc track, which changes nothing,
// extends the cues active when it starts over its own duration.
func TestExportWebVTTVttn(t *testing.T) {
	frags := [][]vttSample{
		{
			{dts: 0, dur: 1000, vttc: []testCue{{id: "a", text: "A"}}},
			{dts: 1000, dur: 1000, vttn: true},
			{dts: 2000, dur: 1000, vttn: true},
			{dts: 3000, dur: 1000, vttc: []testCue{{id: "b", text: "B"}}},
		},
	}
	f := decodeFixture(t, buildFragmentedWvttWithEntry(t, 1000, frags, true))
	vtt, err := f.ExportWebVTT(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []mp4.Cue{
		{ID: "a", Start: 0, End: 3000, Text: "A"},
		{ID: "b", Start: 3000, End: 4000, Text: "B"},
	}
	if fmt.Sprint(vtt.Cues) != fmt.Sprint(want) {
		t.Errorf("got %+v, want %+v", vtt.Cues, want)
	}
	checkRoundTrip(t, vtt)
}

// TestExportWebVTTSameTextInOneSample checks that two cues with the same content in one
// sample stay separate cues, and that a following sample restates only one of them.
func TestExportWebVTTSameTextInOneSample(t *testing.T) {
	frags := [][]vttSample{
		{
			{dts: 0, dur: 1000, vttc: []testCue{{text: "Same"}, {text: "Same"}}},
			{dts: 1000, dur: 1000, vttc: []testCue{{text: "Same"}}},
		},
	}
	f := decodeFixture(t, buildFragmentedWvtt(t, 1000, frags))
	vtt, err := f.ExportWebVTT(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []mp4.Cue{
		{Start: 0, End: 1000, Text: "Same"},
		{Start: 0, End: 2000, Text: "Same"},
	}
	if fmt.Sprint(vtt.Cues) != fmt.Sprint(want) {
		t.Errorf("got %+v, want %+v", vtt.Cues, want)
	}
}

func TestExportWebVTTTimescaleAndCTO(t *testing.T) {
	samples := []vttSample{
		{dts: 0, dur: 90000, cto: 4500, vttc: []testCue{{text: "A"}}},
		{dts: 90000, dur: 90000, cto: 0, vttc: []testCue{{text: "B"}}},
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"progressive", buildProgressiveWvtt(t, 90000, samples)},
		{"fragmented", buildFragmentedWvtt(t, 90000, [][]vttSample{samples})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := decodeFixture(t, tc.data)
			vtt, err := f.ExportWebVTT(0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(vtt.Cues) != 2 {
				t.Fatalf("got %d cues, want 2", len(vtt.Cues))
			}
			if vtt.Cues[0].Start != 50*mp4.TimeMillisecond || vtt.Cues[0].End != 1050*mp4.TimeMillisecond {
				t.Errorf("cue A times %s-%s, want 00:00.050-00:01.050", vtt.Cues[0].Start, vtt.Cues[0].End)
			}
			if vtt.Cues[1].Start != 1000*mp4.TimeMillisecond {
				t.Errorf("cue B starts at %s, want 00:01.000", vtt.Cues[1].Start)
			}
		})
	}
}

func TestExportWebVTTErrors(t *testing.T) {
	nonMonotonic := []vttSample{
		{dts: 0, dur: 1000, cto: 500, vttc: []testCue{{text: "A"}}},
		{dts: 1000, dur: 1000, cto: -600, vttc: []testCue{{text: "B"}}},
	}
	duplicateID := []vttSample{
		{dts: 0, dur: 2000, vttc: []testCue{{id: "same", text: "A"}}},
		{dts: 2000, dur: 2000, cto: -1000, vttc: []testCue{{id: "same", text: "B"}}},
	}
	huge := []vttSample{
		{dts: 0, dur: 1000, vttc: []testCue{{text: strings.Repeat("x", mp4.MaxCueLength+1)}}},
	}

	// A sample whose first box is a vttc with an iden box, whose size field is then corrupted.
	badBoxData := buildProgressiveWvtt(t, 1000, []vttSample{
		{dts: 0, dur: 1000, vttc: []testCue{{id: "id", text: "A"}}},
	})
	idenPos := bytes.Index(badBoxData, []byte("iden"))
	if idenPos < 4 {
		t.Fatal("fixture has no iden box")
	}
	badBoxData[idenPos-4] = 0xff // the box size now exceeds the sample

	testCases := []struct {
		name      string
		data      []byte
		trackID   uint32
		wantTrack string
		want      string
	}{
		{"no such track", buildProgressiveWvtt(t, 1000, overlappingCueSamples[0]), 7, "track 7", "no such track"},
		{"non monotonic", buildFragmentedWvtt(t, 1000, [][]vttSample{nonMonotonic}), 0, "track 1",
			"presentation time 400 before the previous sample's 500"},
		{"duplicate ID", buildFragmentedWvtt(t, 1000, [][]vttSample{duplicateID}), 0, "track 1",
			`cue ID "same"`},
		{"huge cue", buildProgressiveWvtt(t, 1000, huge), 0, "track 1", "more than the"},
		{"bad box", badBoxData, 0, "track 1", "sample 1"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			f := decodeFixture(t, tc.data)
			_, err := f.ExportWebVTT(tc.trackID, nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantTrack) {
				t.Errorf("error %q does not name %q", err, tc.wantTrack)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}

	t.Run("truncated sample", func(t *testing.T) {
		f := decodeFixture(t, buildProgressiveWvtt(t, 1000, overlappingCueSamples[0]))
		f.Moov.Trak.Mdia.Minf.Stbl.Stsz.SampleSize[0] += 10000 // more data than the mdat holds
		_, err := f.ExportWebVTT(0, nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "track 1") || !strings.Contains(err.Error(), "sample 1") {
			t.Errorf("error %q does not name the track and sample", err)
		}
	})

	t.Run("zero timescale", func(t *testing.T) {
		f := decodeFixture(t, buildProgressiveWvtt(t, 1000, overlappingCueSamples[0]))
		f.Moov.Trak.Mdia.Mdhd.Timescale = 0
		_, err := f.ExportWebVTT(0, nil)
		if err == nil || !strings.Contains(err.Error(), "track 1: zero media timescale") {
			t.Errorf("got %v, want a zero timescale error naming the track", err)
		}
	})

	t.Run("no wvtt track", func(t *testing.T) {
		init := mp4.CreateEmptyInit()
		_ = init.AddEmptyTrack(1000, "video", "und")
		var buf bytes.Buffer
		if err := init.Encode(&buf); err != nil {
			t.Fatal(err)
		}
		f := decodeFixture(t, buf.Bytes())
		if _, err := f.ExportWebVTT(0, nil); !errors.Is(err, mp4.ErrNoWebVTTTrack) {
			t.Errorf("got %v, want ErrNoWebVTTTrack", err)
		}
		if _, err := f.ExportWebVTT(1, nil); err == nil ||
			!strings.Contains(err.Error(), "track 1: not a wvtt track") {
			t.Errorf("got %v, want a not-a-wvtt-track error", err)
		}
	})
}

func TestWebVTTTime(t *testing.T) {
	testCases := []struct {
		time mp4.Time
		str  string
	}{
		{0, "00:00.000"},
		{5 * mp4.TimeSecond, "00:05.000"},
		{mp4.TimeHour + 2*mp4.TimeMinute + 3*mp4.TimeSecond + 4*mp4.TimeMillisecond, "01:02:03.004"},
		{12 * mp4.TimeHour, "12:00:00.000"},
	}
	for _, tc := range testCases {
		if got := tc.time.String(); got != tc.str {
			t.Errorf("Time(%d).String() = %q, want %q", int64(tc.time), got, tc.str)
		}
		parsed, err := mp4.ParseTime(tc.str)
		if err != nil {
			t.Errorf("ParseTime(%q): %v", tc.str, err)
		} else if parsed != tc.time {
			t.Errorf("ParseTime(%q) = %d, want %d", tc.str, parsed, tc.time)
		}
	}
	for _, bad := range []string{"", "1:2.3", "00:1.000", "00:60.000", "aa:bb.ccc", "00:00.0000", "00:00.00x"} {
		if _, err := mp4.ParseTime(bad); err == nil {
			t.Errorf("ParseTime(%q) gave no error", bad)
		}
	}
}

func TestParseWebVTTSkipsNonCueBlocks(t *testing.T) {
	text := "WEBVTT\n\nNOTE a comment\n\n00:01.000 --> 00:02.000\nHello\n\n" +
		"STYLE\n::cue { color: red }\n\nplain\n00:03.000 --> 00:04.000 line:0\nBye\n"
	vtt, err := mp4.ParseWebVTT(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(vtt.Cues) != 2 {
		t.Fatalf("parsed %d cues, want 2", len(vtt.Cues))
	}
	if vtt.Cues[1].ID != "plain" || vtt.Cues[1].Settings != "line:0" || vtt.Cues[1].Text != "Bye" {
		t.Errorf("second cue %+v", vtt.Cues[1])
	}
}

// TestExportWebVTTNoModification checks that the export leaves the file as it is, so that
// other tracks and a re-encode are unaffected.
func TestExportWebVTTNoModification(t *testing.T) {
	f := decodeFixture(t, readFixture(t, "wvtt_overlap_frag.mp4"))
	var before bytes.Buffer
	if err := f.Encode(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ExportWebVTT(0, nil); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := f.Encode(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Error("export changed the file")
	}
}
