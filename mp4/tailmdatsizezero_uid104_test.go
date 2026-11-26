package mp4_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// tailMdatFixture is a file that consists only of a terminal mdat whose size is
// 0, followed by three payload bytes. Its box is 8 header bytes + 3 payload.
var tailMdatFixture = []byte{
	0x00, 0x00, 0x00, 0x00, 'm', 'd', 'a', 't',
	0x01, 0x02, 0x03,
}

type tailMdatPlainReader struct{ r io.Reader }

func (r tailMdatPlainReader) Read(p []byte) (int, error) { return r.r.Read(p) }

func TestTailMdatSizeZeroSliceReader(t *testing.T) {
	// DecodeHeaderSR resolves a top-level mdat size 0 to the bounded remainder.
	hdr, err := mp4.DecodeHeaderSR(bits.NewFixedSliceReader(tailMdatFixture))
	if err != nil {
		t.Fatalf("DecodeHeaderSR: %v", err)
	}
	if hdr.Size != 11 || hdr.Name != "mdat" || hdr.Hdrlen != 8 {
		t.Errorf("header = %+v, want mdat/11/hdrlen 8", hdr)
	}

	box, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(tailMdatFixture))
	if err != nil {
		t.Fatalf("DecodeBoxSR: %v", err)
	}
	mdat, ok := box.(*mp4.MdatBox)
	if !ok {
		t.Fatalf("box is %T, want *MdatBox", box)
	}
	if mdat.Size() != 11 {
		t.Errorf("box size = %d, want 11", mdat.Size())
	}
	if got := mdat.Payload(); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("payload = % x, want 01 02 03", got)
	}

	f, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(tailMdatFixture))
	if err != nil {
		t.Fatalf("DecodeFileSR: %v", err)
	}
	if f.Mdat == nil || f.Mdat.Size() != 11 || !bytes.Equal(f.Mdat.Data, []byte{1, 2, 3}) {
		t.Errorf("file mdat = %+v", f.Mdat)
	}
}

func TestTailMdatSizeZeroReaders(t *testing.T) {
	// A seeking reader in normal mode measures the exact size.
	f, err := mp4.DecodeFile(bytes.NewReader(tailMdatFixture))
	if err != nil {
		t.Fatalf("DecodeFile normal: %v", err)
	}
	if f.Mdat == nil || f.Mdat.Size() != 11 || !bytes.Equal(f.Mdat.Data, []byte{1, 2, 3}) {
		t.Errorf("normal mdat = %+v", f.Mdat)
	}

	// Lazy mode keeps the payload out of memory but knows its size and offsets.
	rs := bytes.NewReader(tailMdatFixture)
	lf, err := mp4.DecodeFile(rs, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	if err != nil {
		t.Fatalf("DecodeFile lazy: %v", err)
	}
	if lf.Mdat == nil {
		t.Fatal("lazy file has no mdat")
	}
	if lf.Mdat.Size() != 11 || lf.Mdat.GetLazyDataSize() != 3 {
		t.Errorf("lazy size %d payload %d, want 11 and 3", lf.Mdat.Size(), lf.Mdat.GetLazyDataSize())
	}
	if got, err := lf.Mdat.ReadData(8, 3, rs); err != nil || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("lazy ReadData = % x, %v", got, err)
	}

	// A plain, non-seeking stream reads the terminal mdat until EOF.
	pf, err := mp4.DecodeFile(tailMdatPlainReader{bytes.NewReader(tailMdatFixture)})
	if err != nil {
		t.Fatalf("DecodeFile plain stream: %v", err)
	}
	if pf.Mdat == nil || pf.Mdat.Size() != 11 || !bytes.Equal(pf.Mdat.Data, []byte{1, 2, 3}) {
		t.Errorf("plain stream mdat = %+v", pf.Mdat)
	}
}

