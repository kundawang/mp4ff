package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/mp4ff/internal"
	"github.com/Eyevinn/mp4ff/mp4"
)

const (
	appName = "mp4ff-subtovtt"
)

var usg = `%s exports the WebVTT cues of a wvtt track in a progressive or fragmented mp4 file
to a standalone .vtt file, for preview in a player that does not read subtitles from mp4.

Usage of %s:
`

type options struct {
	trackID int
	outPath string
	version bool
}

func parseOptions(fs *flag.FlagSet, args []string) (*options, error) {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, usg, appName, appName)
		fmt.Fprintf(os.Stderr, "\n%s [options] <inFile>\n\noptions:\n", appName)
		fs.PrintDefaults()
	}

	opts := options{}

	fs.IntVar(&opts.trackID, "t", 0, "Track ID to export (0 is the first wvtt track)")
	fs.StringVar(&opts.outPath, "o", "", "Output .vtt file (default - for stdout)")
	fs.BoolVar(&opts.version, "version", false, "Get mp4ff version")

	err := fs.Parse(args[1:])
	return &opts, err
}

func main() {
	if err := run(os.Args, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	o, err := parseOptions(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if o.version {
		fmt.Fprintf(stdout, "%s %s\n", appName, internal.Version())
		return nil
	}

	if len(fs.Args()) != 1 {
		fs.Usage()
		return fmt.Errorf("must specify one input file")
	}
	if o.trackID < 0 {
		return fmt.Errorf("invalid track ID %d", o.trackID)
	}

	ifh, err := os.Open(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("error opening input file: %w", err)
	}
	defer ifh.Close()
	parsedFile, err := mp4.DecodeFile(ifh, mp4.WithDecodeMode(mp4.DecModeLazyMdat))
	if err != nil {
		return fmt.Errorf("error decoding mp4 file: %w", err)
	}
	vtt, err := parsedFile.ExportWebVTT(uint32(o.trackID), ifh)
	if err != nil {
		return err
	}

	if o.outPath == "" || o.outPath == "-" {
		return writeWebVTT(vtt, stdout)
	}
	ofh, err := os.Create(o.outPath)
	if err != nil {
		return fmt.Errorf("error creating output file: %w", err)
	}
	defer ofh.Close()
	return writeWebVTT(vtt, ofh)
}

// writeWebVTT writes the cues buffered, since the output may be a pipe.
func writeWebVTT(vtt *mp4.WebVTT, w io.Writer) error {
	bw := bufio.NewWriter(w)
	if err := vtt.Encode(bw); err != nil {
		return err
	}
	return bw.Flush()
}
