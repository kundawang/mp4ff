package mp4_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// st3d box with stereo_mode 1 (top-bottom), as an FFmpeg-written file lays it out.
const st3dTopBottomHex = "0000000d737433640000000001"

// prhd box with a zero pose, 24 bytes like movenc.c writes.
const prhdZeroPoseHex = "000000187072686400000000000000000000000000000000"

// equi box with all four projection bounds zero, 28 bytes like movenc.c writes.
const equiFullProjectionHex = "0000001c657175690000000000000000000000000000000000000000"

func decodeHexBox(t *testing.T, h string) mp4.Box {
	t.Helper()
	data, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	box, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func TestSt3DSpecByteLayout(t *testing.T) {
	st3d := decodeHexBox(t, st3dTopBottomHex).(*mp4.Stereo3DBox)
	if st3d.Version != 0 || st3d.Flags != 0 {
		t.Errorf("version %d flags %#x, want 0/0", st3d.Version, st3d.Flags)
	}
	if st3d.StereoMode != mp4.StereoModeTopBottom {
		t.Errorf("stereoMode %d, want %d", st3d.StereoMode, mp4.StereoModeTopBottom)
	}
	if st3d.StereoModeName() != "top-bottom" {
		t.Errorf("stereo mode name %q", st3d.StereoModeName())
	}
	if st3d.Size() != 13 {
		t.Errorf("size %d, want 13", st3d.Size())
	}
	// Both encode paths must reproduce the canonical bytes.
	cmpAfterDecodeEncodeBox(t, mustHex(t, st3dTopBottomHex))
	boxDiffAfterEncodeAndDecode(t, st3d)
}

func TestSt3DStereoModes(t *testing.T) {
	names := map[byte]string{
		mp4.StereoModeMonoscopic:   "monoscopic",
		mp4.StereoModeTopBottom:    "top-bottom",
		mp4.StereoModeLeftRight:    "left-right",
		mp4.StereoModeStereoCustom: "stereo-custom",
		mp4.StereoModeRightLeft:    "right-left",
	}
	for mode, name := range names {
		st3d := mp4.NewStereo3DBox(mode)
		if st3d.StereoModeName() != name {
			t.Errorf("mode %d: name %q, want %q", mode, st3d.StereoModeName(), name)
		}
		boxDiffAfterEncodeAndDecode(t, st3d)
	}
}

func TestSt3DIllegalModeAndVersion(t *testing.T) {
	// stereo_mode 5 is not defined by the RFC.
	illegalMode := mustHex(t, "0000000d737433640000000005")
	for _, decode := range []func() (mp4.Box, error){
		func() (mp4.Box, error) { return mp4.DecodeBox(0, bytes.NewReader(illegalMode)) },
		func() (mp4.Box, error) { return mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(illegalMode)) },
	} {
		if _, err := decode(); err == nil || !strings.Contains(err.Error(), "stereo_mode") {
			t.Fatalf("illegal stereo_mode gave error %v", err)
		}
	}
	// Version 1 is not defined.
	illegalVersion := mustHex(t, "0000000d737433640100000000")
	if _, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(illegalVersion)); err == nil ||
		!strings.Contains(err.Error(), "version 0") {
		t.Fatalf("version 1 gave error %v", err)
	}
	if err := mp4.NewStereo3DBox(mp4.StereoModeTopBottom).Encode(&bytes.Buffer{}); err != nil {
		t.Errorf("valid st3d should encode: %v", err)
	}
	// prhd and svhd reject a non-zero version on encode as well.
	badPrhd := mp4.NewProjectionHeaderBox(0, 0, 0)
	badPrhd.Version = 1
	if err := badPrhd.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "prhd") {
		t.Fatalf("prhd version 1 gave error %v", err)
	}
	badSvhd := mp4.NewSphericalVideoHeaderBox("tool")
	badSvhd.Flags = 1
	if err := badSvhd.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "svhd") {
		t.Fatalf("svhd flags 1 gave error %v", err)
	}
}

