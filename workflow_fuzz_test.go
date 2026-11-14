package chameleon

import "testing"

func FuzzWorkflowDecoders(f *testing.F) {
	f.Add([]byte("0102030405"))
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			t.Skip()
		}
		for _, format := range []string{"bin", "eml", "json"} {
			_, _ = DecodeMemoryFile(b, format)
		}
		_, _ = DecodeHIDProx(b)
		_, _ = DecodeIDTECK(b)
		_, _ = DecodePyramid(b)
		_, _ = DecodePACRaw(b)
		for _, p := range []string{"em410x", "electra", "hidprox", "ioprox", "pac", "idteck", "jablotron", "pyramid", "em4x05"} {
			_, _ = DecodeLFRecord(p, 100, b)
		}
	})
}
