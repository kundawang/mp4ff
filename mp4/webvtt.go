package mp4

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/Eyevinn/mp4ff/bits"
)

// WebVTT track export.
//
// A wvtt track carries WebVTT cues as ISOBMFF samples: each sample is one vtte box, or one or
// more vttc boxes. A vttc box holds the cue identifier, settings and payload of one cue, while
// the cue's timing follows from the sample's presentation time and duration. Exporting such a
// track as a standalone WebVTT file therefore means mapping sample times to cue timings and
// merging consecutive samples that restate the same cue, so that a cue that continues over
// several samples becomes one cue in the output.

// Time is a WebVTT time in milliseconds, the finest precision that a WebVTT timestamp holds.
type Time int64

// WebVTT time units
const (
	TimeMillisecond Time = 1
	TimeSecond      Time = 1000 * TimeMillisecond
	TimeMinute      Time = 60 * TimeSecond
	TimeHour        Time = 60 * TimeMinute
)

// MaxCueLength is the largest cue payload, in bytes, that an export and [WebVTT.Encode] accept.
const MaxCueLength = 1 << 20

// Seconds returns the time as a number of seconds.
func (t Time) Seconds() float64 {
	return float64(t) / float64(TimeSecond)
}

// String returns the time as a WebVTT timestamp, mm:ss.mmm, or hh:mm:ss.mmm from one hour on.
func (t Time) String() string {
	sign := ""
	if t < 0 {
		sign = "-"
		t = -t
	}
	hours := int64(t / TimeHour)
	rest := t % TimeHour
	minutes := int64(rest / TimeMinute)
	rest %= TimeMinute
	seconds := int64(rest / TimeSecond)
	millis := int64(rest%TimeSecond) / int64(TimeMillisecond)
	if hours > 0 {
		return fmt.Sprintf("%s%02d:%02d:%02d.%03d", sign, hours, minutes, seconds, millis)
	}
	return fmt.Sprintf("%s%02d:%02d.%03d", sign, minutes, seconds, millis)
}

// ParseTime parses a WebVTT timestamp, mm:ss.mmm, or hh:mm:ss.mmm, into a Time. The hours part
// may have more than two digits.
func ParseTime(s string) (Time, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, fmt.Errorf("timestamp %q does not have two or three colon-separated parts", s)
	}
	secParts := strings.Split(parts[len(parts)-1], ".")
	if len(secParts) != 2 || len(secParts[1]) != 3 {
		return 0, fmt.Errorf("timestamp %q does not end with ss.mmm", s)
	}
	seconds, err := parseDecimal(secParts[0], 2)
	if err != nil {
		return 0, fmt.Errorf("timestamp %q seconds: %w", s, err)
	}
	millis, err := parseDecimal(secParts[1], 3)
	if err != nil {
		return 0, fmt.Errorf("timestamp %q milliseconds: %w", s, err)
	}
	minutes, err := parseDecimal(parts[len(parts)-2], 2)
	if err != nil {
		return 0, fmt.Errorf("timestamp %q minutes: %w", s, err)
	}
	var hours int64
	if len(parts) == 3 {
		hours, err = parseDecimal(parts[0], len(parts[0]))
		if err != nil || len(parts[0]) < 2 {
			return 0, fmt.Errorf("timestamp %q hours: expected at least two digits", s)
		}
	}
	if minutes > 59 || seconds > 59 {
		return 0, fmt.Errorf("timestamp %q has minutes or seconds above 59", s)
	}
	return Time(hours)*TimeHour + Time(minutes)*TimeMinute + Time(seconds)*TimeSecond +
		Time(millis)*TimeMillisecond, nil
}

// parseDecimal parses a string of exactly width decimal digits.
func parseDecimal(s string, width int) (int64, error) {
	if len(s) != width {
		return 0, fmt.Errorf("%q is not %d digits", s, width)
	}
	var value int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not a decimal number", s)
		}
		value = value*10 + int64(c-'0')
	}
	return value, nil
}

