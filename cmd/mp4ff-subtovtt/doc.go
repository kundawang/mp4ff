/*
mp4ff-subtovtt exports the WebVTT cues of a wvtt track in a progressive or fragmented mp4
file to a standalone .vtt file, for preview in a player that does not read subtitles from mp4.

Cues that the track restates over consecutive samples are merged, and a vtte sample ends the
active cues. The input file is only read, and other tracks are left untouched.

Usage of mp4ff-subtovtt:

	mp4ff-subtovtt [options] <inFile>

options:

	-t int
		Track ID to export (0 is the first wvtt track)
	-o string
		Output .vtt file (default - for stdout)
	-version
		Get mp4ff version
*/
package main