func TestTailMdatSizeZeroAfterFtyp(t *testing.T) {
	ftyp := []byte{
		0, 0, 0, 16, 'f', 't', 'y', 'p',
		'i', 's', 'o', 'm', 0, 0, 0, 0,
	}
	data := append(append([]byte{}, ftyp...), tailMdatFixture...)
	f, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(data))
	if err != nil {
		t.Fatalf("DecodeFileSR: %v", err)
	}
	if len(f.Children) != 2 {
		t.Fatalf("%d children, want 2", len(f.Children))
	}
	if f.Children[0].Type() != "ftyp" || f.Children[0].Size() != 16 {
		t.Errorf("first child = %s/%d", f.Children[0].Type(), f.Children[0].Size())
	}
	if f.Mdat == nil || f.Mdat.Size() != 11 {
		t.Errorf("mdat = %+v, want size 11", f.Mdat)
	}
}

func TestTailMdatSizeZeroEncode(t *testing.T) {
	f, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(tailMdatFixture))
	if err != nil {
		t.Fatal(err)
	}
	sw := bits.NewFixedSliceWriter(32)
	if err := f.Mdat.EncodeSW(sw); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 11, 'm', 'd', 'a', 't', 1, 2, 3}
	if got := sw.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("EncodeSW = % x, want % x", got, want)
	}
	var buf bytes.Buffer
	if err := f.Mdat.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("Encode = % x, want % x", buf.Bytes(), want)
	}
}

func TestTailMdatSizeZeroRejectedWhereNotTerminalMdat(t *testing.T) {
	// The context-free header decoders cannot establish a top-level terminal
	// mdat and keep rejecting size 0.
	if _, err := mp4.DecodeHeader(bytes.NewReader(tailMdatFixture)); err == nil {
		t.Error("DecodeHeader: expected size 0 error")
	}
	zeroTest := []byte{0, 0, 0, 0, 't', 'e', 's', 't'}
	if _, err := mp4.DecodeHeaderSR(bits.NewFixedSliceReader(zeroTest)); err == nil {
		t.Error("DecodeHeaderSR non-mdat: expected size 0 error")
	}
	// A top-level non-mdat box with size 0 is outside the supported case.
	zeroFree := []byte{0, 0, 0, 0, 'f', 'r', 'e', 'e', 1, 2, 3, 4}
	if _, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(zeroFree)); err == nil {
		t.Error("DecodeBoxSR non-mdat size 0: expected error")
	}
	if _, err := mp4.DecodeBox(0, tailMdatPlainReader{bytes.NewReader(zeroFree)}); err == nil {
		t.Error("DecodeBox plain non-mdat size 0: expected error")
	}

	// A size-0 mdat nested in a udta container must fail on both paths.
	nested := []byte{
		0, 0, 0, 16, 'u', 'd', 't', 'a',
		0, 0, 0, 0, 'm', 'd', 'a', 't',
	}
	if _, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(nested)); err == nil {
		t.Error("DecodeFileSR nested size 0: expected error")
	}
	if _, err := mp4.DecodeFile(bytes.NewReader(nested)); err == nil {
		t.Error("DecodeFile nested size 0: expected error")
	}

	// A truncated header still fails.
	if _, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader([]byte{0, 0, 0})); err == nil {
		t.Error("truncated SR header: expected error")
	}
	if _, err := mp4.DecodeBox(0, bytes.NewReader([]byte{0, 0, 0})); err == nil {
		t.Error("truncated io header: expected error")
	}
}

func TestTailMdatDecodeHeaderSeek(t *testing.T) {
	// The seeking header helper resolves size 0 for any top-level box type.
	h, err := mp4.DecodeHeaderSeek(bytes.NewReader([]byte{0, 0, 0, 0, 'f', 'r', 'e', 'e', 1, 2, 3, 4}))
	if err != nil {
		t.Fatalf("DecodeHeaderSeek: %v", err)
	}
	if h.Size != 12 || h.Name != "free" {
		t.Errorf("header = %+v, want free/12", h)
	}
}