// Cue is one WebVTT cue, as written to a WebVTT file.
type Cue struct {
	ID       string // cue identifier, empty if the cue has none
	Start    Time   // cue start time
	End      Time   // cue end time
	Settings string // cue settings such as "line:20% align:start", empty if none
	Text     string // cue payload, with a newline between its lines
}

// WebVTT is a WebVTT file: the cues of one wvtt track, or of a parsed WebVTT text.
type WebVTT struct {
	TrackID uint32 // track ID of the exported wvtt track, 0 for parsed WebVTT text
	Cues    []Cue  // cues sorted by start time, and then by end time
}

// ErrNoWebVTTTrack is wrapped by an error when the file has no wvtt track to export.
var ErrNoWebVTTTrack = errors.New("no wvtt track")

// webVTTError prefixes the message with the track number, so that an error tells which track
// of a multi-track file it concerns.
func webVTTError(trackID uint32, format string, args ...any) error {
	return fmt.Errorf("track %d: "+format, append([]any{trackID}, args...)...)
}

// ExportWebVTT extracts the cues of a wvtt track of f as WebVTT. A trackID of 0 selects the
// first wvtt track of the file. Both progressive and fragmented files are supported.
//
// The cues come from the iden, sttg and payl boxes of each vttc box, and their times are the
// presentation times of the samples that carry them, that is the decode time plus the
// composition time offset, converted from the track timescale to milliseconds by rounding to
// the nearest millisecond. A cue that the track restates unchanged in the following samples
// becomes one cue that spans them. A vtte sample ends the cues that are still active at its
// presentation time, and a vttn sample of the experimental wvtc paint model lets them continue
// over its duration.
//
// Only the selected track is read, and neither it nor any other track is modified. A file
// decoded in DecModeLazyMdat is read through rs, which must then be the reader that the file
// was decoded from. A missing track, a malformed box, a zero timescale, presentation times
// that decrease, truncated sample data, overlapping cues with the same ID and cue payloads
// above MaxCueLength give an error that names the track.
func (f *File) ExportWebVTT(trackID uint32, rs io.ReadSeeker) (*WebVTT, error) {
	if f == nil {
		return nil, fmt.Errorf("no file")
	}
	if f.Moov == nil {
		if trackID == 0 {
			return nil, fmt.Errorf("no moov box, so no wvtt track")
		}
		return nil, fmt.Errorf("track %d: no moov box, so no such track", trackID)
	}
	trak, err := findWebVTTTrack(f.Moov, trackID)
	if err != nil {
		return nil, err
	}
	timescale := trak.Mdia.Mdhd.Timescale
	if timescale == 0 {
		return nil, webVTTError(trak.Tkhd.TrackID, "zero media timescale, cue times cannot be converted")
	}
	exporter := &webVTTExporter{trackID: trak.Tkhd.TrackID, timescale: timescale}
	switch {
	case f.IsFragmented():
		err = exporter.addFragmentedSamples(f, rs)
	case f.Mdat == nil:
		err = webVTTError(exporter.trackID, "no mdat box, so no sample data")
	default:
		err = exporter.addProgressiveSamples(f, trak, rs)
	}
	if err != nil {
		return nil, err
	}
	cues, err := exporter.cues()
	if err != nil {
		return nil, err
	}
	return &WebVTT{TrackID: exporter.trackID, Cues: cues}, nil
}

// findWebVTTTrack returns the wvtt track with trackID, or the first wvtt track when trackID is 0.
func findWebVTTTrack(moov *MoovBox, trackID uint32) (*TrakBox, error) {
	found := false
	for _, trak := range moov.Traks {
		if trak.Tkhd == nil {
			continue
		}
		if trackID != 0 && trak.Tkhd.TrackID != trackID {
			continue
		}
		found = true
		if trak.isWebVTT() {
			return trak, nil
		}
	}
	switch {
	case !found:
		return nil, fmt.Errorf("track %d: no such track", trackID)
	case trackID == 0:
		return nil, fmt.Errorf("%w: no track has a wvtt sample entry", ErrNoWebVTTTrack)
	default:
		return nil, webVTTError(trackID, "not a wvtt track")
	}
}

