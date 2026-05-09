// Package zfs is a thin wrapper around the `zfs` command-line tool.
package zfs

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// A dataset is exposable iff ShareProperty is set to ShareValueOn.
const (
	ShareProperty = "com.github.jfroy.zfs-static-csi:share"
	ShareValueOn  = "on"
)

type Dataset struct {
	Name       string
	Type       string
	Mountpoint string
	Mounted    bool
	ShareValue string
}

func (d *Dataset) IsShared() bool {
	return d.ShareValue == ShareValueOn
}

// NotFoundError covers both "dataset does not exist" and "dataset is in a pool
// not imported on this node" — `zfs get` cannot tell them apart.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("dataset %q does not exist on this node", e.Name)
}

// Runner is the test seam for canning `zfs` output.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

type Options struct {
	// ChrootDir, when non-empty, runs zfs(8) chrooted into this directory so
	// the binary, its libraries, and /dev/zfs come from the host filesystem
	// (ABI-matched with the host kernel module).
	ChrootDir string
}

type Client struct {
	run Runner
}

func NewClient(opts Options) *Client {
	return &Client{run: makeRunner(opts.ChrootDir)}
}

func makeRunner(chrootDir string) Runner {
	if chrootDir == "" {
		return func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "zfs", args...).CombinedOutput()
		}
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "/usr/sbin/zfs", args...)
		// Chroot is applied between fork and execve, so the binary path and
		// its dynamic linker / libs all resolve against chrootDir.
		cmd.SysProcAttr = &syscall.SysProcAttr{Chroot: chrootDir}
		// Parent CWD may not exist inside the new root; pin to "/".
		cmd.Dir = "/"
		return cmd.CombinedOutput()
	}
}

func (c *Client) GetDataset(ctx context.Context, name string) (*Dataset, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	out, err := c.run(ctx, "get", "-H", "-p", "-o", "property,value",
		"type,mountpoint,mounted,"+ShareProperty, name)
	if err != nil {
		s := string(out)
		if strings.Contains(s, "dataset does not exist") {
			return nil, &NotFoundError{Name: name}
		}
		return nil, fmt.Errorf("zfs get %s: %w: %s", name, err, strings.TrimSpace(s))
	}
	return parseDataset(name, string(out))
}

func parseDataset(name, out string) (*Dataset, error) {
	ds := &Dataset{Name: name}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "type":
			ds.Type = parts[1]
		case "mountpoint":
			ds.Mountpoint = parts[1]
		case "mounted":
			ds.Mounted = parts[1] == "yes"
		case ShareProperty:
			ds.ShareValue = parts[1]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse zfs output for %s: %w", name, err)
	}
	return ds, nil
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("dataset name is empty")
	}
	if len(name) > 256 {
		return fmt.Errorf("dataset name exceeds 256 characters")
	}
	if name[0] == '/' || name[0] == '-' {
		return fmt.Errorf("dataset name cannot start with %q", name[0])
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("dataset name cannot contain '..'")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/', r == '-', r == '_', r == '.', r == ':':
		default:
			return fmt.Errorf("dataset name contains invalid character %q", r)
		}
	}
	return nil
}
