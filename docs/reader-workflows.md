# Reader workflows and saved-card editing

The reader window includes **Complete reads, keys and physical-card operations**
(the Chinese UI label is `完整读取、密钥与实体卡操作`). Select an operation, fill
its JSON template, then check capabilities. Templates contain placeholders;
replace them with observed identity, explicit keys, and actual backups. They do
not select a device or supply default credentials.

All device operations use the current serialized session and background tasks.
Results can be queried after a page refresh while the same server process is
running. Task history can import confirmed read data without repeating the
reader command. A server restart discards its task history; export results and
save imported cards for durable storage.

## Reads, repair and conversion

- [Type2 workflows](type2-workflows.md) identify modeled geometry, support full
  and authenticated reads, and retain masks for hidden or unread bytes.
- [Classic repair](classic-repair.md) provides full/selected rereads, candidate
  verification, explicit dictionary checks and bounded recovery helpers. Merge
  and selected-block import create new drafts and retain source evidence.
- [LF conversion](lf-promotion.md) maps supported protocol identities to standard
  cards. Diagnostic Pyramid/EM4x05 captures remain drafts.
- [CLI capture and conversion](reader-cli.md) uses the same read envelopes and
  conversion checks. Existing legacy commands retain their formats.

Select a saved draft in the card library before conversion. Classic requires a
successful full-read authority, matching captured identity and known keys.
Type2 requires complete modeled public memory and explicit emulator-only
credential assignment for unreadable secret bytes. LF requires a complete
present-card observation and a supported protocol. The original draft remains
in the library. These checks establish a usable saved representation; they do
not authenticate a chip or establish compatibility with every reader.

For Classic selected-block results, export the workflow result and import it
under **Import selected Classic repair** (`导入 Classic 指定块补读`). Preview
identity and conflicts, set `confirm_same_card` and byte resolutions in the
merge options, then apply to a new draft. A partial/error observation is never
silently upgraded to a successful full read.

## Editing and recovery

The editor shows memory by zero-based block/page address and lists changed
rows. Classic access-condition editing updates only the three access bytes;
keys and GPB remain unchanged. Unknown bytes cannot be filled by typing.
Use explicit verified rereads to add missing evidence.

Ordinary LF cards have a structured protocol input. `protocol` and `tag_type`
must preserve the saved card type. HID fields and PAC ASCII identities use host
codecs; ioProx composition and raw validation use the connected device firmware.
Encoding updates a saved file, without writing RF or emulator memory.

**Export editor draft** captures the source card plus pending name and text.
Import that file through **Restore exported editor draft**. Recovery requires
the saved source to match the captured source exactly. Changed sources are
refused; invalid pending text remains editable and must pass server validation
before saving. Imported/restored editor text is not persisted until **Save**.
Saved library mutations continue through the existing IndexedDB autosave,
conflict detection and failure handling.

A standard card's **Open in slot loader** action copies the card into the slot
loader and opens that window. Review slot, persistence and activation options,
then execute separately. Drafts remain unavailable, and a running slot operation
prevents replacement of its input.

## Controlled physical writes

Generate a plan and inspect its target, range and data. Enable the confirmation
checkbox, then execute once. The plan expires after five minutes and is bound
to the server instance, current session and exact request. Parameter changes,
connection changes and an execution attempt invalidate the browser's plan.
The server repeats live checks; a browser preview grants no permission.

- Type2 writes cover one modeled ordinary user page with expected original
  bytes. Manufacturer, lock, configuration and password/PACK pages are excluded.
- Classic writes require an explicit target backup and key map. Block 0 is
  always excluded; trailer writes require an additional explicit request.
- [LF writes](lf-physical-write.md) target an isolated dedicated T55xx tag and
  verify only the selected protocol identity. Password/full-memory verification
  and unique physical-target binding are unavailable.

Writes require `serve --allow-writes`. A failed or interrupted write can have an
unknown result. Query the task and independently inspect the card before any
new decision. The application does not replay writes, reconnect automatically,
restore a backup automatically or fall back to native USB. Pairing changes,
full-device restore and DFU retain their native maintenance restrictions.

## Qualification

See [hardware acceptance](hardware-acceptance.md) for the tested environment,
repeatable steps and remaining hardware gaps. Protocol simulations cover
failure paths; they do not replace tests on real card models and firmware.
