package chameleon

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Upstream firmware locations used by the GUI "check for updates" flow.
const (
	firmwareAPIBase     = "https://api.github.com/repos/RfidResearchGroup/ChameleonUltra"
	firmwareNightlyBase = "https://nightly.link/RfidResearchGroup/ChameleonUltra"
	firmwareUpstreamID  = 581338100 // repository ID; rejects artifacts built from forks
	firmwareMaxBytes    = 4 << 20
	firmwareMaxJSON     = 8 << 20
)

// FirmwareUpdater talks to GitHub the way the upstream GUI does. It sends no
// credentials and nothing about the device; only the model picks the asset.
type FirmwareUpdater struct {
	APIBase     string
	NightlyBase string
	Client      *http.Client
}

func NewFirmwareUpdater() *FirmwareUpdater {
	return &FirmwareUpdater{APIBase: firmwareAPIBase, NightlyBase: firmwareNightlyBase, Client: &http.Client{Timeout: 60 * time.Second}}
}

// FirmwareBuild is one downloadable application DFU package.
type FirmwareBuild struct {
	Model      string `json:"model"`
	Asset      string `json:"asset"`
	Commit     string `json:"commit"`
	Source     string `json:"source"` // "actions" (via nightly.link) or "release" (github.com)
	Tag        string `json:"tag,omitempty"`
	RunID      int64  `json:"run_id,omitempty"`
	ArtifactID int64  `json:"artifact_id,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	URL        string `json:"url"`
	Bytes      int64  `json:"bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"` // digest published by GitHub; releases only
}

type FirmwareUpdateStatus struct {
	DeviceVersion string        `json:"device_version"`
	DeviceCommit  string        `json:"device_commit"`
	Dirty         bool          `json:"dirty"`
	Latest        FirmwareBuild `json:"latest"`
	UpToDate      bool          `json:"up_to_date"`
	Notes         []string      `json:"notes,omitempty"`
}

// FirmwareModel maps the device model ID or a name to the upstream asset prefix.
func FirmwareModel(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ultra", "chameleon ultra", "0":
		return "ultra", nil
	case "lite", "chameleon lite", "1":
		return "lite", nil
	}
	return "", fmt.Errorf("firmware model must be ultra or lite")
}

func (u *FirmwareUpdater) get(ctx context.Context, link string, limit int64) ([]byte, error) {
	target, e := url.Parse(link)
	if e != nil || target.Scheme != "https" && target.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("refusing non-HTTPS firmware URL")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "chameleon-go/"+SDKVersion)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream, */*")
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: response exceeds %d bytes", target.Host, limit)
	}
	if resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &m) == nil && m.Message != "" {
			return nil, fmt.Errorf("%s: HTTP %d: %s", target.Host, resp.StatusCode, m.Message)
		}
		return nil, fmt.Errorf("%s: HTTP %d", target.Host, resp.StatusCode)
	}
	return b, nil
}

type firmwareArtifact struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size_in_bytes"`
	Expired   bool   `json:"expired"`
	CreatedAt string `json:"created_at"`
	Run       *struct {
		ID         int64  `json:"id"`
		Branch     string `json:"head_branch"`
		SHA        string `json:"head_sha"`
		Repository int64  `json:"head_repository_id"`
	} `json:"workflow_run"`
}

type firmwareRelease struct {
	Tag        string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Commit     string `json:"target_commitish"`
	Published  string `json:"published_at"`
	Author     struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		Digest  string `json:"digest"`
		Updated string `json:"updated_at"`
		URL     string `json:"browser_download_url"`
	} `json:"assets"`
}