// isWebVTT reports whether the track has a wvtt or wvtc sample description.
func (t *TrakBox) isWebVTT() bool {
	if t.Mdia == nil || t.Mdia.Mdhd == nil || t.Mdia.Minf == nil || t.Mdia.Minf.Stbl == nil ||
		t.Mdia.Minf.Stbl.Stsd == nil {
		return false
	}
	stsd := t.Mdia.Minf.Stbl.Stsd
	return stsd.Wvtt != nil || stsd.Wvtc != nil
}

// sampleCue is one cue read from a vttc box, with times in the track timescale.
type sampleCue struct {
	id       string
	settings string
	text     string
	start    int64 // presentation time of the sample
	end      int64 // presentation time plus the sample duration
}

// sampleMark is either a cue of a sample, or a vtte or vttn marker that applies to the active cues.
type sampleMark struct {
	cue      sampleCue
	vtte     bool   // a vtte sample, which ends the active cues
	vttn     bool   // a vttn sample, which lets the active cues continue over its duration
	time     int64  // start of the sample
	end      int64  // end of the sample
	sampleNr uint32 // 1-based number of the sample the mark comes from
}

// webVTTExporter collects the sample cues of one track and merges them into cues.
type webVTTExporter struct {
	trackID   uint32
	timescale uint32
	sampleNr  uint32 // number of samples read so far
	marks     []sampleMark
	lastStart int64
	started   bool
}

// addProgressiveSamples reads every sample of trak from the mdat box of a progressive file.
func (e *webVTTExporter) addProgressiveSamples(f *File, trak *TrakBox, rs io.ReadSeeker) error {
	stbl := trak.Mdia.Minf.Stbl
	if stbl.Stts == nil || stbl.Stsz == nil {
		return webVTTError(e.trackID, "track has no stts or stsz box")
	}
	if stbl.Stsc == nil || (stbl.Stco == nil && stbl.Co64 == nil) {
		return webVTTError(e.trackID, "track has no stsc, stco or co64 box")
	}
	if err := trak.CheckDataIsSelfContained(); err != nil {
		return webVTTError(e.trackID, "%w", err)
	}
	nrSamples := stbl.Stsz.GetNrSamples()
	if nrSamples == 0 {
		return nil
	}
	durs, err := stbl.Stts.SampleDurations(nrSamples)
	if err != nil {
		return webVTTError(e.trackID, "sample durations: %w", err)
	}
	var ctos []int32
	if stbl.Ctts != nil {
		if ctos, err = stbl.Ctts.CompositionTimeOffsets(nrSamples); err != nil {
			return webVTTError(e.trackID, "composition time offsets: %w", err)
		}
	}
	chunks, err := stbl.GetContainingChunks(1, nrSamples)
	if err != nil {
		return webVTTError(e.trackID, "chunks: %w", err)
	}
	var decodeTime uint64
	var sampleNr uint32 = 1
	for _, chunk := range chunks {
		if sampleNr > nrSamples {
			break
		}
		offset, err := chunkOffset(stbl, int(chunk.ChunkNr))
		if err != nil {
			return webVTTError(e.trackID, "chunk %d: %w", chunk.ChunkNr, err)
		}
		// The entries of stsc give the nominal samples per chunk, so the last chunk may
		// declare more than the stsz sample count leaves in it.
		nrInChunk := min(chunk.NrSamples, nrSamples-sampleNr+1)
		for i := uint32(0); i < nrInChunk; i++ {
			size := stbl.Stsz.GetSampleSize(int(sampleNr))
			data, err := f.Mdat.ReadData(int64(offset), int64(size), rs)
			if err != nil {
				return webVTTError(e.trackID, "sample %d (chunk %d): %w", sampleNr, chunk.ChunkNr, err)
			}
			var cto int32
			if ctos != nil {
				cto = ctos[sampleNr-1]
			}
			if err := e.addSample(data, int64(decodeTime)+int64(cto), int64(durs[sampleNr-1])); err != nil {
				return err
			}
			decodeTime += uint64(durs[sampleNr-1])
			offset += uint64(size)
			sampleNr++
		}
	}
	if sampleNr-1 != nrSamples {
		return webVTTError(e.trackID, "chunks hold %d samples, not the %d declared", sampleNr-1, nrSamples)
	}
	return nil
}

