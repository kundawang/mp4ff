package mp4

import (
	"fmt"
	"io"
	"math"

	"github.com/Eyevinn/mp4ff/bits"
)

// Stereo layouts of St3dBox.StereoMode, Google Spherical Video V2 RFC Sec. "st3d".
const (
	StereoModeMonoscopic   = 0 // Single monoscopic view
	StereoModeTopBottom    = 1 // Left eye in the top half, right eye in the bottom half
	StereoModeLeftRight    = 2 // Left eye in the left half, right eye in the right half
	StereoModeStereoCustom = 3 // Application-dependent layout, for example a mesh per eye
	StereoModeRightLeft    = 4 // Right eye in the left half, left eye in the right half
)

// stereoModeMax is the highest stereo_mode value defined by the Spherical Video V2 RFC.
const stereoModeMax = StereoModeRightLeft

// Stereo3DBox is the Stereoscopic 3D Video Box (st3d), a FullBox inside a
// VisualSampleEntry. It gives the stereo frame layout of a spherical or
// stereoscopic video track. Defined in the Google Spherical Video V2 RFC and
// referenced by ISO/IEC 23000-21.
type Stereo3DBox struct {
	Version    byte
	Flags      uint32
	StereoMode byte
}

// DecodeSt3D decodes an st3d box.
func DecodeSt3D(hdr BoxHeader, startPos uint64, r io.Reader) (Box, error) {
	data, err := readBoxBody(r, hdr)
	if err != nil {
		return nil, err
	}
	sr := bits.NewFixedSliceReader(data)
	return DecodeSt3DSR(hdr, startPos, sr)
}

// DecodeSt3DSR decodes an st3d box from a SliceReader.
func DecodeSt3DSR(hdr BoxHeader, startPos uint64, sr bits.SliceReader) (Box, error) {
	versionAndFlags := sr.ReadUint32()
	b := &Stereo3DBox{
		Version:    byte(versionAndFlags >> 24),
		Flags:      versionAndFlags & flagsMask,
		StereoMode: sr.ReadUint8(),
	}
	if err := sr.AccError(); err != nil {
		return nil, err
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return b, nil
}

// check validates the version, flags and stereo layout of an st3d box.
func (b *Stereo3DBox) check() error {
	if b.Version != 0 || b.Flags != 0 {
		return fmt.Errorf("st3d box must have version 0 and flags 0, got version %d flags %#x",
			b.Version, b.Flags)
	}
	if b.StereoMode > stereoModeMax {
		return fmt.Errorf("st3d box has unsupported stereo_mode %d, only 0 to %d are defined",
			b.StereoMode, stereoModeMax)
	}
	return nil
}

// Type returns the box type.
func (b *Stereo3DBox) Type() string { return "st3d" }

// Size returns the calculated box size: header + version/flags + one stereo_mode byte.
func (b *Stereo3DBox) Size() uint64 { return uint64(boxHeaderSize + 4 + 1) }

// Encode writes the box to w.
func (b *Stereo3DBox) Encode(w io.Writer) error {
	sw := bits.NewFixedSliceWriter(int(b.Size()))
	if err := b.EncodeSW(sw); err != nil {
		return err
	}
	_, err := w.Write(sw.Bytes())
	return err
}

// EncodeSW writes the box to sw.
func (b *Stereo3DBox) EncodeSW(sw bits.SliceWriter) error {
	if err := b.check(); err != nil {
		return err
	}
	if err := EncodeHeaderSW(b, sw); err != nil {
		return err
	}
	sw.WriteUint32((uint32(b.Version) << 24) | b.Flags)
	sw.WriteUint8(b.StereoMode)
	return sw.AccError()
}

// Info writes box-specific information.
func (b *Stereo3DBox) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	bd := newInfoDumper(w, indent, b, int(b.Version), b.Flags)
	bd.write(" - stereoMode: %s (%d)", b.StereoModeName(), b.StereoMode)
	return bd.err
}