// ActionsBuild returns the newest non-expired artifact built from upstream
// main. Run IDs follow build order; artifact IDs and created_at do not.
func (u *FirmwareUpdater) ActionsBuild(ctx context.Context, model string) (FirmwareBuild, error) {
	model, e := FirmwareModel(model)
	if e != nil {
		return FirmwareBuild{}, e
	}
	name := model + "-dfu-app"
	b, e := u.get(ctx, u.APIBase+"/actions/artifacts?per_page=100&name="+name, firmwareMaxJSON)
	if e != nil {
		return FirmwareBuild{}, e
	}
	var list struct {
		Artifacts []firmwareArtifact `json:"artifacts"`
	}
	if e = json.Unmarshal(b, &list); e != nil {
		return FirmwareBuild{}, e
	}
	var best *firmwareArtifact
	for i := range list.Artifacts {
		a := &list.Artifacts[i]
		if a.Name != name || a.Expired || a.Run == nil || a.Run.Branch != "main" || a.Run.Repository != firmwareUpstreamID {
			continue
		}
		if best == nil || a.Run.ID > best.Run.ID {
			best = a
		}
	}
	if best == nil {
		return FirmwareBuild{}, fmt.Errorf("no current %s artifact built from upstream main", name)
	}
	if !isCommit(best.Run.SHA) {
		return FirmwareBuild{}, fmt.Errorf("artifact has an invalid commit")
	}
	return FirmwareBuild{Model: model, Asset: name + ".zip", Commit: best.Run.SHA, Source: "actions", RunID: best.Run.ID, ArtifactID: best.ID, CreatedAt: best.CreatedAt,
		URL: fmt.Sprintf("%s/actions/artifacts/%d.zip", u.NightlyBase, best.ID), Bytes: best.Size}, nil
}

// ReleaseBuild returns the asset of one release. An empty tag selects the
// rolling pre-release published by github-actions[bot], as the GUI does.
func (u *FirmwareUpdater) ReleaseBuild(ctx context.Context, model, tag string) (FirmwareBuild, error) {
	model, e := FirmwareModel(model)
	if e != nil {
		return FirmwareBuild{}, e
	}
	b, e := u.get(ctx, u.APIBase+"/releases", firmwareMaxJSON)
	if e != nil {
		return FirmwareBuild{}, e
	}
	var releases []firmwareRelease
	if e = json.Unmarshal(b, &releases); e != nil {
		return FirmwareBuild{}, e
	}
	name := model + "-dfu-app.zip"
	for _, r := range releases {
		if tag == "" && !(r.Prerelease && r.Author.Login == "github-actions[bot]") || tag != "" && r.Tag != tag {
			continue
		}
		for _, a := range r.Assets {
			if a.Name != name {
				continue
			}
			out := FirmwareBuild{Model: model, Asset: name, Commit: r.Commit, Source: "release", Tag: r.Tag, CreatedAt: r.Published, URL: a.URL, Bytes: a.Size}
			if a.Updated != "" {
				out.CreatedAt = a.Updated // the rolling pre-release keeps its first publish date
			}
			if digest, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && len(digest) == 64 {
				out.SHA256 = strings.ToLower(digest)
			}
			return out, nil
		}
		if tag != "" {
			return FirmwareBuild{}, fmt.Errorf("release %s has no %s", tag, name)
		}
	}
	if tag != "" {
		return FirmwareBuild{}, fmt.Errorf("release %s not found", tag)
	}
	return FirmwareBuild{}, fmt.Errorf("no pre-release with %s", name)
}

// LatestBuild follows the GUI: the newest main artifact, else the rolling
// pre-release. When that pre-release is the same commit it is preferred,
// because GitHub serves it directly and publishes a digest for it.
func (u *FirmwareUpdater) LatestBuild(ctx context.Context, model string) (FirmwareBuild, error) {
	actions, actionsErr := u.ActionsBuild(ctx, model)
	release, releaseErr := u.ReleaseBuild(ctx, model, "")
	switch {
	case actionsErr == nil && releaseErr == nil && release.Commit == actions.Commit:
		return release, nil
	case actionsErr == nil:
		return actions, nil
	case releaseErr == nil:
		return release, nil
	}
	return FirmwareBuild{}, fmt.Errorf("no firmware build found: %v; %v", actionsErr, releaseErr)
}

func isCommit(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	_, e := hex.DecodeString(s + strings.Repeat("0", len(s)%2))
	return e == nil
}