// chunkOffset returns the file offset of the 1-based chunkNr.
func chunkOffset(stbl *StblBox, chunkNr int) (uint64, error) {
	switch {
	case stbl.Stco != nil:
		return stbl.Stco.GetOffset(chunkNr)
	case stbl.Co64 != nil:
		return stbl.Co64.GetOffset(chunkNr)
	}
	return 0, fmt.Errorf("neither stco nor co64 available")
}

// addFragmentedSamples reads every sample of the track from the fragments of a fragmented file.
// The trun boxes are read as decoded, and the tfhd and trex defaults fill in the sample duration
// and size of samples that the trun does not carry them for.
func (e *webVTTExporter) addFragmentedSamples(f *File, rs io.ReadSeeker) error {
	var trex *TrexBox
	if f.Moov.Mvex != nil {
		trex, _ = f.Moov.Mvex.GetTrex(e.trackID)
	}
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			if frag.Moof == nil || frag.Mdat == nil {
				continue
			}
			traf := trafForTrackID(frag.Moof, e.trackID)
			if traf == nil {
				continue
			}
			if frag.Mdat.IsLazy() && rs == nil {
				return webVTTError(e.trackID, "lazily decoded file needs a ReadSeeker for the sample data")
			}
			baseTime := uint64(0)
			if traf.Tfdt != nil {
				baseTime = traf.Tfdt.BaseMediaDecodeTime()
			}
			if baseTime == 0 && f.Mfra != nil {
				// A fragment without a tfdt, or with a zero one, starts at zero in an ISM file.
				// The tfra entry that points to its moof then gives the absolute decode time.
				if entry := f.Mfra.FindEntry(frag.Moof.StartPos, e.trackID); entry != nil &&
					entry.TrafNumber <= 1 && entry.TrunNumber <= 1 && entry.SampleNumber <= 1 {
					baseTime = entry.Time
				}
			}
			defaultDur, defaultSize := defaultSampleDuration(traf.Tfhd, trex), defaultSampleSize(traf.Tfhd, trex)
			walk := frag.trunWalk(traf)
			for _, trun := range traf.Truns {
				var offsetInMdat uint64
				if walk != nil {
					offsetInMdat = walk.offset(frag, trun)
				} else {
					var err error
					if offsetInMdat, err = frag.trunOffsetInMdat(traf.Tfhd, trun); err != nil {
						return webVTTError(e.trackID, "moof at %d: %w", frag.Moof.StartPos, err)
					}
				}
				for i := range trun.Samples {
					s := &trun.Samples[i]
					dur, ok := sampleValue(trun.HasSampleDuration(), s.Dur, defaultDur)
					if !ok {
						return webVTTError(e.trackID, "sample %d: neither the trun, tfhd nor trex box "+
							"gives its duration", e.sampleNr+1)
					}
					size, ok := sampleValue(trun.HasSampleSize(), s.Size, defaultSize)
					if !ok {
						return webVTTError(e.trackID, "sample %d: neither the trun, tfhd nor trex box "+
							"gives its size", e.sampleNr+1)
					}
					cts := int64(baseTime) + int64(s.CompositionTimeOffset)
					data, err := e.readSampleData(frag.Mdat, rs, offsetInMdat, size)
					if err != nil {
						return err
					}
					if err := e.addSample(data, cts, int64(dur)); err != nil {
						return err
					}
					baseTime += uint64(dur)
					offsetInMdat += uint64(size)
				}
			}
		}
	}
	return nil
}

