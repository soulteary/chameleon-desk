package chameleon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Dictionary download mirrors the upstream GUI "Download dictionary" tool: it
// fetches a key/password word list from a fixed set of upstream repositories
// and parses it into a GUIDictionary. The CLI writes the result to a file; it
// never contacts the device.

// DictionarySource is one named, fixed upstream dictionary URL.
type DictionarySource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// DictionarySources are the locations offered by the upstream GUI.
var DictionarySources = []DictionarySource{
	{"proxmark3-classic", "https://raw.githubusercontent.com/RfidResearchGroup/proxmark3/refs/heads/master/client/dictionaries/mfc_default_keys.dic"},
	{"proxmark3-t55xx", "https://raw.githubusercontent.com/RfidResearchGroup/proxmark3/refs/heads/master/client/dictionaries/t55xx_default_pwds.dic"},
	{"proxmark3-ultralight-c", "https://raw.githubusercontent.com/RfidResearchGroup/proxmark3/refs/heads/master/client/dictionaries/mfulc_default_keys.dic"},
	{"proxmark3-mifare-plus", "https://raw.githubusercontent.com/RfidResearchGroup/proxmark3/refs/heads/master/client/dictionaries/mfp_default_keys.dic"},
	{"flipper-unleashed-classic", "https://raw.githubusercontent.com/DarkFlippers/unleashed-firmware/refs/heads/dev/applications/main/nfc/resources/nfc/assets/mf_classic_dict.nfc"},
}

const dictionaryMaxBytes = 8 << 20

// DictionarySourceByName returns a known source, or an error listing the names.
func DictionarySourceByName(name string) (DictionarySource, error) {
	for _, s := range DictionarySources {
		if s.Name == name {
			return s, nil
		}
	}
	var names []string
	for _, s := range DictionarySources {
		names = append(names, s.Name)
	}
	return DictionarySource{}, fmt.Errorf("unknown dictionary %q; known: %s", name, strings.Join(names, ", "))
}

// DownloadDictionary fetches one source and parses it into a GUIDictionary with
// a fresh ID. The HTTP client is optional; a default with a timeout is used.
func DownloadDictionary(ctx context.Context, client *http.Client, src DictionarySource, name string) (GUIDictionary, error) {
	target, e := url.Parse(src.URL)
	if e != nil || target.Scheme != "https" {
		return GUIDictionary{}, fmt.Errorf("dictionary source must be an HTTPS URL")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if e != nil {
		return GUIDictionary{}, e
	}
	req.Header.Set("User-Agent", "chameleon-go/"+SDKVersion)
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, e := client.Do(req)
	if e != nil {
		return GUIDictionary{}, e
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, dictionaryMaxBytes+1))
	if e != nil {
		return GUIDictionary{}, e
	}
	if len(body) > dictionaryMaxBytes {
		return GUIDictionary{}, fmt.Errorf("%s: dictionary exceeds %d bytes", target.Host, dictionaryMaxBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return GUIDictionary{}, fmt.Errorf("%s: HTTP %d", target.Host, resp.StatusCode)
	}
	if name == "" {
		name = src.Name
	}
	return NewGUIDictionaryFromText(name, string(body))
}

// NewGUIDictionaryFromText parses a .dic/.nfc word list the way the GUI does:
// the first valid hex line fixes the key length (6, 4 or 16 bytes), later lines
// of other lengths are skipped, "#" starts a comment, and a key may be followed
// by a space-separated label. Keys are de-duplicated in input order.
func NewGUIDictionaryFromText(name, text string) (GUIDictionary, error) {
	if len(text) > dictionaryMaxBytes {
		return GUIDictionary{}, fmt.Errorf("dictionary text exceeds %d bytes", dictionaryMaxBytes)
	}
	id, e := NewGUIID()
	if e != nil {
		return GUIDictionary{}, e
	}
	keyLength := 0
	var keys []HexBytes
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		token := fields[0]
		if len(token) != 12 && len(token) != 8 && len(token) != 32 {
			continue
		}
		if keyLength != 0 && len(token) != keyLength {
			continue
		}
		b, e := hex.DecodeString(token)
		if e != nil {
			continue
		}
		if keyLength == 0 {
			keyLength = len(token)
		}
		id := strings.ToLower(token)
		if !seen[id] {
			seen[id] = true
			keys = append(keys, b)
		}
	}
	if keyLength == 0 || len(keys) == 0 {
		return GUIDictionary{}, fmt.Errorf("no usable keys found in the dictionary")
	}
	d := GUIDictionary{fields: map[string]json.RawMessage{}, ID: id, Name: name, KeyLength: keyLength, Keys: keys}
	return d, d.Validate()
}