// StereoModeName returns the name of the stereo layout, or "reserved(n)" for an undefined value.
func (b *Stereo3DBox) StereoModeName() string {
	switch b.StereoMode {
	case StereoModeMonoscopic:
		return "monoscopic"
	case StereoModeTopBottom:
		return "top-bottom"
	case StereoModeLeftRight:
		return "left-right"
	case StereoModeStereoCustom:
		return "stereo-custom"
	case StereoModeRightLeft:
		return "right-left"
	default:
		return fmt.Sprintf("reserved(%d)", b.StereoMode)
	}
}

// SphericalVideoBox is the Spherical Video Box (sv3d), a plain container box inside
// a VisualSampleEntry. It carries an SvhdBox and a SphericalProjectionBox, and
// preserves unknown child boxes so they round-trip. Defined in the Google
// Spherical Video V2 RFC and referenced by ISO/IEC 23000-21.
type SphericalVideoBox struct {
	Svhd     *SphericalVideoHeaderBox
	Proj     *SphericalProjectionBox
	Children []Box
}

// DecodeSv3d decodes an sv3d box.
func DecodeSv3d(hdr BoxHeader, startPos uint64, r io.Reader) (Box, error) {
	children, err := DecodeContainerChildren(hdr, startPos+8, startPos+hdr.Size, r)
	if err != nil {
		return nil, err
	}
	return decodeSv3d(children)
}

// DecodeSv3dSR decodes an sv3d box from a SliceReader.
func DecodeSv3dSR(hdr BoxHeader, startPos uint64, sr bits.SliceReader) (Box, error) {
	children, err := DecodeContainerChildrenSR(hdr, startPos+8, startPos+hdr.Size, sr)
	if err != nil {
		return nil, err
	}
	return decodeSv3d(children)
}

// decodeSv3d builds an sv3d box from its decoded children and checks the mandatory
// svhd and proj boxes and duplicates.
func decodeSv3d(children []Box) (Box, error) {
	b := &SphericalVideoBox{Children: make([]Box, 0, len(children))}
	for _, c := range children {
		b.AddChild(c)
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return b, nil
}

// AddChild adds a child box and sets the pointer to the known svhd and proj boxes.
// A second svhd or proj box is reported by check as a duplicate.
func (b *SphericalVideoBox) AddChild(child Box) {
	switch box := child.(type) {
	case *SphericalVideoHeaderBox:
		b.Svhd = box
	case *SphericalProjectionBox:
		b.Proj = box
	}
	b.Children = append(b.Children, child)
}

// check validates that sv3d has exactly one svhd and one proj box.
func (b *SphericalVideoBox) check() error {
	nrSvhd, nrProj := 0, 0
	for _, c := range b.Children {
		switch c.Type() {
		case "svhd":
			nrSvhd++
		case "proj":
			nrProj++
		}
	}
	if nrSvhd == 0 {
		return fmt.Errorf("sv3d box is missing its mandatory svhd box")
	}
	if nrSvhd > 1 {
		return fmt.Errorf("sv3d box has %d svhd boxes, but exactly one is allowed", nrSvhd)
	}
	if nrProj == 0 {
		return fmt.Errorf("sv3d box is missing its mandatory proj box")
	}
	if nrProj > 1 {
		return fmt.Errorf("sv3d box has %d proj boxes, but exactly one is allowed", nrProj)
	}
	return nil
}

// Type returns the box type.
func (b *SphericalVideoBox) Type() string { return "sv3d" }

// Size returns the calculated size.
func (b *SphericalVideoBox) Size() uint64 { return containerSize(b.Children) }

// GetChildren returns the child boxes.
func (b *SphericalVideoBox) GetChildren() []Box { return b.Children }

// Encode writes the box to w.
func (b *SphericalVideoBox) Encode(w io.Writer) error {
	if err := b.check(); err != nil {
		return err
	}
	return EncodeContainer(b, w)
}

// EncodeSW writes the box to sw.
func (b *SphericalVideoBox) EncodeSW(sw bits.SliceWriter) error {
	if err := b.check(); err != nil {
		return err
	}
	return EncodeContainerSW(b, sw)
}

// Info writes box-specific information.
func (b *SphericalVideoBox) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	return ContainerInfo(b, w, specificBoxLevels, indent, indentStep)
}

