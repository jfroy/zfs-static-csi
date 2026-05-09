package driver

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
	mount "k8s.io/mount-utils"
)

// csiMounter wraps a standard mount.Interface and overrides Mount/Unmount to
// call mount(2)/umount(2) directly. mount-utils' default Mount shells out to
// /bin/mount and some of its Unmount paths shell out to /bin/umount; neither
// is present in the distroless runtime image. The remaining methods
// (IsLikelyNotMountPoint, /proc/mounts parsing) are already syscall- or
// file-based, so they work as-is.
type csiMounter struct {
	mount.Interface
}

func newMounter() *csiMounter {
	return &csiMounter{Interface: mount.New("")}
}

// Mount issues a bind mount via the mount(2) syscall. The driver only ever
// asks for bind mounts (NodePublishVolume), so we require the "bind" option.
func (m *csiMounter) Mount(source, target, fstype string, options []string) error {
	flags, data, err := parseBindOptions(options)
	if err != nil {
		return err
	}
	// Per-mount flags (MS_RDONLY, MS_NOSUID, …) are not honored on the
	// initial MS_BIND on kernels < 5.12; apply them via a follow-up remount.
	if err := unix.Mount(source, target, fstype, unix.MS_BIND, data); err != nil {
		return fmt.Errorf("bind mount %s -> %s: %w", source, target, err)
	}
	if flags != 0 {
		if err := unix.Mount("", target, "", unix.MS_BIND|unix.MS_REMOUNT|flags, data); err != nil {
			_ = unix.Unmount(target, 0)
			return fmt.Errorf("remount %s with options %v: %w", target, options, err)
		}
	}
	return nil
}

// Unmount calls umount(2) directly. mount-utils' Mounter.Unmount is already
// syscall-based, but its UnmountWithForce / forceUmount paths shell out to
// /bin/umount; overriding here keeps any future code path using this mounter
// off the binary.
func (m *csiMounter) Unmount(target string) error {
	if err := unix.Unmount(target, 0); err != nil {
		return &os.PathError{Op: "unmount", Path: target, Err: err}
	}
	return nil
}

func parseBindOptions(options []string) (uintptr, string, error) {
	var (
		flags uintptr
		bind  bool
		data  []string
	)
	for _, o := range options {
		switch o {
		case "bind":
			bind = true
		case "ro":
			flags |= unix.MS_RDONLY
		case "rw":
			flags &^= unix.MS_RDONLY
		case "nosuid":
			flags |= unix.MS_NOSUID
		case "suid":
			flags &^= unix.MS_NOSUID
		case "nodev":
			flags |= unix.MS_NODEV
		case "dev":
			flags &^= unix.MS_NODEV
		case "noexec":
			flags |= unix.MS_NOEXEC
		case "exec":
			flags &^= unix.MS_NOEXEC
		case "noatime":
			flags |= unix.MS_NOATIME
		case "atime":
			flags &^= unix.MS_NOATIME
		case "nodiratime":
			flags |= unix.MS_NODIRATIME
		case "diratime":
			flags &^= unix.MS_NODIRATIME
		case "relatime":
			flags |= unix.MS_RELATIME
		case "strictatime":
			flags |= unix.MS_STRICTATIME
		default:
			// Filesystem-specific or unknown options flow through via data.
			// The kernel mostly ignores these on bind mounts.
			data = append(data, o)
		}
	}
	if !bind {
		return 0, "", fmt.Errorf("csiMounter requires the 'bind' option; got %v", options)
	}
	return flags, strings.Join(data, ","), nil
}
