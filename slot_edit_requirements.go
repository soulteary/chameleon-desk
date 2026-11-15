package chameleon

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SlotEdit selects one channel/operation. Initialize requires separate explicit
// confirmation; its firmware command can persist data independently of Persist.
type SlotEdit struct {
	Slot              int     `json:"slot"`
	Sense             Sense   `json:"sense"`
	Action            string  `json:"action"`
	Name              *string `json:"name,omitempty"`
	Tag               *uint16 `json:"tag,omitempty"`
	Enabled           *bool   `json:"enabled,omitempty"`
	Persist           bool    `json:"persist"`
	ConfirmInitialize bool    `json:"confirm_initialize"`
}

// Requirements validates the edit and expands its commands without device I/O.
func (p SlotEdit) Requirements() ([]Command, error) {
	if _, err := slotByte(p.Slot); err != nil {
		return nil, err
	}
	if _, err := senseByte(p.Sense); err != nil {
		return nil, err
	}
	cmds := []Command{}
	switch p.Action {
	case "nickname", "nickname-delete":
		if p.Tag != nil || p.Enabled != nil || p.ConfirmInitialize {
			return nil, fmt.Errorf("unexpected nickname parameters")
		}
		if p.Action == "nickname" {
			if p.Name == nil || len(*p.Name) == 0 || !utf8.ValidString(*p.Name) || len(*p.Name) > 32 {
				return nil, fmt.Errorf("nickname must be valid UTF-8 of 1..32 bytes")
			}
			cmds = []Command{1007, 1008}
		} else {
			if p.Name != nil {
				return nil, fmt.Errorf("delete does not accept a name")
			}
			cmds = []Command{1021, 1008}
		}
	case "type", "initialize":
		if p.Name != nil || p.Enabled != nil || p.Tag == nil {
			return nil, fmt.Errorf("select a tag type")
		}
		if *p.Tag == 0 || strings.HasPrefix(TagName(*p.Tag), "Unknown") || ((*p.Tag >= 1000) != (p.Sense == SenseHF)) {
			return nil, fmt.Errorf("tag type does not match the selected HF/LF channel")
		}
		if p.Action == "initialize" && !p.ConfirmInitialize {
			return nil, fmt.Errorf("initialization requires explicit confirmation")
		}
		if p.Action == "type" && p.ConfirmInitialize {
			return nil, fmt.Errorf("unexpected initialization confirmation")
		}
		cmds = []Command{1018, 1019, 1023}
		if p.Action == "type" {
			cmds = append(cmds, 1004)
		} else {
			cmds = append(cmds, 1005)
		}
	case "enable":
		if p.Name != nil || p.Tag != nil || p.Enabled == nil || p.ConfirmInitialize {
			return nil, fmt.Errorf("enable requires a boolean value only")
		}
		cmds = []Command{1018, 1019, 1023, 1006}
	default:
		return nil, fmt.Errorf("unknown slot edit action")
	}
	if p.Persist {
		cmds = append(cmds, 1009)
	}
	return uniqueCommands(cmds), nil
}