// SphericalVideoHeaderBox is the Spherical Video Header Box (svhd), a FullBox
// inside sv3d. MetadataSource is the null-terminated UTF-8 string naming the
// tool that created the spherical metadata; the terminating zero is added on encode.
type SphericalVideoHeaderBox struct {
	Version        byte
	Flags          uint32
	MetadataSource string
}

// DecodeSvhd decodes an svhd box.
func DecodeSvhd(hdr BoxHeader, startPos uint64, r io.Reader) (Box, error) {
	data, err := readBoxBody(r, hdr)
	if err != nil {
		return nil, err
	}
	sr := bits.NewFixedSliceReader(data)
	return DecodeSvhdSR(hdr, startPos, sr)
}

// DecodeSvhdSR decodes an svhd box from a SliceReader.
func DecodeSvhdSR(hdr BoxHeader, startPos uint64, sr bits.SliceReader) (Box, error) {
	versionAndFlags := sr.ReadUint32()
	b := &SphericalVideoHeaderBox{
		Version:        byte(versionAndFlags >> 24),
		Flags:          versionAndFlags & flagsMask,
		MetadataSource: sr.ReadZeroTerminatedString(hdr.payloadLen() - 4),
	}
	if err := sr.AccError(); err != nil {
		return nil, err
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return b, nil
}

// check validates the version and flags of an svhd box.
func (b *SphericalVideoHeaderBox) check() error {
	if b.Version != 0 || b.Flags != 0 {
		return fmt.Errorf("svhd box must have version 0 and flags 0, got version %d flags %#x",
			b.Version, b.Flags)
	}
	return nil
}

// Type returns the box type.
func (b *SphericalVideoHeaderBox) Type() string { return "svhd" }

// Size returns the calculated size, including the terminating zero of metadata_source.
func (b *SphericalVideoHeaderBox) Size() uint64 {
	return uint64(boxHeaderSize + 4 + len(b.MetadataSource) + 1)
}

// Encode writes the box to w.
func (b *SphericalVideoHeaderBox) Encode(w io.Writer) error {
	sw := bits.NewFixedSliceWriter(int(b.Size()))
	if err := b.EncodeSW(sw); err != nil {
		return err
	}
	_, err := w.Write(sw.Bytes())
	return err
}

// EncodeSW writes the box to sw.
func (b *SphericalVideoHeaderBox) EncodeSW(sw bits.SliceWriter) error {
	if err := b.check(); err != nil {
		return err
	}
	if err := EncodeHeaderSW(b, sw); err != nil {
		return err
	}
	sw.WriteUint32((uint32(b.Version) << 24) | b.Flags)
	sw.WriteString(b.MetadataSource, true)
	return sw.AccError()
}

// Info writes box-specific information.
func (b *SphericalVideoHeaderBox) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	bd := newInfoDumper(w, indent, b, int(b.Version), b.Flags)
	bd.write(" - metadataSource: %q", b.MetadataSource)
	return bd.err
}

// SphericalProjectionBox is the Projection Box (proj) inside an sv3d box. It
// holds a ProjectionHeaderBox and exactly one projection data box, which is an
// EquirectangularProjectionBox for the supported projection. Other projection
// data boxes (cbmp, mshp) and unknown children stay as unknown boxes and
// round-trip. Its Go type name differs from ProjBox, which is the Apple
// visionOS proj box held in a vexu box.
type SphericalProjectionBox struct {
	Prhd     *ProjectionHeaderBox
	Equi     *EquirectangularProjectionBox
	Children []Box
}

