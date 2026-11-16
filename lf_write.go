package chameleon

import (
	"encoding/binary"
	"fmt"
)

// LFPhysicalWriteRequest clones an explicit standard-card identity to an
// isolated T55xx target. The hardware does not provide a unique target binding.
// Passwords are caller-supplied; an empty value never means a default password.
type LFPhysicalWriteRequest struct {
	Schema         int        `json:"schema"`
	Source         GUICard    `json:"source"`
	Protocol       string     `json:"protocol"`
	NewPassword    HexBytes   `json:"new_password"`
	OldPasswords   []HexBytes `json:"old_passwords"`
	TargetIsolated bool       `json:"target_isolated"`
}

type LFPhysicalWritePlan struct {
	Schema                     int       `json:"schema"`
	SourceID                   string    `json:"source_id"`
	SourceTag                  uint16    `json:"source_tag"`
	Protocol                   string    `json:"protocol"`
	Record                     LFRecord  `json:"record"`
	Commands                   []Command `json:"commands"`
	WriteCommand               Command   `json:"write_command"`
	Readback                   LFRead    `json:"readback"`
	TargetUIDBound             bool      `json:"target_uid_bound"`
	FirmwareEncodingValidation bool      `json:"firmware_encoding_validation"`
	Notes                      []string  `json:"notes"`
}

var lfPhysicalCloneCommands = map[string]Command{
	"em410x": CmdEm410xWriteToT55xx, "electra": CmdEm410xElectraWriteToT55xx,
	"hidprox": CmdHidproxWriteToT55xx, "ioprox": CmdIoproxWriteToT55xx,
	"viking": CmdVikingWriteToT55xx, "pac": CmdPacWriteToT55xx, "jablotron": CmdJablotronWriteToT55xx,
}

// Plan validates inputs and declares commands without device access. The plan
// contains no passwords, password hashes or claim of a uniquely identified target.
func (r LFPhysicalWriteRequest) Plan() (LFPhysicalWritePlan, error) {
	var plan LFPhysicalWritePlan
	if r.Schema != 1 {
		return plan, fmt.Errorf("LF write request schema must be 1")
	}
	if err := r.Source.Validate(); err != nil {
		return plan, err
	}
	if err := r.Source.RequireCardImage(); err != nil {
		return plan, err
	}
	if len(r.Source.Data) != 0 {
		return plan, fmt.Errorf("LF physical cloning accepts a standard identity, not a memory image")
	}
	protocol, ok := LFCardProtocol(r.Source.Tag)
	if !ok || protocol != r.Protocol {
		return plan, fmt.Errorf("source LF tag type and explicit protocol must match")
	}
	if protocol == "idteck" {
		return plan, fmt.Errorf("IDTECK cloning has no supported protocol readback in this workflow")
	}
	if !r.TargetIsolated {
		return plan, fmt.Errorf("explicit isolated dedicated T55xx target acknowledgement is required; no target UID binding is available")
	}
	if len(r.NewPassword) != 4 || len(r.OldPasswords) < 1 || len(r.OldPasswords) > 64 {
		return plan, fmt.Errorf("explicit four-byte new password and 1..64 old passwords are required")
	}
	for _, password := range r.OldPasswords {
		if len(password) != 4 {
			return plan, fmt.Errorf("every explicit old password must have four bytes")
		}
	}
	input, err := r.IdentityInput()
	if err != nil {
		return plan, err
	}
	var record LFRecord
	firmwareValidation := protocol == "ioprox"
	if firmwareValidation {
		record, err = DecodeLFRecord(protocol, 0, input.ID)
	} else {
		record, err = EncodeLFIdentityOffline(input)
	}
	if err != nil {
		return plan, err
	}
	write, ok := lfPhysicalCloneCommands[record.Protocol]
	if !ok {
		return plan, fmt.Errorf("LF protocol has no controlled T55xx writer")
	}
	scan, ok := lfScanCommands[protocol]
	if !ok {
		return plan, fmt.Errorf("LF protocol has no readback command")
	}
	readback := LFRead{Protocol: protocol}
	if protocol == "hidprox" {
		readback.Format = record.ID[0]
	}
	commands := []Command{CmdChangeDeviceMode, CmdGetDeviceMode, write, scan}
	if firmwareValidation {
		commands = append(commands, CmdIoproxDecodeRaw)
	}
	notes := []string{
		"Only the selected LF identity is cloned; this is not a full target memory/configuration backup.",
		"T55xx targets cannot be UID-bound by this API. Isolate one dedicated writable test tag and keep other tags outside the RF field.",
		"One host write command can perform several RF operations in firmware. Cancellation is not rollback and a missing acknowledgement/readback leaves the write outcome unknown.",
		"Readback compares the selected protocol identity; it does not prove that the same physical target remained in the field.",
	}
	if protocol == "em410x" && r.Source.Tag != 100 && r.Source.Tag != 104 {
		notes = append(notes, "The T55xx writer uses its normal EM410x format; source clock/subtype metadata is not reproduced.")
	}
	plan = LFPhysicalWritePlan{Schema: 1, SourceID: r.Source.ID, SourceTag: r.Source.Tag, Protocol: protocol, Record: record, Commands: uniqueCommands(commands), WriteCommand: write, Readback: readback, FirmwareEncodingValidation: firmwareValidation, Notes: notes}
	return plan, nil
}

// IdentityInput maps the standard card to the existing CloneLF codec. It does
// not issue a command. ioProx will be checked by the firmware decoder on execute.
func (r LFPhysicalWriteRequest) IdentityInput() (LFIdentityInput, error) {
	protocol, ok := LFCardProtocol(r.Source.Tag)
	if !ok || protocol != r.Protocol {
		return LFIdentityInput{}, fmt.Errorf("source and explicit LF protocol disagree")
	}
	id, err := guiUIDBytes(r.Source.UID)
	if err != nil {
		return LFIdentityInput{}, err
	}
	input := LFIdentityInput{Protocol: protocol, TagType: r.Source.Tag, ID: append(HexBytes(nil), id...)}
	if protocol == "ioprox" {
		if len(id) != 16 {
			return input, fmt.Errorf("ioProx source identity must contain sixteen bytes")
		}
		input.IOProx = &IOProxID{Version: id[0], Facility: id[1], Number: binary.BigEndian.Uint16(id[2:4]), Raw: append(HexBytes(nil), id[4:12]...)}
	}
	return input, nil
}

func (r LFPhysicalWriteRequest) Requirements() ([]Command, error) {
	plan, err := r.Plan()
	if err != nil {
		return nil, err
	}
	return plan.Commands, nil
}