// sampleValue returns the value that a trun carries for a sample, or the tfhd or trex default.
// ok is false when no box gives a duration or size for the sample.
func sampleValue(inTrun bool, value, defaultValue uint32) (uint32, bool) {
	switch {
	case inTrun:
		return value, true
	case defaultValue != 0:
		return defaultValue, true
	}
	return 0, false
}

// defaultSampleDuration is the tfhd default sample duration, falling back to the trex one.
func defaultSampleDuration(tfhd *TfhdBox, trex *TrexBox) uint32 {
	switch {
	case tfhd != nil && tfhd.HasDefaultSampleDuration():
		return tfhd.DefaultSampleDuration
	case trex != nil:
		return trex.DefaultSampleDuration
	}
	return 0
}

// defaultSampleSize is the tfhd default sample size, falling back to the trex one.
func defaultSampleSize(tfhd *TfhdBox, trex *TrexBox) uint32 {
	switch {
	case tfhd != nil && tfhd.HasDefaultSampleSize():
		return tfhd.DefaultSampleSize
	case trex != nil:
		return trex.DefaultSampleSize
	}
	return 0
}

// readSampleData returns the data of one sample, read from memory or, for a lazy mdat, via rs.
func (e *webVTTExporter) readSampleData(mdat *MdatBox, rs io.ReadSeeker, offsetInMdat uint64, size uint32) ([]byte, error) {
	offset := mdat.PayloadAbsoluteOffset() + offsetInMdat
	data, err := mdat.ReadData(int64(offset), int64(size), rs)
	if err != nil {
		return nil, webVTTError(e.trackID, "sample %d: %w", e.sampleNr+1, err)
	}
	return data, nil
}

// addSample parses the boxes of one sample and records its cues. cts is the sample presentation
// time and dur its duration, both in the track timescale.
func (e *webVTTExporter) addSample(data []byte, cts, dur int64) error {
	sampleNr := e.sampleNr + 1
	if len(data) == 0 {
		return webVTTError(e.trackID, "sample %d is empty", sampleNr)
	}
	if dur < 0 {
		return webVTTError(e.trackID, "sample %d has negative duration %d", sampleNr, dur)
	}
	if e.started && cts < e.lastStart {
		return webVTTError(e.trackID, "sample %d has presentation time %d before the previous sample's %d",
			sampleNr, cts, e.lastStart)
	}
	boxes, err := decodeWebVTTSampleBoxes(data)
	if err != nil {
		return webVTTError(e.trackID, "sample %d: %w", sampleNr, err)
	}
	if len(boxes) == 0 {
		return webVTTError(e.trackID, "sample %d has no vttc, vttn or vtte box", sampleNr)
	}
	hasVtte := false
	hasVttn := false
	nrCues := 0
	for _, box := range boxes {
		switch b := box.(type) {
		case *VtteBox:
			hasVtte = true
		case *VttnBox:
			hasVttn = true
		case *VttcBox:
			nrCues++
			if b.Payl == nil {
				return webVTTError(e.trackID, "sample %d: vttc box has no payl box", sampleNr)
			}
			sc := sampleCue{text: b.Payl.CueText, start: cts, end: cts + dur}
			if b.Iden != nil {
				sc.id = b.Iden.CueID
			}
			if b.Sttg != nil {
				sc.settings = b.Sttg.Settings
			}
			e.marks = append(e.marks, sampleMark{cue: sc, time: cts, sampleNr: sampleNr})
		default:
			return webVTTError(e.trackID, "sample %d has a %s box, not a vttc, vttn or vtte box",
				sampleNr, box.Type())
		}
	}
	if hasVtte {
		if nrCues > 0 || len(boxes) > 1 {
			return webVTTError(e.trackID, "sample %d mixes a vtte box with other boxes", sampleNr)
		}
		e.marks = append(e.marks, sampleMark{vtte: true, time: cts, end: cts + dur, sampleNr: sampleNr})
	}
	if hasVttn {
		if nrCues > 0 || len(boxes) > 1 {
			return webVTTError(e.trackID, "sample %d mixes a vttn box with other boxes", sampleNr)
		}
		e.marks = append(e.marks, sampleMark{vttn: true, time: cts, end: cts + dur, sampleNr: sampleNr})
	}
	e.started = true
	e.lastStart = cts
	e.sampleNr++
	return nil
}

