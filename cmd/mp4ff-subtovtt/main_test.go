package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wantedOverlap = `WEBVTT

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

const wantedISM = `WEBVTT

00:00.000 --> 00:06.640 align:left
<c.magenta>...</c>

00:06.960 --> 00:10.880 align:center
<c.magenta>-Tout, tout, tout pourri,</c>
<c.magenta>tout, tout, tout plaplat,</c>

00:11.200 --> 00:14.360 align:center
<c.magenta>Chien Pourri et Chaplapla,</c>
<c.magenta>c'est moi, le chien, toi, le chat.</c>

00:14.680 --> 00:20.000 align:center
<c.magenta>Un ami, une poubelle,</c>
<c.magenta>et pour nous, la vie est belle.</c>

`

func TestSubtovtt(t *testing.T) {
	testCases := []struct {
		desc        string
		args        []string
		expectedErr bool
		wantErr     string
		wanted      string
	}{
		{desc: "help", args: []string{appName, "-h"}},
		{desc: "unknown flag", args: []string{appName, "-x"}, expectedErr: true},
		{desc: "version", args: []string{appName, "-version"}},
		{desc: "no args", args: []string{appName}, expectedErr: true},
		{desc: "non-existent file", args: []string{appName, "notExisting.mp4"}, expectedErr: true},
		{desc: "bad track ID", args: []string{appName, "-t", "-1", "testdata/wvtt_overlap_prog.mp4"},
			expectedErr: true},
		{desc: "no such track", args: []string{appName, "-t", "9", "testdata/wvtt_overlap_prog.mp4"},
			expectedErr: true, wantErr: "track 9"},
		{desc: "no moov", args: []string{appName, "../../mp4/testdata/1.m4s"},
			expectedErr: true, wantErr: "no moov"},
		{
			desc:   "progressive",
			args:   []string{appName, "testdata/wvtt_overlap_prog.mp4"},
			wanted: wantedOverlap,
		},
		{
			desc:   "fragmented",
			args:   []string{appName, "testdata/wvtt_overlap_frag.mp4"},
			wanted: wantedOverlap,
		},
		{
			desc:   "ISM with vtte",
			args:   []string{appName, "testdata/sample_short.ismt"},
			wanted: wantedISM,
		},
	}

	for _, c := range testCases {
		t.Run(c.desc, func(t *testing.T) {
			gotOut := bytes.Buffer{}
			err := run(c.args, &gotOut)
			if c.expectedErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				if c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error %q does not contain %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if c.wanted != "" && c.wanted != gotOut.String() {
				t.Errorf("expected\n%s\ngot\n%s", c.wanted, gotOut.String())
			}
		})
	}
}

func TestSubtovttOutputFile(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "out.vtt")
	if err := run([]string{appName, "-o", outPath, "testdata/wvtt_overlap_frag.mp4"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != wantedOverlap {
		t.Errorf("output file holds\n%s\nwant\n%s", data, wantedOverlap)
	}
}

func TestSubtovttDashOutput(t *testing.T) {
	gotOut := bytes.Buffer{}
	if err := run([]string{appName, "-o", "-", "testdata/wvtt_overlap_prog.mp4"}, &gotOut); err != nil {
		t.Fatal(err)
	}
	if gotOut.String() != wantedOverlap {
		t.Errorf("got\n%s\nwant\n%s", gotOut.String(), wantedOverlap)
	}
}