// decodeProjChildren decodes a proj box, which two schemes share: the Apple
// spatial/immersive ProjBox in a vexu box (children such as prji) and the
// Spherical Video V2 SphericalProjectionBox in an sv3d box (mandatory prhd).
// The children tell the two layouts apart. The registered proj decoder
// (DecodeProj and DecodeProjSR) calls this function.
func decodeProjChildren(children []Box) (Box, error) {
	for _, c := range children {
		switch c.Type() {
		case "prhd", "equi", "cbmp", "mshp":
			return decodeSphericalProj(children)
		}
	}
	b := ProjBox{Children: make([]Box, 0, len(children))}
	for _, c := range children {
		b.AddChild(c)
	}
	return &b, nil
}

// decodeSphericalProj builds a proj box from its decoded children and checks the
// mandatory prhd box, the single projection data box and duplicates.
func decodeSphericalProj(children []Box) (Box, error) {
	b := &SphericalProjectionBox{Children: make([]Box, 0, len(children))}
	for _, c := range children {
		b.AddChild(c)
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return b, nil
}

// AddChild adds a child box and sets the pointer to the known prhd and equi boxes.
// Duplicate and unsupported boxes are reported by check.
func (b *SphericalProjectionBox) AddChild(child Box) {
	switch box := child.(type) {
	case *ProjectionHeaderBox:
		b.Prhd = box
	case *EquirectangularProjectionBox:
		b.Equi = box
	}
	b.Children = append(b.Children, child)
}

// check validates the mandatory prhd box and the single projection data box of proj.
func (b *SphericalProjectionBox) check() error {
	nrPrhd, nrProjectionData := 0, 0
	for _, c := range b.Children {
		switch c.Type() {
		case "prhd":
			nrPrhd++
		case "equi", "cbmp", "mshp":
			nrProjectionData++
		}
	}
	if nrPrhd == 0 {
		return fmt.Errorf("proj box is missing its mandatory prhd box")
	}
	if nrPrhd > 1 {
		return fmt.Errorf("proj box has %d prhd boxes, but exactly one is allowed", nrPrhd)
	}
	if nrProjectionData == 0 {
		return fmt.Errorf("proj box is missing its projection data box (equi, cbmp or mshp)")
	}
	if nrProjectionData > 1 {
		return fmt.Errorf("proj box has %d projection data boxes, but exactly one is allowed",
			nrProjectionData)
	}
	return nil
}

// Type returns the box type.
func (b *SphericalProjectionBox) Type() string { return "proj" }

// Size returns the calculated size.
func (b *SphericalProjectionBox) Size() uint64 { return containerSize(b.Children) }

// GetChildren returns the child boxes.
func (b *SphericalProjectionBox) GetChildren() []Box { return b.Children }

// Encode writes the box to w.
func (b *SphericalProjectionBox) Encode(w io.Writer) error {
	if err := b.check(); err != nil {
		return err
	}
	return EncodeContainer(b, w)
}

// EncodeSW writes the box to sw.
func (b *SphericalProjectionBox) EncodeSW(sw bits.SliceWriter) error {
	if err := b.check(); err != nil {
		return err
	}
	return EncodeContainerSW(b, sw)
}

// Info writes box-specific information.
func (b *SphericalProjectionBox) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	return ContainerInfo(b, w, specificBoxLevels, indent, indentStep)
}

// ProjectionHeaderBox is the Projection Header Box (prhd), a FullBox inside a
// proj box. Its three pose values are signed 16.16 fixed-point degrees, as
// defined in the Google Spherical Video V2 RFC and ISO/IEC 23000-21.
type ProjectionHeaderBox struct {
	Version byte
	Flags   uint32
	// YawDegrees, PitchDegrees and RollDegrees are the raw 16.16 fixed-point values.
	// Use Yaw, Pitch and Roll to get the angles in degrees.
	YawDegrees   int32
	PitchDegrees int32
	RollDegrees  int32
}

// fixed16One is the 16.16 fixed-point value 1.0.
const fixed16One = 1 << 16

