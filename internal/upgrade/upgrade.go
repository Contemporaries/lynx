package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const defaultRepo = "Contemporaries/lynx"

type Status struct {
	State   string `json:"state"` // idle|checking|downloading|verifying|installing|restarting|done|error
	Tag     string `json:"tag,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Updated string `json:"updated,omitempty"`
}

type Manager struct {
	mu     sync.Mutex
	status Status
	Repo   string
	Binary string // lynx-server or lynx-client
	Unit   string // systemd unit name
}

func New(binary, unit string) *Manager {
	return &Manager{
		status: Status{State: "idle"},
		Repo:   defaultRepo,
		Binary: binary,
		Unit:   unit,
	}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) set(s Status) {
	m.mu.Lock()
	s.Updated = time.Now().UTC().Format(time.RFC3339)
	m.status = s
	m.mu.Unlock()
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func (m *Manager) Latest(ctx context.Context, tag string) (string, error) {
	rel, err := m.fetchRelease(ctx, tag)
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

func (m *Manager) fetchRelease(ctx context.Context, tag string) (*releaseInfo, error) {
	repo := m.Repo
	if repo == "" {
		repo = defaultRepo
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	if tag != "" && tag != "latest" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("github api %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func assetName(binary string) (string, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported arch %s", arch)
	}
	if osName == "windows" {
		return fmt.Sprintf("%s-windows-%s.exe", binary, arch), nil
	}
	if osName != "linux" {
		return "", fmt.Errorf("unsupported os %s", osName)
	}
	return fmt.Sprintf("%s-linux-%s", binary, arch), nil
}

// Start begins an async upgrade; returns immediately.
func (m *Manager) Start(ctx context.Context, tag string, afterInstall func() error) error {
	m.mu.Lock()
	if m.status.State != "idle" && m.status.State != "done" && m.status.State != "error" {
		m.mu.Unlock()
		return fmt.Errorf("upgrade already in progress")
	}
	m.mu.Unlock()
	go m.run(ctx, tag, afterInstall)
	return nil
}

func (m *Manager) run(ctx context.Context, tag string, afterInstall func() error) {
	m.set(Status{State: "checking", Message: "querying GitHub release"})
	rel, err := m.fetchRelease(ctx, tag)
	if err != nil {
		m.set(Status{State: "error", Error: err.Error()})
		return
	}
	name, err := assetName(m.Binary)
	if err != nil {
		m.set(Status{State: "error", Error: err.Error(), Tag: rel.TagName})
		return
	}
	var assetURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case name:
			assetURL = a.BrowserDownloadURL
		case "SHA256SUMS":
			sumsURL = a.BrowserDownloadURL
		}
	}
	if assetURL == "" || sumsURL == "" {
		m.set(Status{State: "error", Tag: rel.TagName, Error: fmt.Sprintf("release missing %s or SHA256SUMS", name)})
		return
	}

	tmp, err := os.MkdirTemp("", "lynx-upgrade-*")
	if err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}
	defer os.RemoveAll(tmp)

	m.set(Status{State: "downloading", Tag: rel.TagName, Message: name})
	binPath := filepath.Join(tmp, name)
	if err := download(ctx, assetURL, binPath); err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}
	sumsPath := filepath.Join(tmp, "SHA256SUMS")
	if err := download(ctx, sumsURL, sumsPath); err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}

	m.set(Status{State: "verifying", Tag: rel.TagName})
	if err := verifySHA256(binPath, sumsPath, name); err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}

	m.set(Status{State: "installing", Tag: rel.TagName})
	installPath, err := os.Executable()
	if err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}
	installPath, err = filepath.EvalSymlinks(installPath)
	if err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}
	backupDir := "/var/backups/lynx"
	_ = os.MkdirAll(backupDir, 0o755)
	stamp := time.Now().Format("20060102150405")
	_ = copyFile(installPath, filepath.Join(backupDir, filepath.Base(installPath)+"."+stamp))

	if err := replaceBinary(binPath, installPath); err != nil {
		m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
		return
	}

	m.set(Status{State: "restarting", Tag: rel.TagName})
	if afterInstall != nil {
		if err := afterInstall(); err != nil {
			m.set(Status{State: "error", Tag: rel.TagName, Error: err.Error()})
			return
		}
	}
	m.set(Status{State: "done", Tag: rel.TagName, Message: "upgrade complete"})
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func verifySHA256(binPath, sumsPath, assetName string) error {
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	var want string
	for _, line := range strings.Split(string(sums), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "  "+assetName) || strings.HasSuffix(line, " *"+assetName) {
			want = strings.Fields(line)[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("checksum for %s not found", assetName)
	}
	f, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func replaceBinary(src, dest string) error {
	dir := filepath.Dir(dest)
	tmp := filepath.Join(dir, "."+filepath.Base(dest)+".new")
	if err := copyFile(src, tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// RestartService tries systemctl restart, else returns ErrNeedSelfRestart.
func RestartService(unit string) error {
	if unit == "" {
		return ErrNeedSelfRestart
	}
	if os.Getenv("INVOCATION_ID") == "" && os.Getenv("NOTIFY_SOCKET") == "" {
		// Still try systemctl if unit file exists.
	}
	cmd := exec.Command("systemctl", "restart", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %w (%s)", unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

var ErrNeedSelfRestart = fmt.Errorf("self restart required")