func TestPrhdSpecByteLayoutAndPose(t *testing.T) {
	prhd := decodeHexBox(t, prhdZeroPoseHex).(*mp4.ProjectionHeaderBox)
	if prhd.Size() != 24 {
		t.Errorf("size %d, want 24", prhd.Size())
	}
	if prhd.Yaw() != 0 || prhd.Pitch() != 0 || prhd.Roll() != 0 {
		t.Error("zero pose did not decode as zero")
	}
	cmpAfterDecodeEncodeBox(t, mustHex(t, prhdZeroPoseHex))

	// One degree is 1<<16 in 16.16 fixed point.
	prhd = mp4.NewProjectionHeaderBox(1<<16, -(90 << 16), -(180 << 16))
	boxDiffAfterEncodeAndDecode(t, prhd)
	if prhd.Yaw() != 1.0 || prhd.Pitch() != -90.0 || prhd.Roll() != -180.0 {
		t.Errorf("poses %v %v %v", prhd.Yaw(), prhd.Pitch(), prhd.Roll())
	}
}

func TestEquiSpecByteLayout(t *testing.T) {
	equi := decodeHexBox(t, equiFullProjectionHex).(*mp4.EquirectangularProjectionBox)
	if equi.Size() != 28 {
		t.Errorf("size %d, want 28", equi.Size())
	}
	if !equi.IsFullProjection() {
		t.Error("zero bounds should be a full projection")
	}
	cmpAfterDecodeEncodeBox(t, mustHex(t, equiFullProjectionHex))
	boxDiffAfterEncodeAndDecode(t, equi)

	// 0x80000000 is the 0.32 fixed-point value 0.5 on each cropped edge.
	cropped := mp4.NewEquirectangularProjectionBox(0x80000000, 0, 0x80000000, 0)
	boxDiffAfterEncodeAndDecode(t, cropped)
	if cropped.TopFraction() != 0.5 || cropped.LeftFraction() != 0.5 {
		t.Errorf("fractions %v %v, want 0.5/0.5", cropped.TopFraction(), cropped.LeftFraction())
	}
	if cropped.IsFullProjection() {
		t.Error("non-zero bounds should not be a full projection")
	}
}

func TestEquiIllegalBounds(t *testing.T) {
	// The two crops leave no frame row: top + bottom == 0xFFFFFFFF.
	noRows := mp4.NewEquirectangularProjectionBox(0x80000000, 0x7fffffff, 0, 0)
	err := noRows.Encode(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "top and bottom crops") {
		t.Fatalf("top+bottom == MaxUint32 gave error %v", err)
	}
	noCols := mp4.NewEquirectangularProjectionBox(0, 0, 0x80000000, 0x7fffffff)
	err = noCols.Encode(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "left and right crops") {
		t.Fatalf("left+right == MaxUint32 gave error %v", err)
	}
	// The same malformed values must be rejected on decode from both paths. The box
	// cannot be encoded through Encode, so build its 28 bytes manually.
	raw := mustHex(t, "0000001c6571756900000000"+
		"80000000"+ // top 0x80000000
		"7fffffff"+ // bottom 0x7fffffff
		"00000000"+ // left
		"00000000") // right
	if _, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(raw)); err == nil {
		t.Fatal("decoding malformed equi should fail")
	}
	if _, err := mp4.DecodeBox(0, bytes.NewReader(raw)); err == nil {
		t.Fatal("io decoding malformed equi should fail")
	}
}

// newSphericalSampleEntry builds an avc1 entry carrying st3d and an sv3d hierarchy
// with the given stereo mode, pose and equirectangular crop bounds.
func newSphericalSampleEntry(t *testing.T, stereoMode byte, yaw, pitch, roll int32,
	top, bottom, left, right uint32) *mp4.VisualSampleEntryBox {
	t.Helper()
	avc1 := mp4.CreateVisualSampleEntryBox("avc1", 2048, 1024, nil)
	avc1.AddChild(mp4.NewStereo3DBox(stereoMode))
	sv3d, err := mp4.NewSphericalVideoBox("mp4ff test",
		mp4.NewProjectionHeaderBox(yaw, pitch, roll),
		mp4.NewEquirectangularProjectionBox(top, bottom, left, right))
	if err != nil {
		t.Fatal(err)
	}
	avc1.AddChild(sv3d)
	return avc1
}

