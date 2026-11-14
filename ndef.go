package chameleon

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// NDEF support mirrors the upstream GUI NDEF editor: decode and build NFC
// Forum messages, and read or replace the NDEF TLV inside a Type 2 or MIFARE
// Classic dump. It is pure data handling; nothing here touches a device.

// ndefURIPrefixes is the NFC Forum URI abbreviation table (RTD "U", index 0).
var ndefURIPrefixes = []string{
	"", "http://www.", "https://www.", "http://", "https://", "tel:", "mailto:",
	"ftp://anonymous:anonymous@", "ftp://ftp.", "ftps://", "sftp://", "smb://",
	"nfs://", "ftp://", "dav://", "news:", "telnet://", "imap:", "rtsp://",
	"urn:", "pop:", "sip:", "sips:", "tftp:", "btspp://", "btl2cap://",
	"btgoep://", "tcpobex://", "irdaobex://", "file://", "urn:epc:id:",
	"urn:epc:tag:", "urn:epc:pat:", "urn:epc:raw:", "urn:epc:", "urn:nfc:",
}

// NdefRecord is one NFC Forum record. TNF is the 3-bit type-name format.
type NdefRecord struct {
	TNF     byte     `json:"tnf"`
	Type    string   `json:"type"`
	Kind    string   `json:"kind"`
	Value   string   `json:"value,omitempty"`
	Lang    string   `json:"language,omitempty"`
	ID      HexBytes `json:"id,omitempty"`
	Payload HexBytes `json:"payload"`
}

func asciiEncode(s string) ([]byte, error) {
	for _, r := range s {
		if r > 0x7f {
			return nil, fmt.Errorf("value must be ASCII")
		}
	}
	return []byte(s), nil
}

// NewNdefText builds a UTF-8 Text record (RTD "T").
func NewNdefText(text, language string) (NdefRecord, error) {
	if language == "" {
		language = "en"
	}
	lang, e := asciiEncode(language)
	if e != nil || len(lang) > 63 {
		return NdefRecord{}, fmt.Errorf("language must be at most 63 ASCII bytes")
	}
	payload := append([]byte{byte(len(lang))}, lang...)
	payload = append(payload, []byte(text)...)
	return ndefRecord(1, "T", nil, payload)
}

// NewNdefURI builds a URI record (RTD "U") with the shortest prefix.
func NewNdefURI(uri string) (NdefRecord, error) {
	prefix := 0
	for i := 1; i < len(ndefURIPrefixes); i++ {
		if strings.HasPrefix(uri, ndefURIPrefixes[i]) && len(ndefURIPrefixes[i]) > len(ndefURIPrefixes[prefix]) {
			prefix = i
		}
	}
	payload := append([]byte{byte(prefix)}, []byte(uri[len(ndefURIPrefixes[prefix]):])...)
	return ndefRecord(1, "U", nil, payload)
}

// NewNdefMime builds a media-type record (TNF 2).
func NewNdefMime(mimeType, value string) (NdefRecord, error) {
	return ndefRecord(2, mimeType, nil, []byte(value))
}

// NewNdefExternal builds an external-type record (TNF 4).
func NewNdefExternal(externalType, value string) (NdefRecord, error) {
	return ndefRecord(4, strings.ToLower(externalType), nil, []byte(value))
}

func ndefRecord(tnf byte, typeName string, id, payload []byte) (NdefRecord, error) {
	if len(typeName) > 0xFF || len(id) > 0xFF {
		return NdefRecord{}, fmt.Errorf("NDEF type or ID exceeds 255 bytes")
	}
	r := NdefRecord{TNF: tnf & 7, Type: typeName, ID: id, Payload: payload}
	r.Kind, r.Value, r.Lang = r.describe()
	return r, nil
}