// decodeWebVTTSampleBoxes decodes the top-level boxes of a wvtt sample.
func decodeWebVTTSampleBoxes(data []byte) ([]Box, error) {
	sr := bits.NewFixedSliceReader(data)
	var boxes []Box
	for sr.NrRemainingBytes() > 0 {
		startPos := sr.GetPos()
		box, err := DecodeBoxSR(0, sr)
		if err != nil {
			return nil, fmt.Errorf("decode box at byte %d: %w", startPos, err)
		}
		if sr.AccError() != nil {
			return nil, fmt.Errorf("decode box at byte %d: %w", startPos, sr.AccError())
		}
		if box.Size() == 0 {
			return nil, fmt.Errorf("box %s at byte %d has zero size", box.Type(), startPos)
		}
		boxes = append(boxes, box)
	}
	return boxes, nil
}

// cues converts the sample cues to WebVTT times, applies the vtte and vttn sample semantics and
// merges cues that continue over consecutive samples.
func (e *webVTTExporter) cues() ([]Cue, error) {
	cues := make([]Cue, 0, len(e.marks))
	// active maps a cue identity to the last cue with that identity and the sample it came
	// from, so that a restated cue continues that cue rather than starting a new one.
	type activeCue struct {
		idx      int
		sampleNr uint32
	}
	active := make(map[string]activeCue)
	for i := range e.marks {
		mark := &e.marks[i]
		start, err := e.toMillis(mark.time)
		if err != nil {
			return nil, webVTTError(e.trackID, "%w", err)
		}
		if mark.vtte {
			// A vtte sample clears the screen, so the active cues end where it starts.
			for _, ac := range active {
				if cues[ac.idx].End > start {
					cues[ac.idx].End = start
				}
			}
			clear(active)
			continue
		}
		if mark.vttn {
			// A vttn sample changes nothing, so the active cues stay and cover its duration.
			end, err := e.toMillis(mark.end)
			if err != nil {
				return nil, webVTTError(e.trackID, "sample ending at %d in timescale %d: %w",
					mark.end, e.timescale, err)
			}
			for _, ac := range active {
				if cues[ac.idx].End >= start && cues[ac.idx].End < end {
					cues[ac.idx].End = end
				}
			}
			continue
		}
		sc := &mark.cue
		end, err := e.toMillis(sc.end)
		if err != nil {
			return nil, webVTTError(e.trackID, "cue ending at %d in timescale %d: %w",
				sc.end, e.timescale, err)
		}
		if end < start {
			return nil, webVTTError(e.trackID, "cue %q ends at %s before it starts at %s", sc.id, end, start)
		}
		if len(sc.text) > MaxCueLength {
			return nil, webVTTError(e.trackID, "cue %q has %d bytes of text, more than the %d allowed",
				sc.id, len(sc.text), MaxCueLength)
		}
		key := cueIdentity(sc.id, sc.settings, sc.text)
		// A cue that the following samples restate continues the earlier cue. A cue that
		// starts in the same sample is a cue of its own, even with the same content, and so
		// is a cue that restarts after the earlier cue ended.
		if ac, ok := active[key]; ok && ac.sampleNr < mark.sampleNr && cues[ac.idx].End >= start {
			if end > cues[ac.idx].End {
				cues[ac.idx].End = end
			}
			continue
		}
		cues = append(cues, Cue{ID: sc.id, Start: start, End: end, Settings: sc.settings, Text: sc.text})
		active[key] = activeCue{idx: len(cues) - 1, sampleNr: mark.sampleNr}
	}
	sort.SliceStable(cues, func(i, j int) bool {
		if cues[i].Start != cues[j].Start {
			return cues[i].Start < cues[j].Start
		}
		return cues[i].End < cues[j].End
	})
	for i := range cues {
		if err := cues[i].validate(); err != nil {
			return nil, webVTTError(e.trackID, "cue %d: %w", i+1, err)
		}
	}
	if err := checkCueIDs(cues); err != nil {
		return nil, webVTTError(e.trackID, "%w", err)
	}
	return cues, nil
}