// TestSphericalSampleEntryRoundTrip covers the user-facing case: top-bottom stereo
// (mode 1), zero pose and zero crops read back as mode 1 and a full equirectangular
// projection, and survive both decode/encode paths.
func TestSphericalSampleEntryRoundTrip(t *testing.T) {
	avc1 := newSphericalSampleEntry(t, mp4.StereoModeTopBottom, 0, 0, 0, 0, 0, 0, 0)
	boxDiffAfterEncodeAndDecode(t, avc1)

	decoded := boxAfterEncodeAndDecode(t, avc1).(*mp4.VisualSampleEntryBox)
	st3d := decoded.Stereo3D()
	if st3d == nil || st3d.StereoMode != mp4.StereoModeTopBottom {
		t.Fatalf("stereo mode after round-trip: %v", st3d)
	}
	prhd := decoded.ProjectionHeader()
	if prhd == nil || prhd.Yaw() != 0 || prhd.Pitch() != 0 || prhd.Roll() != 0 {
		t.Fatalf("pose after round-trip: %v", prhd)
	}
	equi := decoded.EquirectangularProjection()
	if equi == nil || !equi.IsFullProjection() {
		t.Fatalf("equirectangular projection after round-trip: %v", equi)
	}
	if decoded.SphericalVideo() == nil || decoded.SphericalVideo().Svhd.MetadataSource != "mp4ff test" {
		t.Fatal("sv3d and svhd metadata source not available after round-trip")
	}
}

func TestSphericalSampleEntryCroppedAndPosed(t *testing.T) {
	avc1 := newSphericalSampleEntry(t, mp4.StereoModeLeftRight,
		45<<16, -10<<16, 2<<16, 0x20000000, 0x10000000, 0, 0)
	boxDiffAfterEncodeAndDecode(t, avc1)
	decoded := boxAfterEncodeAndDecode(t, avc1).(*mp4.VisualSampleEntryBox)
	if decoded.Stereo3D().StereoMode != mp4.StereoModeLeftRight {
		t.Error("stereo mode lost")
	}
	prhd := decoded.ProjectionHeader()
	if prhd.Yaw() != 45.0 || prhd.Pitch() != -10.0 || prhd.Roll() != 2.0 {
		t.Errorf("pose %v %v %v", prhd.Yaw(), prhd.Pitch(), prhd.Roll())
	}
	equi := decoded.EquirectangularProjection()
	if equi.TopFraction() != 0.125 || equi.BottomFraction() != 0.0625 {
		t.Errorf("crops top %v bottom %v", equi.TopFraction(), equi.BottomFraction())
	}
}

func TestSphericalSampleEntryCanonicalBoxSizes(t *testing.T) {
	avc1 := newSphericalSampleEntry(t, mp4.StereoModeMonoscopic, 0, 0, 0, 0, 0, 0, 0)
	data := encodeForTest(t, avc1)
	// The hierarchy must appear in the st3d then sv3d order of the RFC.
	st3dPos := bytes.Index(data, []byte("st3d"))
	sv3dPos := bytes.Index(data, []byte("sv3d"))
	svhdPos := bytes.Index(data, []byte("svhd"))
	projPos := bytes.Index(data, []byte("proj"))
	prhdPos := bytes.Index(data, []byte("prhd"))
	equiPos := bytes.Index(data, []byte("equi"))
	for name, pos := range map[string]int{
		"st3d": st3dPos, "sv3d": sv3dPos, "svhd": svhdPos, "proj": projPos, "prhd": prhdPos, "equi": equiPos,
	} {
		if pos < 0 {
			t.Fatalf("box %s not found in sample entry", name)
		}
	}
	if !(st3dPos < sv3dPos && sv3dPos < svhdPos && svhdPos < projPos && projPos < prhdPos && prhdPos < equiPos) {
		t.Fatalf("unexpected box order: st3d=%d sv3d=%d svhd=%d proj=%d prhd=%d equi=%d",
			st3dPos, sv3dPos, svhdPos, projPos, prhdPos, equiPos)
	}
}

func TestSv3dMissingMandatoryBoxes(t *testing.T) {
	// sv3d without any children is missing svhd and proj.
	empty := &mp4.SphericalVideoBox{}
	if err := empty.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "svhd") {
		t.Fatalf("empty sv3d gave error %v", err)
	}

	sv3d := &mp4.SphericalVideoBox{}
	sv3d.AddChild(mp4.NewSphericalVideoHeaderBox("tool"))
	// svhd present, proj missing.
	if err := sv3d.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "proj") {
		t.Fatalf("sv3d without proj gave error %v", err)
	}
}