func (r NdefRecord) describe() (kind, value, lang string) {
	switch {
	case r.TNF == 1 && r.Type == "T":
		if len(r.Payload) == 0 {
			return "text", "", "en"
		}
		langLen := int(r.Payload[0] & 0x3F)
		if len(r.Payload) < langLen+1 {
			return "text", "", "en"
		}
		language := string(r.Payload[1 : langLen+1])
		body := r.Payload[langLen+1:]
		if r.Payload[0]&0x80 != 0 {
			return "text", decodeUTF16(body), language
		}
		return "text", string(body), language
	case r.TNF == 1 && r.Type == "U":
		if len(r.Payload) == 0 {
			return "uri", "", ""
		}
		prefix := ""
		if int(r.Payload[0]) < len(ndefURIPrefixes) {
			prefix = ndefURIPrefixes[r.Payload[0]]
		}
		return "uri", prefix + string(r.Payload[1:]), ""
	case r.TNF == 2:
		return "mime", string(r.Payload), ""
	case r.TNF == 4:
		return "external", string(r.Payload), ""
	}
	return "raw", strings.ToUpper(hex.EncodeToString(r.Payload)), ""
}

func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	little := b[0] == 0xFF && b[1] == 0xFE
	offset := 0
	if little || (b[0] == 0xFE && b[1] == 0xFF) {
		offset = 2
	}
	units := make([]uint16, 0, (len(b)-offset)/2)
	for i := offset; i+1 < len(b); i += 2 {
		if little {
			units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
		} else {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		}
	}
	return string(utf16.Decode(units))
}

// DecodeNdefMessage parses a complete NDEF message. Chunked records are
// rejected, matching the GUI.
func DecodeNdefMessage(message []byte) ([]NdefRecord, error) {
	if len(message) == 0 {
		return nil, nil
	}
	var records []NdefRecord
	offset := 0
	sawBegin, sawEnd := false, false
	for offset < len(message) {
		header := message[offset]
		offset++
		mb := header&0x80 != 0
		me := header&0x40 != 0
		chunked := header&0x20 != 0
		short := header&0x10 != 0
		hasID := header&0x08 != 0
		tnf := header & 0x07
		if chunked {
			return nil, fmt.Errorf("chunked NDEF records unsupported")
		}
		if len(records) == 0 && !mb {
			return nil, fmt.Errorf("missing NDEF message-begin flag")
		}
		if len(records) != 0 && mb {
			return nil, fmt.Errorf("unexpected NDEF message-begin flag")
		}
		sawBegin = sawBegin || mb
		if offset >= len(message) {
			return nil, fmt.Errorf("truncated NDEF type length")
		}
		typeLen := int(message[offset])
		offset++
		var payloadLen int
		if short {
			if offset >= len(message) {
				return nil, fmt.Errorf("truncated NDEF payload length")
			}
			payloadLen = int(message[offset])
			offset++
		} else {
			if offset+4 > len(message) {
				return nil, fmt.Errorf("truncated NDEF payload length")
			}
			payloadLen = int(binary.BigEndian.Uint32(message[offset:]))
			offset += 4
		}
		idLen := 0
		if hasID {
			if offset >= len(message) {
				return nil, fmt.Errorf("truncated NDEF ID length")
			}
			idLen = int(message[offset])
			offset++
		}
		end := offset + typeLen + idLen + payloadLen
		if end > len(message) || end < offset {
			return nil, fmt.Errorf("truncated NDEF record")
		}
		typeName := string(message[offset : offset+typeLen])
		offset += typeLen
		id := append([]byte(nil), message[offset:offset+idLen]...)
		offset += idLen
		payload := append([]byte(nil), message[offset:offset+payloadLen]...)
		offset += payloadLen
		r, e := ndefRecord(tnf, typeName, id, payload)
		if e != nil {
			return nil, e
		}
		records = append(records, r)
		if me {
			sawEnd = true
			if offset != len(message) {
				return nil, fmt.Errorf("data follows NDEF message-end record")
			}
			break
		}
	}
	if !sawBegin || !sawEnd {
		return nil, fmt.Errorf("incomplete NDEF message")
	}
	return records, nil
}

// EncodeNdefMessage serialises records into a wire NDEF message.
func EncodeNdefMessage(records []NdefRecord) ([]byte, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("an NDEF message needs at least one record")
	}
	var out []byte
	for i, r := range records {
		typeBytes, e := asciiEncode(r.Type)
		if e != nil {
			return nil, e
		}
		if len(typeBytes) > 0xFF || len(r.ID) > 0xFF {
			return nil, fmt.Errorf("NDEF type or ID exceeds 255 bytes")
		}
		short := len(r.Payload) < 256
		header := r.TNF & 0x07
		if i == 0 {
			header |= 0x80
		}
		if i == len(records)-1 {
			header |= 0x40
		}
		if short {
			header |= 0x10
		}
		if len(r.ID) > 0 {
			header |= 0x08
		}
		out = append(out, header, byte(len(typeBytes)))
		if short {
			out = append(out, byte(len(r.Payload)))
		} else {
			out = binary.BigEndian.AppendUint32(out, uint32(len(r.Payload)))
		}
		if len(r.ID) > 0 {
			out = append(out, byte(len(r.ID)))
		}
		out = append(out, typeBytes...)
		out = append(out, r.ID...)
		out = append(out, r.Payload...)
	}
	return out, nil
}

