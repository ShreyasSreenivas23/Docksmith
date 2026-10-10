//go:build linux
// +build linux

package runtime_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"docksmith/runtime"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__child__" {
		if err := runtime.ChildProcess(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "child process error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func copyReexecBinary(t *testing.T) string {
	src, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "isol-reexec")
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open executable: %v", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		t.Fatalf("create copy: %v", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatalf("copy executable: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close copy: %v", err)
	}
	return dst
}

func copyHostFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func populateShellRootfs(root, shPath string) error {
	if err := copyHostFile(shPath, filepath.Join(root, "bin", "sh")); err != nil {
		return err
	}
	// Dynamic /bin/sh (dash/bash) needs the loader and libc inside the chroot.
	for _, lib := range []string{
		"/lib/x86_64-linux-gnu/libc.so.6",
		"/lib64/ld-linux-x86-64.so.2",
		"/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2",
	} {
		if _, err := os.Stat(lib); err != nil {
			continue
		}
		dst := filepath.Join(root, strings.TrimPrefix(lib, "/"))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := copyHostFile(lib, dst); err != nil {
			return err
		}
	}
	return nil
}

func TestRunIsolatedBasic(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root for chroot/namespaces")
	}
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("requires /bin/sh")
	}

	reexec := copyReexecBinary(t)
	runtime.SetReexecBinary(reexec)
	defer runtime.SetReexecBinary("")

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	shPath, err := exec.LookPath("/bin/sh")
	if err != nil {
		t.Fatalf("LookPath /bin/sh: %v", err)
	}
	if err := populateShellRootfs(root, shPath); err != nil {
		t.Fatalf("populate shell rootfs: %v", err)
	}

	run := func() (int, error) {
		return runtime.RunIsolated(root, []string{"echo isolated"}, "/", []string{"PATH=/bin"}, true)
	}

	code, err := run()
	if err != nil && isNamespaceExecError(err) {
		t.Logf("user namespace re-exec unavailable in go test (%v); retrying without CLONE_NEWUSER", err)
		runtime.SetUseUserNamespace(false)
		defer runtime.SetUseUserNamespace(true)
		code, err = run()
	}
	if err != nil {
		t.Fatalf("RunIsolated: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
}

func isNamespaceExecError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "permission denied")
}