// ResolveFirmwareCommit turns the device git version into a commit:
// "v2.2.0-64-g5c99d4a[-dirty]" carries it, a bare tag is looked up upstream.
func (u *FirmwareUpdater) ResolveFirmwareCommit(ctx context.Context, version string) (commit string, dirty bool, e error) {
	version = strings.TrimSpace(version)
	version, dirty = strings.CutSuffix(version, "-dirty")
	parts := strings.Split(version, "-")
	if n := len(parts); n >= 3 && strings.HasPrefix(parts[n-1], "g") && isCommit(parts[n-1][1:]) {
		return strings.ToLower(parts[n-1][1:]), dirty, nil
	}
	if isCommit(version) {
		return strings.ToLower(version), dirty, nil
	}
	if version == "" {
		return "", dirty, fmt.Errorf("empty firmware version")
	}
	b, e := u.get(ctx, u.APIBase+"/tags?per_page=100", firmwareMaxJSON)
	if e != nil {
		return "", dirty, e
	}
	var tags []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if e = json.Unmarshal(b, &tags); e != nil {
		return "", dirty, e
	}
	for _, t := range tags {
		if t.Name == version && isCommit(t.Commit.SHA) {
			return t.Commit.SHA, dirty, nil
		}
	}
	return "", dirty, fmt.Errorf("firmware version %q is not an upstream tag", version)
}

// CheckFirmwareUpdate compares the device git version with the latest build.
// It only says whether the commits differ; it cannot tell which one is newer.
func (u *FirmwareUpdater) CheckFirmwareUpdate(ctx context.Context, model, deviceVersion string) (FirmwareUpdateStatus, error) {
	out := FirmwareUpdateStatus{DeviceVersion: deviceVersion}
	var e error
	if out.DeviceCommit, out.Dirty, e = u.ResolveFirmwareCommit(ctx, deviceVersion); e != nil {
		return out, e
	}
	if out.Latest, e = u.LatestBuild(ctx, model); e != nil {
		return out, e
	}
	out.UpToDate = strings.HasPrefix(strings.ToLower(out.Latest.Commit), out.DeviceCommit)
	if out.Dirty {
		out.Notes = append(out.Notes, "device firmware was built from a modified tree; flashing upstream replaces local changes")
	}
	if !out.UpToDate {
		out.Notes = append(out.Notes, "commits differ; a self-built or newer-than-main firmware also shows as not up to date")
	}
	return out, nil
}

// FirmwareDownload describes a verified package written to disk.
type FirmwareDownload struct {
	Build          FirmwareBuild   `json:"build"`
	Output         string          `json:"output"`
	Bytes          int             `json:"bytes"`
	SHA256         string          `json:"sha256"`
	DigestVerified bool            `json:"digest_verified"`
	InitHash       string          `json:"init_hash"`
	Package        FirmwarePackage `json:"package"`
	Notes          []string        `json:"notes,omitempty"`
}

// DownloadFirmware fetches a build, checks it and writes it to a new file.
// An existing output is never replaced, and nothing is flashed. The package
// must be application-only, its init packet must be signed and carry the
// hash of the binary, and a release must match the digest GitHub publishes.
// The signature itself is checked by the device bootloader, not here.
func (u *FirmwareUpdater) DownloadFirmware(ctx context.Context, build FirmwareBuild, output string) (FirmwareDownload, error) {
	out := FirmwareDownload{Build: build, Output: output}
	if output == "" {
		return out, fmt.Errorf("firmware download requires an output file")
	}
	if _, e := os.Lstat(output); e == nil {
		return out, fmt.Errorf("%s already exists; choose a new output file", output)
	}
	data, e := u.get(ctx, build.URL, firmwareMaxBytes)
	if e != nil {
		return out, e
	}
	sum := sha256.Sum256(data)
	out.Bytes, out.SHA256 = len(data), hex.EncodeToString(sum[:])
	if build.SHA256 != "" {
		if out.SHA256 != build.SHA256 {
			return out, fmt.Errorf("download SHA256 %s does not match the published digest %s", out.SHA256, build.SHA256)
		}
		out.DigestVerified = true
	} else {
		out.Notes = append(out.Notes, "no published digest for this source; integrity rests on the signed init packet and the bootloader")
	}
	tmp, e := os.CreateTemp(filepath.Dir(output), ".chameleon-fw-*")
	if e != nil {
		return out, e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(data); e == nil {
		e = tmp.Sync()
	}
	if closeErr := tmp.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return out, e
	}
	if out.Package, e = InspectFirmware(tmp.Name()); e != nil {
		return out, fmt.Errorf("downloaded package rejected: %w", e)
	}
	image, e := ReadFirmwareApplication(tmp.Name())
	if e != nil {
		return out, fmt.Errorf("downloaded package rejected: %w", e)
	}
	if out.InitHash, e = VerifyFirmwareInit(image.Init, image.Firmware); e != nil {
		return out, fmt.Errorf("downloaded package rejected: %w", e)
	}
	// Link fails if the output appeared meanwhile, so nothing is overwritten.
	if e = os.Link(tmp.Name(), output); e != nil {
		return out, e
	}
	return out, nil
}

