package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"docksmith/builder"
	"docksmith/manifest"
	"docksmith/parser"
	"docksmith/runtime"
	"docksmith/store"
)

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	// Handle the child process re-exec for namespace isolation.
	if args[0] == "__child__" {
		if err := runtime.ChildProcess(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "child process error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	command := args[0]
	switch command {
	case "build":
		if err := runBuild(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "run":
		exitCode, err := runRun(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Container exited with code %d\n", exitCode)
		os.Exit(exitCode)
	case "images":
		if err := runImages(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "rmi":
		if err := runRmi(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "import":
		if err := runImport(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Docksmith - A simplified Docker-like build and runtime system

Usage:
  docksmith <command> [options]

Commands:
  build     Build an image from a Docksmithfile
  run       Run a container from an image
  images    List all images
  rmi       Remove an image
  import    Import a base image from a rootfs tarball
  help      Show this help message

Use "docksmith <command> --help" for more information about a command.`)
}

// ─── BUILD ──────────────────────────────────────────────────────────────────

func runBuild(args []string) error {
	var (
		name    string
		tag     string
		noCache bool
		context string
	)

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-t":
			if i+1 >= len(args) {
				return fmt.Errorf("-t requires a name:tag argument")
			}
			i++
			name, tag = parseNameTag(args[i])
		case "--no-cache":
			noCache = true
		case "--help", "-h":
			fmt.Println(`Usage: docksmith build -t <name:tag> [--no-cache] <context>

Build an image from a Docksmithfile.

Options:
  -t name:tag    Name and tag for the image (required)
  --no-cache     Skip all cache lookups and writes`)
			return nil
		default:
			if context == "" {
				context = args[i]
			} else {
				return fmt.Errorf("unexpected argument: %s", args[i])
			}
		}
		i++
	}

	if name == "" {
		return fmt.Errorf("missing -t flag: use -t name:tag")
	}
	if context == "" {
		return fmt.Errorf("missing build context directory")
	}

	absContext, err := filepath.Abs(context)
	if err != nil {
		return fmt.Errorf("resolve context path: %w", err)
	}

	instructions, err := parser.Parse(absContext)
	if err != nil {
		return fmt.Errorf("parse Docksmithfile: %w", err)
	}

	return builder.Build(instructions, builder.BuildOptions{
		Name:       name,
		Tag:        tag,
		ContextDir: absContext,
		NoCache:    noCache,
	})
}

// ─── RUN ────────────────────────────────────────────────────────────────────

func runRun(args []string) (int, error) {
	var (
		envOverrides []string
		imageRef     string
		cmdOverride  []string
	)

	i := 0
	for i < len(args) {
		switch {
		case args[i] == "-e":
			if i+1 >= len(args) {
				return 1, fmt.Errorf("-e requires KEY=VALUE argument")
			}
			i++
			if !strings.Contains(args[i], "=") {
				return 1, fmt.Errorf("-e requires KEY=VALUE format, got: %s", args[i])
			}
			envOverrides = append(envOverrides, args[i])
		case args[i] == "--help" || args[i] == "-h":
			fmt.Println(`Usage: docksmith run [-e KEY=VALUE]... <name:tag> [cmd...]

Run a container from an image.

Options:
  -e KEY=VALUE   Override or add an environment variable (repeatable)`)
			return 0, nil
		case imageRef == "":
			imageRef = args[i]
		default:
			cmdOverride = append(cmdOverride, args[i])
		}
		i++
	}

	if imageRef == "" {
		return 1, fmt.Errorf("missing image reference: use name:tag")
	}

	name, tag := parseNameTag(imageRef)
	return runtime.Run(name, tag, envOverrides, cmdOverride)
}

// ─── IMAGES ─────────────────────────────────────────────────────────────────

func runImages() error {
	manifests, err := store.ListManifests()
	if err != nil {
		return err
	}

	if len(manifests) == 0 {
		fmt.Println("No images found.")
		return nil
	}

	fmt.Printf("%-20s %-15s %-14s %s\n", "NAME", "TAG", "ID", "CREATED")
	fmt.Printf("%-20s %-15s %-14s %s\n", "----", "---", "--", "-------")

	for _, m := range manifests {
		shortID := manifest.ShortID(m)
		fmt.Printf("%-20s %-15s %-14s %s\n", m.Name, m.Tag, shortID, m.Created)
	}

	return nil
}

// ─── RMI ────────────────────────────────────────────────────────────────────

func runRmi(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing image reference: use name:tag")
	}

	if args[0] == "--help" || args[0] == "-h" {
		fmt.Println(`Usage: docksmith rmi <name:tag>

Remove an image manifest and all of its layer files from disk.`)
		return nil
	}

	name, tag := parseNameTag(args[0])

	if err := store.RemoveImage(name, tag); err != nil {
		return fmt.Errorf("remove image %s:%s: %w", name, tag, err)
	}

	fmt.Printf("Removed image %s:%s\n", name, tag)
	return nil
}

// ─── IMPORT ─────────────────────────────────────────────────────────────────

func runImport(args []string) error {
	var (
		name    string
		tag     string
		tarPath string
	)

	i := 0
	for i < len(args) {
		switch args[i] {
		case "-t":
			if i+1 >= len(args) {
				return fmt.Errorf("-t requires a name:tag argument")
			}
			i++
			name, tag = parseNameTag(args[i])
		case "--help", "-h":
			fmt.Println(`Usage: docksmith import -t <name:tag> <rootfs.tar>

Import a base image from a root filesystem tarball.`)
			return nil
		default:
			if tarPath == "" {
				tarPath = args[i]
			} else {
				return fmt.Errorf("unexpected argument: %s", args[i])
			}
		}
		i++
	}

	if name == "" {
		return fmt.Errorf("missing -t flag: use -t name:tag")
	}
	if tarPath == "" {
		return fmt.Errorf("missing rootfs tarball path")
	}

	if err := store.EnsureDirs(); err != nil {
		return err
	}

	srcData, err := os.ReadFile(tarPath)
	if err != nil {
		return fmt.Errorf("read tarball: %w", err)
	}

	normalizedTar, err := normalizeTar(srcData)
	if err != nil {
		return fmt.Errorf("normalise tarball: %w", err)
	}

	hash := sha256.Sum256(normalizedTar)
	digest := fmt.Sprintf("sha256:%x", hash)

	layerPath := store.LayerPath(digest)
	if _, err := os.Stat(layerPath); os.IsNotExist(err) {
		if err := os.WriteFile(layerPath, normalizedTar, 0644); err != nil {
			return fmt.Errorf("write layer: %w", err)
		}
	}

	fi, err := os.Stat(layerPath)
	if err != nil {
		return fmt.Errorf("stat layer: %w", err)
	}

	m := &manifest.Manifest{
		Name:    name,
		Tag:     tag,
		Created: time.Now().UTC().Format(time.RFC3339),
		Config: manifest.Config{
			Env:        []string{},
			Cmd:        []string{},
			WorkingDir: "",
		},
		Layers: []manifest.Layer{
			{
				Digest:    digest,
				Size:      fi.Size(),
				CreatedBy: fmt.Sprintf("import %s", filepath.Base(tarPath)),
			},
		},
	}

	if err := store.SaveManifest(m); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}

	fmt.Printf("Imported %s:%s (layer %s, %d bytes)\n", name, tag, digest[:19], fi.Size())
	return nil
}

// ─── HELPERS ────────────────────────────────────────────────────────────────

func parseNameTag(nameTag string) (string, string) {
	for i := len(nameTag) - 1; i >= 0; i-- {
		if nameTag[i] == ':' {
			return nameTag[:i], nameTag[i+1:]
		}
	}
	return nameTag, "latest"
}

func normalizeTar(srcData []byte) ([]byte, error) {
	type tarEntry struct {
		Header *tar.Header
		Data   []byte
	}

	tr := tar.NewReader(bytes.NewReader(srcData))
	var entries []tarEntry

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		cleanHdr := &tar.Header{
			Typeflag: hdr.Typeflag,
			Name:     filepath.ToSlash(hdr.Name),
			Linkname: hdr.Linkname,
			Size:     hdr.Size,
			Mode:     hdr.Mode,
			Uid:      0,
			Gid:      0,
			ModTime:  time.Time{},
		}

		cleanHdr.Name = strings.TrimPrefix(cleanHdr.Name, "./")
		if cleanHdr.Name == "" || cleanHdr.Name == "." {
			continue
		}

		var data []byte
		if hdr.Size > 0 {
			data = make([]byte, hdr.Size)
			if _, err := io.ReadFull(tr, data); err != nil {
				return nil, fmt.Errorf("read file data for %s: %w", hdr.Name, err)
			}
		}

		entries = append(entries, tarEntry{Header: cleanHdr, Data: data})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Header.Name < entries[j].Header.Name
	})

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, entry := range entries {
		if err := tw.WriteHeader(entry.Header); err != nil {
			return nil, fmt.Errorf("write header for %s: %w", entry.Header.Name, err)
		}
		if len(entry.Data) > 0 {
			if _, err := tw.Write(entry.Data); err != nil {
				return nil, fmt.Errorf("write data for %s: %w", entry.Header.Name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}

	return buf.Bytes(), nil
}