// cueIdentity is the key that tells whether two samples restate the same cue.
func cueIdentity(id, settings, text string) string {
	return id + "\x00" + settings + "\x00" + text
}

// toMillis converts a time in the track timescale to milliseconds, rounded to the nearest
// millisecond. A time too large for a Time gives an error.
func (e *webVTTExporter) toMillis(t int64) (Time, error) {
	const maxTicks = int64(math.MaxInt64) / int64(TimeSecond) // largest t that times 1000 fits
	ts := int64(e.timescale)
	if t > maxTicks || t < -maxTicks {
		return 0, fmt.Errorf("time %d in timescale %d is too large", t, e.timescale)
	}
	if t >= 0 {
		return Time((t*int64(TimeSecond) + ts/2) / ts), nil
	}
	return Time(-((-t*int64(TimeSecond) + ts/2) / ts)), nil
}

// checkCueIDs rejects overlapping cues that share a cue identifier.
func checkCueIDs(cues []Cue) error {
	byID := make(map[string][]int, len(cues))
	for i, cue := range cues {
		if cue.ID == "" {
			continue
		}
		for _, prev := range byID[cue.ID] {
			if cues[prev].End > cue.Start && cue.End > cues[prev].Start {
				return fmt.Errorf("cue ID %q is used by cue %d and cue %d, which overlap", cue.ID, prev+1, i+1)
			}
		}
		byID[cue.ID] = append(byID[cue.ID], i)
	}
	return nil
}

// validate checks that the cue can be written as WebVTT text and read back unchanged.
func (c Cue) validate() error {
	switch {
	case c.End < c.Start:
		return fmt.Errorf("cue %q ends before it starts", c.ID)
	case strings.ContainsRune(c.Text, 0) || strings.Contains(c.Text, "\r"):
		return fmt.Errorf("cue %q has a NUL or carriage return in its text", c.ID)
	case len(c.Text) > MaxCueLength:
		return fmt.Errorf("cue %q has %d bytes of text, more than the %d allowed", c.ID, len(c.Text), MaxCueLength)
	case strings.Contains(c.Text, "\n\n"):
		return fmt.Errorf("cue %q has a blank line in its text, which WebVTT cannot hold", c.ID)
	case strings.ContainsAny(c.ID, "\r\n"):
		return fmt.Errorf("cue ID %q contains a line break", c.ID)
	case strings.Contains(c.ID, "-->"):
		return fmt.Errorf("cue ID %q contains -->", c.ID)
	case isVTTBlockKeyword(c.ID):
		return fmt.Errorf("cue ID %q starts with a WebVTT block keyword", c.ID)
	case strings.ContainsAny(c.Settings, "\r\n"):
		return fmt.Errorf("cue %q has a line break in its settings", c.ID)
	}
	return nil
}

// isVTTBlockKeyword reports whether id would make the cue block a NOTE, STYLE or REGION block.
func isVTTBlockKeyword(id string) bool {
	first, _, _ := strings.Cut(id, " ")
	switch first {
	case "NOTE", "STYLE", "REGION":
		return true
	}
	return false
}

