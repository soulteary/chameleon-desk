package chameleon

import (
	"context"
	"fmt"
)

// EditRequirements resolves only the codec commands used by a saved-card edit.
// Host codecs require no firmware. ioProx fields/raw IDs require a firmware
// composition/decoding response; length alone never validates the encoding.
func (input LFIdentityInput) EditRequirements() ([]Command, error) {
	protocol, ok := LFCardProtocol(input.TagType)
	if !ok || protocol != input.Protocol {
		return nil, fmt.Errorf("LF edit requires matching saved protocol and tag type")
	}
	if protocol != "ioprox" {
		if _, err := EncodeLFIdentityOffline(input); err != nil {
			return nil, err
		}
		return []Command{}, nil
	}
	if len(input.ID) != 0 && len(input.ID) != 16 {
		return nil, fmt.Errorf("ioProx encoded identity must contain sixteen bytes")
	}
	if input.IOProx == nil {
		if len(input.ID) != 16 {
			return nil, fmt.Errorf("ioProx edit requires encoded identity or structured fields")
		}
		return []Command{CmdIoproxDecodeRaw}, nil
	}
	if len(input.IOProx.Raw) > 0 {
		if len(input.IOProx.Raw) != 8 {
			return nil, fmt.Errorf("ioProx raw identity must contain eight bytes")
		}
		return []Command{CmdIoproxDecodeRaw}, nil
	}
	return []Command{CmdIoproxComposeId}, nil
}

// EditLFIdentityOffline applies a host-validated structured identity to an
// ordinary saved card. Firmware-only codecs require Client.EditLFIdentity.
func (c GUICard) EditLFIdentityOffline(name string, input LFIdentityInput) (GUICard, error) {
	if err := c.validateLFIdentityEdit(input); err != nil {
		return c, err
	}
	record, err := EncodeLFIdentityOffline(input)
	if err != nil {
		return c, err
	}
	return c.applyLFIdentity(name, record)
}
func (c GUICard) validateLFIdentityEdit(input LFIdentityInput) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.RequireCardImage(); err != nil {
		return err
	}
	protocol, ok := LFCardProtocol(c.Tag)
	if !ok {
		return fmt.Errorf("card has no supported LF editor")
	}
	if input.Protocol != protocol || input.TagType != c.Tag {
		return fmt.Errorf("structured identity must preserve the saved LF protocol and tag type")
	}
	return nil
}
func (c GUICard) applyLFIdentity(name string, record LFRecord) (GUICard, error) {
	protocol, ok := LFCardProtocol(c.Tag)
	encodedProtocol := record.Protocol
	if c.Tag == 104 && encodedProtocol == "electra" {
		encodedProtocol = "em410x"
	}
	if !ok || protocol != encodedProtocol || (record.TagType != 0 && record.TagType != c.Tag && !(record.TagType == 100 && c.Tag >= 100 && c.Tag <= 103)) {
		return c, fmt.Errorf("encoded identity changes card type")
	}
	if _, err := DecodeLFRecord(protocol, c.Tag, record.ID); err != nil {
		return c, err
	}
	c.Name = name
	c.UID = record.ID.String()
	return c, c.Validate()
}

// EditLFIdentity validates firmware-backed ioProx fields without writing RF or
// emulator memory. It returns a file edit; persistence remains explicit.
func (client *Client) EditLFIdentity(ctx context.Context, card GUICard, name string, input LFIdentityInput) (GUICard, error) {
	if err := card.validateLFIdentityEdit(input); err != nil {
		return card, err
	}
	if _, err := input.EditRequirements(); err != nil {
		return card, err
	}
	// An encoded ioProx ID includes decoded fields and an eight-byte raw
	// frame. The host parser cannot prove that relationship. Always validate
	// ID-only edits with the firmware decoder before treating them as saved IDs.
	if input.Protocol == "ioprox" && input.IOProx == nil {
		if len(input.ID) != 16 {
			return card, fmt.Errorf("ioProx editing requires structured fields or a complete 16-byte encoded identity")
		}
		input.IOProx = &IOProxID{Raw: append(HexBytes(nil), input.ID[4:12]...)}
	}
	record, err := client.EncodeLFIdentity(ctx, input)
	if err != nil {
		return card, err
	}
	return card.applyLFIdentity(name, record)
}
