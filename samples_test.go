package chameleon

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestManchesterPhasesAndAnalysis(t *testing.T) {
	data := []byte{}
	for _, b := range []byte{0, 1, 1, 0, 0, 1, 1, 0} {
		value := byte(0)
		if b != 0 {
			value = 200
		}
		data = append(data, bytes.Repeat([]byte{value}, 4)...)
	}
	v, e := DecodeManchester(data, 8, false, 0)
	if e != nil || v.Bits != "0101" || v.InvalidPairs != 0 || v.TimingErrors != 0 {
		t.Fatal(v, e)
	}
	flat, e := AnalyzeLFSamples(bytes.Repeat([]byte{128}, 100))
	if e != nil || !strings.Contains(flat.Hint, "flat") {
		t.Fatal(flat, e)
	}
	if _, e = DecodeManchester(data, 7, false, 0); e == nil {
		t.Fatal("odd clock accepted")
	}
}
func TestSampleExportsAndBounds(t *testing.T) {
	data := []byte{0, 255, 1, 200}
	csv, e := LFSamplesCSV(data, 125000)
	if e != nil || !strings.Contains(csv, "1,8.000000,255") {
		t.Fatal(csv, e)
	}
	svg, e := LFSamplesSVG(data)
	if e != nil {
		t.Fatal(e)
	}
	d := xml.NewDecoder(strings.NewReader(svg))
	for {
		_, e = d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = LFSamplesSVG(nil); e == nil {
		t.Fatal("empty samples accepted")
	}
}