// check validates the version and flags of a prhd box.
func (b *ProjectionHeaderBox) check() error {
	if b.Version != 0 || b.Flags != 0 {
		return fmt.Errorf("prhd box must have version 0 and flags 0, got version %d flags %#x",
			b.Version, b.Flags)
	}
	return nil
}

// DecodePrhd decodes a prhd box.
func DecodePrhd(hdr BoxHeader, startPos uint64, r io.Reader) (Box, error) {
	data, err := readBoxBody(r, hdr)
	if err != nil {
		return nil, err
	}
	sr := bits.NewFixedSliceReader(data)
	return DecodePrhdSR(hdr, startPos, sr)
}

// DecodePrhdSR decodes a prhd box from a SliceReader.
func DecodePrhdSR(hdr BoxHeader, startPos uint64, sr bits.SliceReader) (Box, error) {
	versionAndFlags := sr.ReadUint32()
	b := &ProjectionHeaderBox{
		Version:      byte(versionAndFlags >> 24),
		Flags:        versionAndFlags & flagsMask,
		YawDegrees:   sr.ReadInt32(),
		PitchDegrees: sr.ReadInt32(),
		RollDegrees:  sr.ReadInt32(),
	}
	if err := sr.AccError(); err != nil {
		return nil, err
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return b, nil
}

// Type returns the box type.
func (b *ProjectionHeaderBox) Type() string { return "prhd" }

// Size returns the calculated size: header + version/flags + three int32 poses.
func (b *ProjectionHeaderBox) Size() uint64 { return uint64(boxHeaderSize + 4 + 12) }

// Yaw returns the yaw pose in degrees as a 16.16 fixed-point value.
func (b *ProjectionHeaderBox) Yaw() float64 {
	return float64(b.YawDegrees) / fixed16One
}

// Pitch returns the pitch pose in degrees as a 16.16 fixed-point value.
func (b *ProjectionHeaderBox) Pitch() float64 {
	return float64(b.PitchDegrees) / fixed16One
}

// Roll returns the roll pose in degrees as a 16.16 fixed-point value.
func (b *ProjectionHeaderBox) Roll() float64 {
	return float64(b.RollDegrees) / fixed16One
}

// Encode writes the box to w.
func (b *ProjectionHeaderBox) Encode(w io.Writer) error {
	sw := bits.NewFixedSliceWriter(int(b.Size()))
	if err := b.EncodeSW(sw); err != nil {
		return err
	}
	_, err := w.Write(sw.Bytes())
	return err
}

// EncodeSW writes the box to sw.
func (b *ProjectionHeaderBox) EncodeSW(sw bits.SliceWriter) error {
	if err := b.check(); err != nil {
		return err
	}
	if err := EncodeHeaderSW(b, sw); err != nil {
		return err
	}
	sw.WriteUint32((uint32(b.Version) << 24) | b.Flags)
	sw.WriteInt32(b.YawDegrees)
	sw.WriteInt32(b.PitchDegrees)
	sw.WriteInt32(b.RollDegrees)
	return sw.AccError()
}

// Info writes box-specific information.
func (b *ProjectionHeaderBox) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	bd := newInfoDumper(w, indent, b, int(b.Version), b.Flags)
	bd.write(" - yaw: %.4f degrees", b.Yaw())
	bd.write(" - pitch: %.4f degrees", b.Pitch())
	bd.write(" - roll: %.4f degrees", b.Roll())
	return bd.err
}

// EquirectangularProjectionBox is the Equirectangular Projection Box (equi), a
// FullBox projection data box inside a proj box. The four bounds are unsigned
// 0.32 fixed-point proportions of the projection cropped from each frame edge;
// zero on every edge means an uncropped equirectangular frame. Defined in the
// Google Spherical Video V2 RFC and referenced by ISO/IEC 23000-21.
type EquirectangularProjectionBox struct {
	Version byte
	Flags   uint32
	// BoundsTop, BoundsBottom, BoundsLeft and BoundsRight are the raw 0.32
	// fixed-point crop proportions. Use the corresponding fraction methods to
	// read them as numbers between 0 and 1.
	BoundsTop    uint32
	BoundsBottom uint32
	BoundsLeft   uint32
	BoundsRight  uint32
}