func TestProjMissingMandatoryBoxes(t *testing.T) {
	proj := &mp4.SphericalProjectionBox{}
	proj.AddChild(mp4.NewEquirectangularProjectionBox(0, 0, 0, 0))
	if err := proj.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "prhd") {
		t.Fatalf("proj without prhd gave error %v", err)
	}

	proj = &mp4.SphericalProjectionBox{}
	proj.AddChild(mp4.NewProjectionHeaderBox(0, 0, 0))
	if err := proj.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "projection data box") {
		t.Fatalf("proj without projection data gave error %v", err)
	}
}

func TestDuplicateBoxesRejected(t *testing.T) {
	// Two st3d boxes in one visual sample entry.
	avc1 := mp4.CreateVisualSampleEntryBox("avc1", 640, 360, nil)
	avc1.AddChild(mp4.NewStereo3DBox(mp4.StereoModeMonoscopic))
	avc1.AddChild(mp4.NewStereo3DBox(mp4.StereoModeTopBottom))
	if err := avc1.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "st3d") {
		t.Fatalf("duplicate st3d gave error %v", err)
	}

	// Two svhd boxes inside one sv3d.
	sv3d := &mp4.SphericalVideoBox{}
	sv3d.AddChild(mp4.NewSphericalVideoHeaderBox("a"))
	sv3d.AddChild(mp4.NewSphericalVideoHeaderBox("b"))
	proj := &mp4.SphericalProjectionBox{}
	proj.AddChild(mp4.NewProjectionHeaderBox(0, 0, 0))
	proj.AddChild(mp4.NewEquirectangularProjectionBox(0, 0, 0, 0))
	sv3d.AddChild(proj)
	if err := sv3d.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "svhd") {
		t.Fatalf("duplicate svhd gave error %v", err)
	}

	// Two projection data boxes inside one proj.
	proj = &mp4.SphericalProjectionBox{}
	proj.AddChild(mp4.NewProjectionHeaderBox(0, 0, 0))
	proj.AddChild(mp4.NewEquirectangularProjectionBox(0, 0, 0, 0))
	// cbmp is 20 bytes: 8 header + 4 version/flags + 4 layout + 4 padding.
	cbmpPayload := make([]byte, 12)
	proj.AddChild(mp4.CreateUnknownBox("cbmp", 20, cbmpPayload))
	if err := proj.Encode(&bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "projection data boxes") {
		t.Fatalf("two projection data boxes gave error %v", err)
	}
}

