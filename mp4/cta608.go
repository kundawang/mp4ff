package mp4

import (
	"fmt"

	"github.com/Eyevinn/mp4ff/avc"
)

// CTA608Event is one CTA-608 closed-caption byte pair and the time at which it is presented.
// The bytes are the raw cc_data bytes, with their odd-parity bits kept; they are not decoded
// into caption characters.
type CTA608Event struct {
	TrackID          uint32 // Track whose AVC sample carries the pair
	Field            int    // CTA-608 field, avc.CTA608Field1 or avc.CTA608Field2
	Data1            byte   // First byte of the pair, parity bit kept
	Data2            byte   // Second byte of the pair, parity bit kept
	PresentationTime int64  // Presentation time in the track timescale (mdhd)
}

// AppendCTA608Events appends the CTA-608 closed-caption events of the AVC track of one fragment to
// dst and returns the extended slice. It is the fragment-level part of the CTA-608 caption timeline.
//
// The track is selected by trex.TrackID, or by the first traf if trex is nil, as for
// Fragment.AppendFullSamples. Its samples are walked in trun order. For each sample, the valid
// CTA-608 byte pairs of its SEI NAL units are extracted with avc.ExtractCTA608Pairs and stamped
// with the sample presentation time, computed from the tfdt base media decode time, the sample
// durations and the signed composition time offset. Pairs present at the same time stay in the
// order in which their SEI messages carry them. SEI messages that are not CTA-608 are ignored.
//
// The fragment data are only read. An error is returned, with dst truncated to its original
// length, when the sample data do not fit the mdat, a NAL unit or SEI message is truncated, or a
// CTA-608 payload is invalid.
func (f *Fragment) AppendCTA608Events(dst []CTA608Event, trex *TrexBox) ([]CTA608Event, error) {
	origLen := len(dst)
	traf := f.sampleTraf(trex)
	if traf == nil {
		return dst, nil // This track is not present in this fragment
	}
	trackID := traf.Tfhd.TrackID
	for sample, err := range f.Samples(trex) {
		if err != nil {
			return dst[:origLen], err
		}
		pairs, err := avc.ExtractCTA608Pairs(sample.Data)
		if err != nil {
			return dst[:origLen], fmt.Errorf("CTA-608 extraction in track %d: %w", trackID, err)
		}
		pts := sample.PresentationTime()
		for _, pair := range pairs {
			dst = append(dst, CTA608Event{
				TrackID:          trackID,
				Field:            pair.Field,
				Data1:            pair.Data1,
				Data2:            pair.Data2,
				PresentationTime: pts,
			})
		}
	}
	return dst, nil
}

// CTA608Events returns the CTA-608 closed-caption timeline of one AVC video track across all
// fragments of a fragmented file. The track is selected by trackID, which must be a video track
// with an avc1 or avc3 sample entry. The events are returned in fragment and sample order, with
// the presentation times that AppendCTA608Events computes. A track that a fragment does not
// contain contributes nothing from that fragment.
//
// The file and its media samples are only read. An error is returned for a track that is missing
// or not AVC, or from the first malformed fragment, truncated NAL unit or invalid CTA-608 payload.
func (f *File) CTA608Events(trackID uint32) ([]CTA608Event, error) {
	if f.Init == nil || f.Init.Moov == nil {
		return nil, fmt.Errorf("no init segment with track metadata")
	}
	trak, err := f.Init.Moov.findAVCTrak(trackID)
	if err != nil {
		return nil, err
	}
	trackID = trak.Tkhd.TrackID
	var trex *TrexBox
	if f.Init.Moov.Mvex != nil {
		trex, _ = f.Init.Moov.Mvex.GetTrex(trackID)
	}
	events := make([]CTA608Event, 0)
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			events, err = frag.AppendCTA608Events(events, trex)
			if err != nil {
				return nil, err
			}
		}
	}
	return events, nil
}

// findAVCTrak returns the track of trackID whose sample entry is AVC (avc1 or avc3).
func (m *MoovBox) findAVCTrak(trackID uint32) (*TrakBox, error) {
	for _, trak := range m.Traks {
		if trak == nil || trak.Tkhd == nil || trak.Tkhd.TrackID != trackID {
			continue
		}
		var stsd *StsdBox
		if trak.Mdia != nil && trak.Mdia.Minf != nil && trak.Mdia.Minf.Stbl != nil {
			stsd = trak.Mdia.Minf.Stbl.Stsd
		}
		if stsd != nil && stsd.AvcX != nil {
			return trak, nil
		}
		return nil, fmt.Errorf("track %d is not an AVC (avc1/avc3) video track", trackID)
	}
	return nil, fmt.Errorf("no track with trackID=%d", trackID)
}
