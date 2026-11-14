package chameleon

import (
	"context"
	"fmt"
)

// WriteT55 writes an encoded LF identifier to a T55xx using explicit known
// passwords. No default or guessed password list is supplied by the SDK.
func (c *Client) WriteT55(ctx context.Context, protocol string, id, newPassword []byte, oldPasswords [][]byte) error {
	cmds := map[string]Command{"em410x": CmdEm410xWriteToT55xx, "electra": CmdEm410xElectraWriteToT55xx, "hidprox": CmdHidproxWriteToT55xx, "ioprox": CmdIoproxWriteToT55xx, "viking": CmdVikingWriteToT55xx, "pac": CmdPacWriteToT55xx, "jablotron": CmdJablotronWriteToT55xx, "idteck": CmdIdteckWriteToT55xx}
	sizes := map[string]int{"em410x": 5, "electra": 13, "hidprox": 13, "ioprox": 16, "viking": 4, "pac": 8, "jablotron": 5, "idteck": 8}
	cmd, ok := cmds[protocol]
	if !ok {
		return fmt.Errorf("unknown T55 protocol %q", protocol)
	}
	if len(id) != sizes[protocol] || len(newPassword) != 4 || len(oldPasswords) < 1 {
		return fmt.Errorf("invalid ID length or missing four-byte passwords")
	}
	if len(id)+4+len(oldPasswords)*4 > MaxPayload {
		return fmt.Errorf("too many passwords")
	}
	payload := append([]byte(nil), id...)
	payload = append(payload, newPassword...)
	for _, key := range oldPasswords {
		if len(key) != 4 {
			return fmt.Errorf("each old password requires four bytes")
		}
		payload = append(payload, key...)
	}
	return c.Reader(ctx, func(s *ReaderSession) error { _, e := s.expect(ctx, cmd, payload, 0x40); return e })
}
