package lima

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Runner runs one limactl command and returns its standard output. On
// failure the error carries Lima's last log line.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Exec returns a Runner for the limactl binary at path. A non-empty home
// is passed as LIMA_HOME. Each command gets its own process group, so a
// signal aimed at the agent (Ctrl-C, launchd stopping the job) does not
// also hit a limactl that is starting a VM; `limactl start` detaches the
// VM's host agent, which keeps running when the agent dies.
func Exec(path, home string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, path, append([]string{"--tty=false"}, args...)...) //nolint:gosec // G204: argv, not a shell; names are validated
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = os.Environ()
		if home != "" {
			cmd.Env = append(cmd.Env, "LIMA_HOME="+home)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return stdout.Bytes(), fmt.Errorf("limactl %s: %w: %s", strings.Join(args, " "), err, lastLog(stderr.String()))
		}
		return stdout.Bytes(), nil
	}
}

// FindLimactl returns path if set, else limactl from PATH, else the
// Homebrew locations (launchd jobs get a PATH without them).
func FindLimactl(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	if p, err := exec.LookPath("limactl"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/limactl", "/usr/local/bin/limactl"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("lima: limactl not found on PATH or in Homebrew; install it with `brew install lima` or set lima.limactl")
}

// Lima puts each instance's sockets in its directory, and a UNIX socket
// path must be shorter than 104 bytes on macOS. The longest is
// <LIMA_HOME>/<instance>/ssh.sock.<16 digits>.
const (
	maxSocketPath = 103
	sockSuffix    = "/ssh.sock.1234567890123456"
	// MaxHomeLen is the longest LIMA_HOME that fits an instance named
	// after a 36-character UUID.
	MaxHomeLen = maxSocketPath - len(sockSuffix) - len("/kdk-") - 36
)

// ResolveHome returns the LIMA_HOME limactl uses (home, else $LIMA_HOME,
// else ~/.lima) with symlinks resolved, as Lima resolves them, and an
// error if it leaves no room for Kondukter's instance names.
func ResolveHome(home string) (string, error) {
	if home == "" {
		home = os.Getenv("LIMA_HOME")
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("lima: %w", err)
		}
		home = filepath.Join(h, ".lima")
	}
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	if len(home) > MaxHomeLen {
		return home, fmt.Errorf("lima: LIMA_HOME %s is %d bytes; at most %d leave room for Lima's sockets under kdk-<uuid>, so set lima.home to a shorter path", home, len(home), MaxHomeLen)
	}
	return home, nil
}

// lastLog extracts the message of the last log line limactl printed.
func lastLog(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if i := strings.Index(last, `msg="`); i >= 0 {
		msg := last[i+5:]
		if j := strings.LastIndex(msg, `"`); j >= 0 {
			msg = msg[:j]
		}
		last = strings.ReplaceAll(msg, `\"`, `"`)
	}
	if len(last) > 300 {
		last = last[:300] + "…"
	}
	return last
}

// instance is one entry of `limactl list --json`.
type instance struct {
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Dir          string            `json:"dir"`
	Param        map[string]string `json:"param"`
	HostAgentPID int               `json:"hostAgentPID"`
	Config       struct {
		PortForwards []struct {
			GuestPort int    `json:"guestPort"`
			HostPort  int    `json:"hostPort"`
			Proto     string `json:"proto"`
			Ignore    bool   `json:"ignore"`
		} `json:"portForwards"`
	} `json:"config"`
}

// Lima's instance statuses.
const (
	statusRunning = "Running"
	statusStopped = "Stopped"
)

// parseList reads limactl's list output: one JSON object per line.
func parseList(out []byte) ([]instance, error) {
	var list []instance
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var in instance
		if err := json.Unmarshal(line, &in); err != nil {
			return nil, fmt.Errorf("lima: parse limactl list: %w", err)
		}
		list = append(list, in)
	}
	return list, sc.Err()
}