// pbField returns the last occurrence of one field in a protobuf message.
func pbField(b []byte, field uint64) (data []byte, value uint64, found bool, e error) {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, 0, false, fmt.Errorf("malformed init packet")
		}
		b = b[n:]
		var d []byte
		var v uint64
		switch key & 7 {
		case 0:
			if v, n = binary.Uvarint(b); n <= 0 {
				return nil, 0, false, fmt.Errorf("malformed init packet")
			}
			b = b[n:]
		case 2:
			size, n := binary.Uvarint(b)
			if n <= 0 || size > uint64(len(b)-n) {
				return nil, 0, false, fmt.Errorf("malformed init packet")
			}
			d, b = b[n:n+int(size)], b[n+int(size):]
		case 1, 5:
			size := 4
			if key&7 == 1 {
				size = 8
			}
			if len(b) < size {
				return nil, 0, false, fmt.Errorf("malformed init packet")
			}
			b = b[size:]
		default:
			return nil, 0, false, fmt.Errorf("malformed init packet")
		}
		if key>>3 == field {
			data, value, found = d, v, true
		}
	}
	return data, value, found, nil
}

// VerifyFirmwareInit checks what the GUI checks before flashing: the Nordic
// init packet is a signed init command whose hash matches the binary. It
// returns the hash type; it does not verify the signature.
func VerifyFirmwareInit(init, firmware []byte) (string, error) {
	step := func(b []byte, field uint64, what string) ([]byte, error) {
		d, _, ok, e := pbField(b, field)
		if e != nil {
			return nil, e
		}
		if !ok || len(d) == 0 {
			return nil, fmt.Errorf("init packet %s", what)
		}
		return d, nil
	}
	signed, e := step(init, 2, "is not signed")
	if e != nil {
		return "", e
	}
	if _, e = step(signed, 3, "has no signature"); e != nil {
		return "", e
	}
	command, e := step(signed, 1, "has no command")
	if e != nil {
		return "", e
	}
	initCommand, e := step(command, 2, "command has no init")
	if e != nil {
		return "", e
	}
	hash, e := step(initCommand, 8, "has no hash")
	if e != nil {
		return "", e
	}
	want, e := step(hash, 2, "has an empty hash")
	if e != nil {
		return "", e
	}
	_, kind, _, e := pbField(hash, 1)
	if e != nil {
		return "", e
	}
	var got []byte
	var name string
	switch kind {
	case 2:
		s := sha1.Sum(firmware)
		got, name = s[:], "sha1"
	case 3:
		s := sha256.Sum256(firmware)
		got, name = s[:], "sha256"
	case 4:
		s := sha512.Sum512(firmware)
		got, name = s[:], "sha512"
	default:
		return "", fmt.Errorf("init packet uses unsupported hash type %d", kind)
	}
	// Nordic stores the digest little-endian.
	reversed := make([]byte, len(want))
	for i := range want {
		reversed[len(want)-1-i] = want[i]
	}
	if !bytes.Equal(reversed, got) {
		return "", fmt.Errorf("init packet hash does not match the firmware binary")
	}
	return name, nil
}
