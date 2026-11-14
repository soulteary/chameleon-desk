package chameleon

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed internal/catalog/upstream.json
var catalogDetails []byte

type PythonReference struct {
	Method        string   `json:"method"`
	Signature     string   `json:"signature"`
	Documentation string   `json:"doc"`
	SourceLine    int      `json:"source_line"`
	PackFormats   []string `json:"pack_formats"`
}
type CommandDetail struct {
	ID             Command           `json:"id"`
	Name           string            `json:"name"`
	Group          string            `json:"group"`
	Effect         string            `json:"effect"`
	Registered     bool              `json:"registered"`
	Python         []PythonReference `json:"python"`
	FirmwareSource string            `json:"firmware_source"`
	FirmwareLine   *int              `json:"firmware_line"`
}

// DescribeCommand includes upstream parameter names, Python struct layouts and
// exact source locations. Wire methods take bytes; this is not a typed codec.
func DescribeCommand(cmd Command) (CommandDetail, error) {
	var doc struct {
		Commands []CommandDetail `json:"commands"`
	}
	if e := json.Unmarshal(catalogDetails, &doc); e != nil {
		return CommandDetail{}, e
	}
	for _, d := range doc.Commands {
		if d.ID == cmd {
			return d, nil
		}
	}
	return CommandDetail{}, fmt.Errorf("unknown command %d", cmd)
}
