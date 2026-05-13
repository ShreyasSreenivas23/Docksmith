//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"docksmith/layer"
	"docksmith/manifest"
	"docksmith/store"
)

// RunIsolated executes a command inside an isolated filesystem root.
// This SAME function is used for both RUN during build AND docksmith run.
func RunIsolated(rootDir string, command []string, workdir string, envVars []string, useShell bool) (int, error) {
	if workdir == "" {
		workdir = "/"
	}

	// Ensure critical dirs exist in root.
	os.MkdirAll(filepath.Join(rootDir, "proc"), 0755)
	os.MkdirAll(filepath.Join(rootDir, "dev"), 0755)
	os.MkdirAll(filepath.Join(rootDir, workdir), 0755)

	// Re-exec self with __child__ to enter new namespaces.
	var cmd *exec.Cmd
	if useShell {
		cmd = exec.Command("/proc/self/exe", append([]string{"__child__", rootDir, workdir, "shell"}, command...)...)
	} else {
		cmd = exec.Command("/proc/self/exe", append([]string{"__child__", rootDir, workdir, "exec"}, command...)...)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = envVars

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNS |
			syscall.CLONE_NEWUTS |
			syscall.CLONE_NEWNET,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, fmt.Errorf("run isolated command: %w", err)
	}
	return 0, nil
}

// ChildProcess is called when the binary is re-exec'd inside a new namespace.
// It sets up chroot and mounts, then execs the actual command.
func ChildProcess(args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("child process requires at least 4 args: rootDir, workdir, mode, command...")
	}

	rootDir := args[0]
	workdir := args[1]
	mode := args[2]
	cmdArgs := args[3:]

	// Mount proc inside the new root.
	procPath := filepath.Join(rootDir, "proc")
	os.MkdirAll(procPath, 0755)
	syscall.Mount("proc", procPath, "proc", 0, "")

	// Chroot into the rootDir.
	if err := syscall.Chroot(rootDir); err != nil {
		return fmt.Errorf("chroot to %s: %w", rootDir, err)
	}

	// Change to the working directory.
	if err := syscall.Chdir(workdir); err != nil {
		return fmt.Errorf("chdir to %s: %w", workdir, err)
	}

	// Set hostname.
	syscall.Sethostname([]byte("docksmith"))

	// Execute the command.
	var cmd *exec.Cmd
	if mode == "shell" {
		shellCmd := strings.Join(cmdArgs, " ")
		cmd = exec.Command("/bin/sh", "-c", shellCmd)
	} else {
		cmd = exec.Command(cmdArgs[0], cmdArgs[1:]...)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// AssembleFilesystem extracts all layers in order into a temp directory.
func AssembleFilesystem(m *manifest.Manifest) (string, error) {
	tmpDir, err := os.MkdirTemp("", "docksmith-run-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	digests := make([]string, len(m.Layers))
	for i, l := range m.Layers {
		digests[i] = l.Digest
	}

	if err := layer.ExtractLayers(tmpDir, digests); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("extract layers: %w", err)
	}

	return tmpDir, nil
}

// Run executes a container from an image (called by `docksmith run`).
func Run(name, tag string, envOverrides []string, cmdOverride []string) (int, error) {
	m, err := store.LoadManifest(name, tag)
	if err != nil {
		return 1, err
	}

	// Determine command.
	cmd := m.Config.Cmd
	if len(cmdOverride) > 0 {
		cmd = cmdOverride
	}
	if len(cmd) == 0 {
		return 1, fmt.Errorf("no CMD defined in image %s:%s and no command given", name, tag)
	}

	// Build environment: image ENV + overrides.
	envMap := make(map[string]string)
	for _, e := range m.Config.Env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	for _, e := range envOverrides {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	var envVars []string
	for k, v := range envMap {
		envVars = append(envVars, k+"="+v)
	}
	if _, ok := envMap["PATH"]; !ok {
		envVars = append(envVars, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}

	// Assemble filesystem.
	rootDir, err := AssembleFilesystem(m)
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(rootDir)

	workdir := m.Config.WorkingDir
	if workdir == "" {
		workdir = "/"
	}

	return RunIsolated(rootDir, cmd, workdir, envVars, false)
}