// Encode writes the cues as a WebVTT file: the WEBVTT signature, a blank line, and then each
// cue with its identifier on a line of its own, the timing line with the settings, and the
// payload, separated by blank lines.
func (w *WebVTT) Encode(wr io.Writer) error {
	var buf bytes.Buffer
	buf.WriteString("WEBVTT\n\n")
	for i := range w.Cues {
		cue := &w.Cues[i]
		if err := cue.validate(); err != nil {
			return fmt.Errorf("cue %d: %w", i+1, err)
		}
		if cue.ID != "" {
			buf.WriteString(cue.ID)
			buf.WriteByte('\n')
		}
		buf.WriteString(cue.Start.String())
		buf.WriteString(" --> ")
		buf.WriteString(cue.End.String())
		if cue.Settings != "" {
			buf.WriteByte(' ')
			buf.WriteString(cue.Settings)
		}
		buf.WriteString("\n")
		if text := strings.TrimSuffix(cue.Text, "\n"); text != "" {
			buf.WriteString(text)
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}
	_, err := wr.Write(buf.Bytes())
	return err
}

// ParseWebVTT parses a WebVTT file into cues. It accepts an optional byte order mark, the
// WEBVTT signature, and then cue blocks with an optional cue identifier, a timing line with
// optional settings and a payload. It skips NOTE, STYLE and REGION blocks. It is the
// counterpart of [WebVTT.Encode].
func ParseWebVTT(r io.Reader) (*WebVTT, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "WEBVTT") {
		return nil, fmt.Errorf("no WEBVTT header")
	}
	vtt := WebVTT{}
	for nr, block := range splitVTTBlocks(lines[1:]) {
		switch {
		case strings.HasPrefix(block[0], "NOTE"), strings.HasPrefix(block[0], "STYLE"),
			strings.HasPrefix(block[0], "REGION"):
			continue
		case strings.HasPrefix(block[0], "X-TIMESTAMP-MAP"):
			continue
		}
		id := ""
		timingIdx := 0
		if !strings.Contains(block[0], "-->") {
			id = block[0]
			timingIdx = 1
		}
		if timingIdx >= len(block) {
			return nil, fmt.Errorf("cue %d has no timing line", nr+1)
		}
		cue, err := parseVTTTimingLine(block[timingIdx])
		if err != nil {
			return nil, fmt.Errorf("cue %d: %w", nr+1, err)
		}
		cue.ID = id
		if timingIdx+1 < len(block) {
			cue.Text = strings.Join(block[timingIdx+1:], "\n")
		}
		vtt.Cues = append(vtt.Cues, cue)
	}
	return &vtt, nil
}

// splitVTTBlocks splits the lines after the WEBVTT header into blocks separated by blank lines.
func splitVTTBlocks(lines []string) [][]string {
	var blocks [][]string
	var block []string
	for _, line := range lines {
		if line == "" {
			if len(block) > 0 {
				blocks = append(blocks, block)
				block = nil
			}
			continue
		}
		block = append(block, line)
	}
	if len(block) > 0 {
		blocks = append(blocks, block)
	}
	return blocks
}

// parseVTTTimingLine parses "start --> end [settings]".
func parseVTTTimingLine(line string) (Cue, error) {
	startStr, rest, ok := strings.Cut(line, "-->")
	if !ok {
		return Cue{}, fmt.Errorf("timing line %q has no -->", line)
	}
	start, err := ParseTime(strings.TrimSpace(startStr))
	if err != nil {
		return Cue{}, err
	}
	rest = strings.TrimSpace(rest)
	endStr, settings, _ := strings.Cut(rest, " ")
	end, err := ParseTime(endStr)
	if err != nil {
		return Cue{}, err
	}
	if end < start {
		return Cue{}, fmt.Errorf("cue ends at %s before it starts at %s", end, start)
	}
	return Cue{Start: start, End: end, Settings: strings.TrimSpace(settings)}, nil
}