// ndefLocation is one byte position in the card dump (row and column).
type ndefLocation struct{ row, col int }

// NdefContainer is the NDEF-bearing region of a dump plus where it maps back.
type NdefContainer struct {
	Mapping       string `json:"mapping"`
	region        []byte
	locations     []ndefLocation
	tlvOffset     int
	messageOffset int
	messageLength int
}

// Message returns the raw NDEF message bytes currently stored.
func (c *NdefContainer) Message() []byte {
	return append([]byte(nil), c.region[c.messageOffset:c.messageOffset+c.messageLength]...)
}

// Capacity is the largest NDEF message that fits, keeping any preserved suffix.
func (c *NdefContainer) Capacity() int {
	suffix := c.preservedSuffix()
	available := len(c.region) - c.tlvOffset - 1 - len(suffix)
	short, long := 0, 0
	if available > 1 {
		short = clamp(available-1, 0, 254)
	}
	if available > 3 {
		long = clamp(available-3, 0, 0xFFFF)
	}
	if short > long {
		return short
	}
	return long
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// DetectNdef locates the NDEF TLV in a Type 2 or MIFARE Classic dump.
func DetectNdef(tag uint16, blocks [][]byte) (*NdefContainer, error) {
	if _, ok := guiType2Pages[tag]; ok {
		return detectType2Ndef(blocks)
	}
	if _, ok := guiClassicBlocks[tag]; ok {
		return detectClassicNdef(blocks)
	}
	return nil, fmt.Errorf("tag type %d carries no NDEF mapping", tag)
}

func detectType2Ndef(blocks [][]byte) (*NdefContainer, error) {
	if len(blocks) <= 4 || len(blocks[3]) != 4 || blocks[3][0] != 0xE1 {
		return nil, fmt.Errorf("no NFC Forum Type 2 capability container (page 3)")
	}
	advertised := int(blocks[3][2]) * 8
	if len(blocks)-4 < advertised/4 {
		return nil, fmt.Errorf("dump is shorter than the Type 2 capability-container region")
	}
	var region []byte
	var locations []ndefLocation
	for page := 4; page < len(blocks) && len(region) < advertised; page++ {
		if len(blocks[page]) != 4 {
			return nil, fmt.Errorf("invalid Type 2 page %d: expected four bytes", page)
		}
		for col := 0; col < 4 && len(region) < advertised; col++ {
			locations = append(locations, ndefLocation{page, col})
			region = append(region, blocks[page][col])
		}
	}
	return ndefFromRegion("NFC Forum Type 2", locations, region)
}

func detectClassicNdef(blocks [][]byte) (*NdefContainer, error) {
	if len(blocks) < 8 || len(blocks[1]) != 16 || len(blocks[2]) != 16 {
		return nil, fmt.Errorf("dump too small to hold a MIFARE Application Directory")
	}
	var sectors []int
	mad1 := append(append([]byte(nil), blocks[1]...), blocks[2]...)
	for sector := 1; sector <= 15; sector++ {
		o := 2 + (sector-1)*2
		if mad1[o] == 0x03 && mad1[o+1] == 0xE1 {
			sectors = append(sectors, sector)
		}
	}
	if len(blocks) >= 128 {
		var mad2 []byte
		for block := 64; block <= 66; block++ {
			if len(blocks[block]) != 16 {
				return nil, fmt.Errorf("truncated MAD2 block %d", block)
			}
			mad2 = append(mad2, blocks[block]...)
		}
		for sector := 17; sector <= 39; sector++ {
			o := 2 + (sector-17)*2
			if mad2[o] == 0x03 && mad2[o+1] == 0xE1 {
				sectors = append(sectors, sector)
			}
		}
	}
	if len(sectors) == 0 {
		return nil, fmt.Errorf("no NDEF sector marked in the MAD")
	}
	var region []byte
	var locations []ndefLocation
	for _, sector := range sectors {
		first, count, e := ClassicSectorGeometry(sector)
		if e != nil {
			return nil, e
		}
		for rel := 0; rel < count-1; rel++ {
			block := first + rel
			if block >= len(blocks) || len(blocks[block]) != 16 {
				return nil, fmt.Errorf("truncated data block %d", block)
			}
			for col := 0; col < 16; col++ {
				locations = append(locations, ndefLocation{block, col})
				region = append(region, blocks[block][col])
			}
		}
	}
	return ndefFromRegion("MIFARE Classic", locations, region)
}

func ndefFromRegion(mapping string, locations []ndefLocation, region []byte) (*NdefContainer, error) {
	offset := 0
	for offset < len(region) {
		t := region[offset]
		if t == 0x00 {
			offset++
			continue
		}
		if t == 0xFE {
			return nil, fmt.Errorf("NDEF terminator before any NDEF TLV")
		}
		tlvOffset := offset
		offset++
		if offset >= len(region) {
			return nil, fmt.Errorf("truncated TLV length")
		}
		length := int(region[offset])
		offset++
		if length == 0xFF {
			if offset+2 > len(region) {
				return nil, fmt.Errorf("truncated three-byte TLV length")
			}
			length = int(region[offset])<<8 | int(region[offset+1])
			offset += 2
		}
		if offset+length > len(region) {
			return nil, fmt.Errorf("TLV length exceeds the mapped region")
		}
		if t == 0x03 {
			return &NdefContainer{Mapping: mapping, region: region, locations: locations, tlvOffset: tlvOffset, messageOffset: offset, messageLength: length}, nil
		}
		offset += length
	}
	return nil, fmt.Errorf("no NDEF message TLV (0x03) found")
}

func (c *NdefContainer) preservedSuffix() []byte {
	start := c.messageOffset + c.messageLength
	// FE is a terminator only at a TLV boundary, never inside its value.
	for i := start; i < len(c.region); {
		t := c.region[i]
		i++
		if t == 0xFE {
			return append([]byte(nil), c.region[start:i]...)
		}
		if t == 0 {
			continue
		}
		if i >= len(c.region) {
			break
		}
		n := int(c.region[i])
		i++
		if n == 0xFF {
			if len(c.region)-i < 2 {
				break
			}
			n = int(c.region[i])<<8 | int(c.region[i+1])
			i += 2
		}
		if n > len(c.region)-i {
			break
		}
		i += n
	}
	// An unterminated or malformed suffix is preserved in full.
	suffix := c.region[start:]
	for _, b := range suffix {
		if b != 0 {
			return append([]byte(nil), suffix...)
		}
	}
	return nil
}

// WriteMessage replaces the NDEF message, rewriting the TLV in place and
// updating the dump blocks. The dump grows no larger; spare space is zeroed.
func (c *NdefContainer) WriteMessage(blocks [][]byte, message []byte) error {
	if len(message) > 0xFFFF {
		return fmt.Errorf("NDEF message exceeds 65535 bytes")
	}
	var lengthBytes []byte
	if len(message) < 255 {
		lengthBytes = []byte{byte(len(message))}
	} else {
		lengthBytes = []byte{0xFF, byte(len(message) >> 8), byte(len(message))}
	}
	out := append([]byte(nil), c.region[:c.tlvOffset]...)
	out = append(out, 0x03)
	out = append(out, lengthBytes...)
	out = append(out, message...)
	out = append(out, c.preservedSuffix()...)
	if len(out) > len(c.region) {
		return fmt.Errorf("NDEF message needs %d of %d available bytes", len(out), len(c.region))
	}
	out = append(out, make([]byte, len(c.region)-len(out))...)
	for i, loc := range c.locations {
		blocks[loc.row][loc.col] = out[i]
	}
	c.region = out
	c.messageOffset = c.tlvOffset + 1 + len(lengthBytes)
	c.messageLength = len(message)
	return nil
}

// ndefText reports whether every record decodes to valid UTF-8 for display.
func ndefText(records []NdefRecord) bool {
	for _, r := range records {
		if r.Kind != "raw" && !utf8.ValidString(r.Value) {
			return false
		}
	}
	return true
}
