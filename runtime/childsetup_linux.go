//go:build linux

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// childSetupEnv carries the opt-in setup (userns.go) from the parent to
// the child, which removes it before running the command.
const childSetupEnv = "DOCKSMITH_CHILD_SETUP"

var devNodes = []string{"null", "zero", "full", "random", "urandom"}

// withChildSetup adds the setup the flags asked for to the child's environment.
func withChildSetup(env []string) []string {
	var parts []string
	if ChildLoopback {
		parts = append(parts, "loopback")
	}
	if ChildDev {
		parts = append(parts, "dev")
	}
	if ChildUser != "" {
		parts = append(parts, "user="+ChildUser)
	}
	if len(parts) == 0 {
		return env
	}
	return append(append([]string{}, env...), childSetupEnv+"="+strings.Join(parts, ","))
}

type childSetup struct {
	loopback, dev bool
	uid, gid      int
	user          bool
}

// takeChildSetup reads and removes the setup from the environment.
func takeChildSetup() (childSetup, error) {
	var s childSetup
	v, ok := os.LookupEnv(childSetupEnv)
	if !ok {
		return s, nil
	}
	os.Unsetenv(childSetupEnv)
	for _, p := range strings.Split(v, ",") {
		switch {
		case p == "loopback":
			s.loopback = true
		case p == "dev":
			s.dev = true
		case strings.HasPrefix(p, "user="):
			u, g, ok := strings.Cut(strings.TrimPrefix(p, "user="), ":")
			uid, err1 := strconv.Atoi(u)
			gid, err2 := strconv.Atoi(g)
			if !ok || err1 != nil || err2 != nil || uid < 0 || gid < 0 {
				return s, fmt.Errorf("bad --user %q: want uid:gid", p)
			}
			s.user, s.uid, s.gid = true, uid, gid
		default:
			return s, fmt.Errorf("unknown container setup %q", p)
		}
	}
	return s, nil
}

// beforeChroot does what needs the host's view: device bind mounts and lo.
func (s childSetup) beforeChroot(rootDir string) error {
	if s.dev {
		for _, n := range devNodes {
			dst := filepath.Join(rootDir, "dev", n)
			if f, err := os.OpenFile(dst, os.O_CREATE|os.O_RDONLY, 0o666); err == nil {
				f.Close()
			}
			if err := syscall.Mount("/dev/"+n, dst, "", syscall.MS_BIND, ""); err != nil {
				return fmt.Errorf("bind /dev/%s: %w", n, err)
			}
		}
	}
	if s.user {
		// The root is Docksmith's private temporary copy, created 0700;
		// another user couldn't even reach /bin.
		if err := os.Chmod(rootDir, 0o755); err != nil {
			return fmt.Errorf("open the root for --user: %w", err)
		}
		// Layers don't keep modes like /tmp's 1777, nor owners; give the
		// user a world-writable /tmp, as every image expects.
		tmp := filepath.Join(rootDir, "tmp")
		if err := os.MkdirAll(tmp, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0o1777); err != nil {
			return fmt.Errorf("make /tmp writable for --user: %w", err)
		}
	}
	if s.loopback {
		if err := loopbackUp(); err != nil {
			return fmt.Errorf("bring lo up: %w", err)
		}
	}
	return nil
}

// afterChroot switches to the requested user, while the child still has
// the capabilities to do it.
func (s childSetup) afterChroot() error {
	if !s.user {
		return nil
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(s.gid); err != nil {
		return fmt.Errorf("setgid %d: %w", s.gid, err)
	}
	if err := syscall.Setuid(s.uid); err != nil {
		return fmt.Errorf("setuid %d: %w", s.uid, err)
	}
	return nil
}

// loopbackUp sets IFF_UP on lo with SIOCSIFFLAGS.
func loopbackUp() error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var ifr struct {
		name  [syscall.IFNAMSIZ]byte
		flags uint16
		_     [22]byte
	}
	copy(ifr.name[:], "lo")
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifr))); e != 0 {
		return e
	}
	ifr.flags |= syscall.IFF_UP | syscall.IFF_RUNNING
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifr))); e != 0 {
		return e
	}
	return nil
}
