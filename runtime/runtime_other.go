//go:build !linux
// +build !linux

package runtime

import (
	"fmt"

	"docksmith/manifest"
)

func RunIsolated(rootDir string, command []string, workdir string, envVars []string, useShell bool) (int, error) {
	return 1, fmt.Errorf("docksmith runtime requires Linux (namespaces + chroot)")
}

func ChildProcess(args []string) error {
	return fmt.Errorf("docksmith runtime requires Linux (namespaces + chroot)")
}

func AssembleFilesystem(m *manifest.Manifest) (string, error) {
	return "", fmt.Errorf("docksmith runtime requires Linux (namespaces + chroot)")
}

func Run(name, tag string, envOverrides []string, cmdOverride []string) (int, error) {
	return 1, fmt.Errorf("docksmith runtime requires Linux (namespaces + chroot)")
}

