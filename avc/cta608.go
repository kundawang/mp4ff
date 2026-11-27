package avc

import (
	"fmt"

	"github.com/Eyevinn/mp4ff/sei"
)

// CTA-608 caption fields, as carried by the cc_type bits of a cc_data() entry
// (ANSI/CTA-708-E Section 4.3). cc_type 0 is field 1 and cc_type 1 is field 2.
const (
	CTA608Field1 = 1
	CTA608Field2 = 2
)

// CTA608Pair is one CTA-608 closed-caption byte pair from an AVC SEI user_data_registered_itu_t_t35
// message. Field is CTA608Field1 or CTA608Field2, the field the pair belongs to. Data1 and Data2 are
// the two cc_data bytes, with their odd-parity bits kept, as ParseCTA608 returns them. The bytes are
// not decoded into caption characters.
type CTA608Pair struct {
	Field int
	Data1 byte
	Data2 byte
}

// ExtractCTA608Pairs scans the length-prefixed NAL units of an AVC sample and returns the valid
// CTA-608 byte pairs of its SEI NAL units, in the order they appear in the sample. AVC SEI carries
// CTA-608 in a user_data_registered_itu_t_t35 (type 4) message identified by the ATSC A/53 GA94
// data of sei.CTA608ITUData; other SEI messages and NAL units that are not SEI are ignored.
//
// The pairs of one cc_data() keep their order, and the two pairs present at the same time are
// returned as consecutive entries. An error is returned for a truncated NAL unit or a malformed
// SEI message, including an invalid CTA-608 cc_data() payload, instead of dropping the data
// silently. The sample is read only and is not modified.
func ExtractCTA608Pairs(sample []byte) ([]CTA608Pair, error) {
	if len(sample) == 0 {
		return nil, nil
	}
	nalus, err := GetNalusFromSample(sample)
	if err != nil {
		return nil, fmt.Errorf("getting NAL units from sample: %w", err)
	}
	var pairs []CTA608Pair
	for i, nalu := range nalus {
		if len(nalu) == 0 || GetNaluType(nalu[0]) != NALU_SEI {
			continue
		}
		msgs, err := ParseSEINalu(nalu, nil)
		if err != nil {
			return nil, fmt.Errorf("NAL unit %d: parsing SEI: %w", i, err)
		}
		for _, msg := range msgs {
			if msg.Type() != sei.SEIUserDataRegisteredITUtT35Type {
				continue
			}
			cta, ok := msg.(*sei.CTA608sei)
			if !ok {
				continue // registered user data that is not CTA-608, such as another country code
			}
			pairs = appendCTA608FieldPairs(pairs, CTA608Field1, cta.Field1)
			pairs = appendCTA608FieldPairs(pairs, CTA608Field2, cta.Field2)
		}
	}
	return pairs, nil
}

// appendCTA608FieldPairs appends the byte pairs of one CTA-608 field, two bytes per pair.
// ParseCTA608 only appends complete, valid non-empty pairs, so the field length is even.
func appendCTA608FieldPairs(pairs []CTA608Pair, field int, data []byte) []CTA608Pair {
	for i := 0; i+1 < len(data); i += 2 {
		pairs = append(pairs, CTA608Pair{
			Field: field,
			Data1: data[i],
			Data2: data[i+1],
		})
	}
	return pairs
}