// TestUnknownChildrenRoundTrip puts an unknown box in sv3d and in proj. It must
// be carried as an UnknownBox and survive both encode/decode paths byte for byte.
func TestUnknownChildrenRoundTrip(t *testing.T) {
	unknownInSv3d := mp4.CreateUnknownBox("zzzz", 16, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	unknownInProj := mp4.CreateUnknownBox("yyyy", 14, []byte{9, 9, 9, 9, 9, 9})
	proj := &mp4.SphericalProjectionBox{}
	proj.AddChild(mp4.NewProjectionHeaderBox(0, 0, 0))
	proj.AddChild(unknownInProj)
	proj.AddChild(mp4.NewEquirectangularProjectionBox(0, 0, 0, 0))

	sv3d := &mp4.SphericalVideoBox{}
	sv3d.AddChild(mp4.NewSphericalVideoHeaderBox(""))
	sv3d.AddChild(unknownInSv3d)
	sv3d.AddChild(proj)
	boxDiffAfterEncodeAndDecode(t, sv3d)

	decoded := boxAfterEncodeAndDecode(t, sv3d).(*mp4.SphericalVideoBox)
	var foundInSv3d, foundInProj bool
	for _, c := range decoded.GetChildren() {
		if c.Type() == "zzzz" {
			foundInSv3d = true
			if !bytes.Equal(c.(*mp4.UnknownBox).Payload(), []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
				t.Error("unknown sv3d child payload changed")
			}
		}
	}
	for _, c := range decoded.Proj.GetChildren() {
		if c.Type() == "yyyy" {
			foundInProj = true
		}
	}
	if !foundInSv3d || !foundInProj {
		t.Fatalf("unknown children lost: sv3d=%t proj=%t", foundInSv3d, foundInProj)
	}
}

// TestCbmpProjectionIsUnknown checks that an unsupported cubemap projection data
// box still round-trips, while the mandatory prhd remains decoded and queryable.
func TestCbmpProjectionIsUnknown(t *testing.T) {
	// cbmp is 20 bytes: 8 header + 4 version/flags + 4 layout + 4 padding, all zero.
	cbmp := mp4.CreateUnknownBox("cbmp", 20, make([]byte, 12))
	proj := &mp4.SphericalProjectionBox{}
	proj.AddChild(mp4.NewProjectionHeaderBox(0, 0, 0))
	proj.AddChild(cbmp)
	sv3d := &mp4.SphericalVideoBox{}
	sv3d.AddChild(mp4.NewSphericalVideoHeaderBox("tool"))
	sv3d.AddChild(proj)
	boxDiffAfterEncodeAndDecode(t, sv3d)

	decoded := boxAfterEncodeAndDecode(t, sv3d).(*mp4.SphericalVideoBox)
	if decoded.Proj.Prhd == nil {
		t.Fatal("prhd of cbmp proj not decoded")
	}
	if decoded.Proj.Equi != nil {
		t.Fatal("cbmp must not be exposed as an equi box")
	}
	var foundCbmp bool
	for _, c := range decoded.Proj.GetChildren() {
		if c.Type() == "cbmp" {
			foundCbmp = true
		}
	}
	if !foundCbmp {
		t.Fatal("cbmp child not preserved")
	}
}

// TestAppleProjStillDecoded makes sure the shared "proj" FourCC still decodes the
// Apple vexu proj box (with a prji child) as ProjBox.
func TestAppleProjStillDecoded(t *testing.T) {
	proj := &mp4.ProjBox{}
	proj.AddChild(&mp4.PrjiBox{ProjectionType: "rect"})
	boxDiffAfterEncodeAndDecode(t, proj)
	decoded := boxAfterEncodeAndDecode(t, proj)
	if _, ok := decoded.(*mp4.ProjBox); !ok {
		t.Fatalf("Apple proj decoded as %T", decoded)
	}
}

func TestSphericalBoxesInfo(t *testing.T) {
	avc1 := newSphericalSampleEntry(t, mp4.StereoModeTopBottom, 0, 0, 0, 0, 0, 0, 0)
	var buf bytes.Buffer
	if err := avc1.Info(&buf, "all:1", "", "  "); err != nil {
		t.Fatal(err)
	}
	info := buf.String()
	for _, want := range []string{"[st3d]", "stereoMode: top-bottom (1)", "[sv3d]", "[svhd]",
		"[proj]", "[prhd]", "[equi]", "projectionBoundsTop"} {
		if !strings.Contains(info, want) {
			t.Errorf("Info output lacks %q:\n%s", want, info)
		}
	}
}

func TestNewSphericalVideoBoxValidation(t *testing.T) {
	if _, err := mp4.NewSphericalVideoBox("tool", nil,
		mp4.NewEquirectangularProjectionBox(0, 0, 0, 0)); err == nil {
		t.Error("missing prhd should be rejected")
	}
	if _, err := mp4.NewSphericalVideoBox("tool", mp4.NewProjectionHeaderBox(0, 0, 0), nil); err == nil {
		t.Error("missing projection data should be rejected")
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	data, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encodeForTest(t *testing.T, b mp4.Box) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := b.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Ensure malformed boxes return errors through both decode paths, not panics.
func TestMalformedSphericalBoxes(t *testing.T) {
	cases := map[string]string{
		"truncated st3d":     "0000000d73743364000000",
		"truncated equi":     "0000001c65717569000000000000000000000000",
		"svhd without NUL":   "0000000e7376686400000000616263",
		"sv3d missing boxes": "0000000873763364",
	}
	for name, h := range cases {
		data := mustHex(t, h)
		if _, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(data)); err == nil {
			t.Errorf("%s: expected decode error", name)
		}
		if _, err := mp4.DecodeBox(0, bytes.NewReader(data)); err == nil {
			t.Errorf("%s: expected io decode error", name)
		}
	}
}
