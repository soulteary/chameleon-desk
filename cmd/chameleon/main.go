package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func autoPort() (string, error) {
	ports, e := chameleon.Ports()
	if e != nil {
		return "", e
	}
	var candidates []string
	for _, p := range ports {
		b := filepath.Base(p)
		if strings.HasPrefix(b, "cu.usbmodem") || strings.HasPrefix(b, "ttyACM") {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("found %d USB CDC candidates; use devices, then --port explicitly", len(candidates))
	}
	return candidates[0], nil
}

type report struct {
	Started   string          `json:"started"`
	Port      string          `json:"port"`
	Requested int             `json:"requested_rounds"`
	Completed int             `json:"completed_rounds"`
	Reopens   int             `json:"reopens"`
	Elapsed   string          `json:"elapsed"`
	MinMS     float64         `json:"min_round_ms"`
	P50MS     float64         `json:"p50_round_ms"`
	P95MS     float64         `json:"p95_round_ms"`
	MaxMS     float64         `json:"max_round_ms"`
	Stats     chameleon.Stats `json:"transport"`
	Failure   string          `json:"failure,omitempty"`
	First     *chameleon.Info `json:"first,omitempty"`
	Last      *chameleon.Info `json:"last,omitempty"`
}

func addStats(a *chameleon.Stats, b chameleon.Stats) {
	a.Requests += b.Requests
	a.Responses += b.Responses
	a.RejectedFrames += b.RejectedFrames
	a.UnexpectedResponses += b.UnexpectedResponses
}
func stress(ctx context.Context, port string, timeout, interval time.Duration, count, reopen int) (r report, err error) {
	start := time.Now()
	r = report{Started: start.Format(time.RFC3339), Port: port, Requested: count}
	var c *chameleon.Client
	var durations []float64
	defer func() {
		if c != nil {
			addStats(&r.Stats, c.Stats())
			_ = c.Close()
		}
		r.Elapsed = time.Since(start).String()
		if err != nil {
			r.Failure = err.Error()
		}
		sort.Float64s(durations)
		if len(durations) > 0 {
			r.MinMS = durations[0]
			r.P50MS = durations[(len(durations)-1)/2]
			r.P95MS = durations[(len(durations)-1)*95/100]
			r.MaxMS = durations[len(durations)-1]
		}
	}()
	for i := 0; i < count; i++ {
		if e := ctx.Err(); e != nil {
			return r, e
		}
		if c == nil {
			c, err = chameleon.OpenContext(ctx, port, timeout)
			if err != nil {
				return r, err
			}
		}
		t := time.Now()
		info, e := c.Info(ctx)
		if e != nil {
			return r, fmt.Errorf("round %d: %w", i+1, e)
		}
		durations = append(durations, float64(time.Since(t))/float64(time.Millisecond))
		if r.First == nil {
			first := info
			r.First = &first
		} else if info.ModelID != r.First.ModelID || info.Firmware != r.First.Firmware || info.GitVersion != r.First.GitVersion || !reflect.DeepEqual(info.Capabilities, r.First.Capabilities) {
			return r, fmt.Errorf("round %d: device identity, version or capabilities changed", i+1)
		}
		r.Last = &info
		r.Completed++
		if reopen > 0 && (i+1)%reopen == 0 && i+1 < count {
			addStats(&r.Stats, c.Stats())
			if e = c.Close(); e != nil {
				return r, e
			}
			c = nil
			r.Reopens++
		}
		if i+1 < count && interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return r, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return r, nil
}
func run() error {
	if isLibrary(os.Args[1:]) {
		return runLibrary(os.Args[1:])
	}
	if isFirmwareUpdate(os.Args[1:]) {
		return runFirmwareUpdate(os.Args[1:])
	}
	if isCardTool(os.Args[1:]) {
		return runCardTool(os.Args[1:])
	}
	if isDeviceTool(os.Args[1:]) {
		return runDeviceTool(os.Args[1:])
	}
	if isMappedWorkflow(os.Args[1:]) {
		return runMappedWorkflow(os.Args[1:])
	}
	if len(os.Args) > 1 && os.Args[1] == "data" {
		return runSamples(os.Args[2:])
	}
	if isWorkflow(os.Args[1:]) {
		return runWorkflow(os.Args[1:])
	}
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		return runBackup(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "database" {
		return runDatabase(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "cardfile" {
		return runCardfile(os.Args[2:])
	}
	if len(os.Args) < 2 {
		return errors.New("usage: chameleon devices|info|slots|scan|stress|commands|call|settings|slot|mode|emulator|lf|mfc|type2|raw|apdu|doctor|recover|image|sniff|firmware|t55|ble|cardfile|database|ndef|acl|vanity|dictionary|gen4|gen1a|gen3|backup|ulc|rf|seos|emv|des [--port PATH] [--json]")
	}
	command := os.Args[1]
	if command == "version" || command == "--version" {
		fmt.Printf("Chameleon Desk %s\nupstream %s\n", chameleon.SDKVersion, chameleon.UpstreamCommit)
		return nil
	}
	if extendedCommand(command) {
		return runExtended(os.Args[1:])
	}
	if command == "--help" || command == "-h" || command == "help" {
		fmt.Println("Usage: chameleon devices|info|slots|scan|stress|commands|call|settings|slot|mode|emulator|lf|mfc|type2|raw|apdu|doctor|recover|image|sniff|firmware|t55|ble|cardfile|database|ndef|acl|vanity|dictionary|gen4|gen1a|gen3|backup|ulc|rf|seos|emv|des [options]\nRun chameleon <command> --help for options.")
		return nil
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	port := f.String("port", "", "serial port; auto-selects one USB CDC candidate")
	timeout := f.Duration("timeout", 3*time.Second, "timeout per command, including queue wait")
	jsonOut := f.Bool("json", false, "JSON output")
	count, interval, reopen := 100, 20*time.Millisecond, 0
	output, wait, poll := "", time.Duration(0), 250*time.Millisecond
	if command == "stress" {
		f.IntVar(&count, "count", 100, "stress rounds (9 commands per round)")
		f.DurationVar(&interval, "interval", 20*time.Millisecond, "pause between stress rounds")
		f.IntVar(&reopen, "reconnect-every", 0, "close/reopen serial connection every N stress rounds")
	}
	if command == "scan" || command == "stress" {
		f.StringVar(&output, "output", "", "save result as JSON")
	}
	if command == "scan" {
		f.DurationVar(&wait, "wait", 0, "wait for a card; finish the current scan before stopping")
		f.DurationVar(&poll, "poll-interval", 250*time.Millisecond, "delay between no-card retries")
	}

	if e := f.Parse(os.Args[2:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", f.Args())
	}
	if *timeout <= 0 || count <= 0 || interval < 0 || reopen < 0 || wait < 0 || poll <= 0 {
		return errors.New("timeout/count must be positive; interval/reconnect-every must be non-negative")
	}
	if e := checkNewOutput(output); e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if command == "devices" {
		ports, e := chameleon.Ports()
		if e != nil {
			return e
		}
		if *jsonOut {
			return printJSON(ports)
		}
		for _, p := range ports {
			fmt.Println(p)
		}
		return nil
	}
	if command != "info" && command != "slots" && command != "scan" && command != "stress" {
		return fmt.Errorf("unknown command %q", command)
	}
	if *port == "" {
		p, e := autoPort()
		if e != nil {
			return e
		}
		*port = p
	}
	if command == "stress" {
		r, e := stress(ctx, *port, *timeout, interval, count, reopen)
		if output != "" {
			b, je := json.MarshalIndent(r, "", "  ")
			if je != nil {
				return je
			}
			if je = saveResultFile(output, append(b, '\n')); je != nil {
				return je
			}
		}
		if *jsonOut {
			if je := printJSON(r); je != nil {
				return je
			}
		} else {
			fmt.Printf("%d/%d rounds, %d requests, %d responses, %d rejected frames, %d unexpected responses\nElapsed %s; round p50 %.2f ms, p95 %.2f ms, max %.2f ms; reopens %d\n", r.Completed, r.Requested, r.Stats.Requests, r.Stats.Responses, r.Stats.RejectedFrames, r.Stats.UnexpectedResponses, r.Elapsed, r.P50MS, r.P95MS, r.MaxMS, r.Reopens)
		}
		return e
	}
	c, e := chameleon.OpenContext(ctx, *port, *timeout)
	if e != nil {
		return e
	}
	defer c.Close()
	if command == "scan" {
		cards, err := scanCards(ctx, c, wait, poll)
		if err != nil {
			return err
		}
		if output != "" {
			b, e := json.MarshalIndent(cards, "", "  ")
			if e != nil {
				return e
			}
			if e = saveResultFile(output, append(b, '\n')); e != nil {
				return e
			}
		}
		if *jsonOut {
			return printJSON(cards)
		}
		for i, card := range cards {
			fmt.Printf("Card %d\n  UID: %s (%d bytes)\n  ATQA: 0x%04X (wire: %s)\n  SAK: 0x%02X\n  Type: %s\n  Basis: %s\n", i+1, card.UID, len(card.UID), card.ATQA, card.ATQAWire, card.SAK, card.Type, card.TypeBasis)
			if len(card.ATS) > 0 {
				fmt.Printf("  ATS: %s\n", card.ATS)
			}
		}
		return nil
	}
	if command == "slots" {
		s, e := c.Slots(ctx)
		if e != nil {
			return e
		}
		if *jsonOut {
			return printJSON(s)
		}
		printSlots(s)
		return nil
	}
	info, e := c.Info(ctx)
	if e != nil {
		return e
	}
	if *jsonOut {
		return printJSON(info)
	}
	fmt.Printf("Port: %s\nModel: %s\nFirmware: %s (%s)\nMode: %s\nBattery: %d%% (%d mV)\nSupported commands: %d\n", *port, info.Model, info.Firmware, info.GitVersion, info.Mode, info.Battery.Percent, info.Battery.Millivolts, len(info.Capabilities))
	printSlots(info.Slots)
	return nil
}
func printSlots(slots []chameleon.Slot) {
	for _, s := range slots {
		mark := " "
		if s.Active {
			mark = "*"
		}
		fmt.Printf("%s Slot %d: HF=%s (enabled=%t), LF=%s (enabled=%t)\n", mark, s.Number, s.HFName, s.HFEnabled, s.LFName, s.LFEnabled)
	}
}

type cardScanner interface {
	ScanISO14443A(context.Context) ([]chameleon.Card, error)
}

func scanCards(ctx context.Context, c cardScanner, wait, poll time.Duration) ([]chameleon.Card, error) {
	if wait == 0 {
		return c.ScanISO14443A(ctx)
	}
	deadline := time.Now().Add(wait)
	for {
		// Do not turn the polling deadline into a mid-command cancellation:
		// let the current scan finish and restore the mode before stopping.
		cards, err := c.ScanISO14443A(ctx)
		if err == nil {
			return cards, nil
		}
		var restore *chameleon.ModeRestoreError
		if !errors.Is(err, chameleon.ErrNoCard) || errors.As(err, &restore) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("no card detected within %s: %w", wait, chameleon.ErrNoCard)
		}
		timer := time.NewTimer(min(poll, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no card detected within %s: %w", wait, chameleon.ErrNoCard)
		}
	}
}