// DecodeEqui decodes an equi box.
func DecodeEqui(hdr BoxHeader, startPos uint64, r io.Reader) (Box, error) {
	data, err := readBoxBody(r, hdr)
	if err != nil {
		return nil, err
	}
	sr := bits.NewFixedSliceReader(data)
	return DecodeEquiSR(hdr, startPos, sr)
}

// DecodeEquiSR decodes an equi box from a SliceReader.
func DecodeEquiSR(hdr BoxHeader, startPos uint64, sr bits.SliceReader) (Box, error) {
	versionAndFlags := sr.ReadUint32()
	b := &EquirectangularProjectionBox{
		Version:      byte(versionAndFlags >> 24),
		Flags:        versionAndFlags & flagsMask,
		BoundsTop:    sr.ReadUint32(),
		BoundsBottom: sr.ReadUint32(),
		BoundsLeft:   sr.ReadUint32(),
		BoundsRight:  sr.ReadUint32(),
	}
	if err := sr.AccError(); err != nil {
		return nil, err
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return b, nil
}

// check validates the version and the crop combinations of an equi box.
func (b *EquirectangularProjectionBox) check() error {
	if b.Version != 0 || b.Flags != 0 {
		return fmt.Errorf("equi box must have version 0 and flags 0, got version %d flags %#x",
			b.Version, b.Flags)
	}
	// The RFC requires bottom < 0xFFFFFFFF - top and right < 0xFFFFFFFF - left,
	// so the crops of each pair leave at least one frame row or column.
	if uint64(b.BoundsTop)+uint64(b.BoundsBottom) >= math.MaxUint32 {
		return fmt.Errorf("equi box top and bottom crops must leave part of the frame: top=%#x bottom=%#x",
			b.BoundsTop, b.BoundsBottom)
	}
	if uint64(b.BoundsLeft)+uint64(b.BoundsRight) >= math.MaxUint32 {
		return fmt.Errorf("equi box left and right crops must leave part of the frame: left=%#x right=%#x",
			b.BoundsLeft, b.BoundsRight)
	}
	return nil
}

// Type returns the box type.
func (b *EquirectangularProjectionBox) Type() string { return "equi" }

// Size returns the calculated size: header + version/flags + four uint32 bounds.
func (b *EquirectangularProjectionBox) Size() uint64 { return uint64(boxHeaderSize + 4 + 16) }

// fixed32One is the 0.32 fixed-point value 1.0, which does not fit in a uint32.
const fixed32One = uint64(1) << 32

// TopFraction returns the crop from the top edge as a fraction between 0 and 1.
func (b *EquirectangularProjectionBox) TopFraction() float64 {
	return float64(uint64(b.BoundsTop)) / float64(fixed32One)
}

// BottomFraction returns the crop from the bottom edge as a fraction between 0 and 1.
func (b *EquirectangularProjectionBox) BottomFraction() float64 {
	return float64(uint64(b.BoundsBottom)) / float64(fixed32One)
}

// LeftFraction returns the crop from the left edge as a fraction between 0 and 1.
func (b *EquirectangularProjectionBox) LeftFraction() float64 {
	return float64(uint64(b.BoundsLeft)) / float64(fixed32One)
}

// RightFraction returns the crop from the right edge as a fraction between 0 and 1.
func (b *EquirectangularProjectionBox) RightFraction() float64 {
	return float64(uint64(b.BoundsRight)) / float64(fixed32One)
}

// IsFullProjection reports whether all crops are zero, so the whole equirectangular
// projection is present in the video frame.
func (b *EquirectangularProjectionBox) IsFullProjection() bool {
	return b.BoundsTop == 0 && b.BoundsBottom == 0 && b.BoundsLeft == 0 && b.BoundsRight == 0
}

// Encode writes the box to w.
func (b *EquirectangularProjectionBox) Encode(w io.Writer) error {
	sw := bits.NewFixedSliceWriter(int(b.Size()))
	if err := b.EncodeSW(sw); err != nil {
		return err
	}
	_, err := w.Write(sw.Bytes())
	return err
}

// EncodeSW writes the box to sw.
func (b *EquirectangularProjectionBox) EncodeSW(sw bits.SliceWriter) error {
	if err := b.check(); err != nil {
		return err
	}
	if err := EncodeHeaderSW(b, sw); err != nil {
		return err
	}
	sw.WriteUint32((uint32(b.Version) << 24) | b.Flags)
	sw.WriteUint32(b.BoundsTop)
	sw.WriteUint32(b.BoundsBottom)
	sw.WriteUint32(b.BoundsLeft)
	sw.WriteUint32(b.BoundsRight)
	return sw.AccError()
}

// Info writes box-specific information.
func (b *EquirectangularProjectionBox) Info(w io.Writer, specificBoxLevels, indent, indentStep string) error {
	bd := newInfoDumper(w, indent, b, int(b.Version), b.Flags)
	bd.write(" - projectionBoundsTop: %#x (%.6f)", b.BoundsTop, b.TopFraction())
	bd.write(" - projectionBoundsBottom: %#x (%.6f)", b.BoundsBottom, b.BottomFraction())
	bd.write(" - projectionBoundsLeft: %#x (%.6f)", b.BoundsLeft, b.LeftFraction())
	bd.write(" - projectionBoundsRight: %#x (%.6f)", b.BoundsRight, b.RightFraction())
	return bd.err
}

// NewStereo3DBox creates an st3d box with one of the StereoMode* layout values.
func NewStereo3DBox(stereoMode byte) *Stereo3DBox {
	return &Stereo3DBox{StereoMode: stereoMode}
}

// NewSphericalVideoHeaderBox creates an svhd box naming the tool that produced
// the spherical metadata.
func NewSphericalVideoHeaderBox(metadataSource string) *SphericalVideoHeaderBox {
	return &SphericalVideoHeaderBox{MetadataSource: metadataSource}
}

// NewProjectionHeaderBox creates a prhd box with the three raw 16.16
// fixed-point degree values.
func NewProjectionHeaderBox(yaw, pitch, roll int32) *ProjectionHeaderBox {
	return &ProjectionHeaderBox{YawDegrees: yaw, PitchDegrees: pitch, RollDegrees: roll}
}

// NewEquirectangularProjectionBox creates an equi box with four raw 0.32
// fixed-point crop proportions, all zero for an uncropped frame.
func NewEquirectangularProjectionBox(top, bottom, left, right uint32) *EquirectangularProjectionBox {
	return &EquirectangularProjectionBox{
		BoundsTop:    top,
		BoundsBottom: bottom,
		BoundsLeft:   left,
		BoundsRight:  right,
	}
}

// NewSphericalVideoBox builds an sv3d box hierarchy with an svhd box, and a proj
// box holding the prhd and a projection data box. Unknown children are not
// added here; call AddChild on the returned boxes to preserve extra boxes.
func NewSphericalVideoBox(metadataSource string, prhd *ProjectionHeaderBox,
	projectionData Box) (*SphericalVideoBox, error) {
	if prhd == nil {
		return nil, fmt.Errorf("sv3d box requires a prhd box")
	}
	if projectionData == nil {
		return nil, fmt.Errorf("sv3d box requires a projection data box")
	}
	proj := &SphericalProjectionBox{}
	proj.AddChild(prhd)
	proj.AddChild(projectionData)
	if err := proj.check(); err != nil {
		return nil, err
	}
	sv3d := &SphericalVideoBox{}
	sv3d.AddChild(NewSphericalVideoHeaderBox(metadataSource))
	sv3d.AddChild(proj)
	if err := sv3d.check(); err != nil {
		return nil, err
	}
	return sv3d, nil
}
