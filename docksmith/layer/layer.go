package layer

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

	"docksmith/store"
)

var zeroTime = time.Time{}

// CreateCopyLayer creates a tar layer for a COPY instruction.
func CreateCopyLayer(contextDir, src, dst, workdir string) ([]byte, string, error) {
	srcFiles, err := resolveSourceFiles(contextDir, src)
	if err != nil {
		return nil, "", fmt.Errorf("resolve source files: %w", err)
	}
	if len(srcFiles) == 0 {
		return nil, "", fmt.Errorf("COPY %s: no files matched", src)
	}

	destPath := dst
	if !filepath.IsAbs(destPath) {
		if workdir != "" {
			destPath = filepath.Join(workdir, destPath)
		} else {
			destPath = "/" + destPath
		}
	}
	destPath = filepath.ToSlash(destPath)

	sort.Strings(srcFiles)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	isDstDir := strings.HasSuffix(dst, "/") || len(srcFiles) > 1

	for _, srcFile := range srcFiles {
		relPath, err := filepath.Rel(contextDir, srcFile)
		if err != nil {
			relPath = filepath.Base(srcFile)
		}
		relPath = filepath.ToSlash(relPath)

		fi, err := os.Stat(srcFile)
		if err != nil {
			return nil, "", fmt.Errorf("stat source file %s: %w", srcFile, err)
		}

		var tarPath string
		if isDstDir {
			tarPath = destPath + "/" + filepath.Base(relPath)
		} else {
			tarPath = destPath
		}
		tarPath = filepath.ToSlash(tarPath)
		tarPath = strings.TrimPrefix(tarPath, "/")

		if fi.IsDir() {
			// We intentionally do not write directory headers for destination parents.
			// This keeps WORKDIR "silent creation" from being stored as a tar delta.
			err = addDirFilesToTar(tw, srcFile, tarPath)
			if err != nil {
				return nil, "", err
			}
			continue
		}

		data, err := os.ReadFile(srcFile)
		if err != nil {
			return nil, "", fmt.Errorf("read source file %s: %w", srcFile, err)
		}
		hdr := &tar.Header{
			Name:    tarPath,
			Size:    int64(len(data)),
			Mode:    int64(fi.Mode().Perm()),
			ModTime: zeroTime,
			Uid:     0,
			Gid:     0,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", fmt.Errorf("write tar header: %w", err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, "", fmt.Errorf("write tar data: %w", err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, "", fmt.Errorf("close tar: %w", err)
	}

	tarBytes := buf.Bytes()
	digest := computeDigest(tarBytes)
	return tarBytes, digest, nil
}

// CreateRunLayer creates a delta tar of files added/modified by a RUN command.
func CreateRunLayer(rootDir string, beforeSnapshot map[string]string) ([]byte, string, error) {
	afterFiles := make(map[string]os.FileInfo)
	var afterPaths []string

	filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "." {
			return nil
		}
		if relPath == "proc" || strings.HasPrefix(relPath, "proc/") ||
			relPath == "dev" || strings.HasPrefix(relPath, "dev/") ||
			relPath == "sys" || strings.HasPrefix(relPath, "sys/") {
			return nil
		}
		afterFiles[relPath] = info
		afterPaths = append(afterPaths, relPath)
		return nil
	})

	sort.Strings(afterPaths)
	var changedPaths []string
	for _, p := range afterPaths {
		info := afterFiles[p]
		if info.IsDir() {
			if _, existed := beforeSnapshot[p]; !existed {
				changedPaths = append(changedPaths, p)
			}
			continue
		}
		beforeHash, existed := beforeSnapshot[p]
		if !existed {
			changedPaths = append(changedPaths, p)
			continue
		}
		fullPath := filepath.Join(rootDir, filepath.FromSlash(p))
		afterHash, err := hashFileContents(fullPath)
		if err != nil {
			continue
		}
		if afterHash != beforeHash {
			changedPaths = append(changedPaths, p)
		}
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	addedDirs := make(map[string]bool)

	for _, p := range changedPaths {
		info := afterFiles[p]
		fullPath := filepath.Join(rootDir, filepath.FromSlash(p))

		if info.IsDir() {
			if !addedDirs[p] {
				hdr := &tar.Header{
					Typeflag: tar.TypeDir,
					Name:     p + "/",
					Mode:     0755,
					ModTime:  zeroTime,
					Uid:      0,
					Gid:      0,
				}
				tw.WriteHeader(hdr)
				addedDirs[p] = true
			}
			continue
		}

		ensureParentDirs(tw, p, addedDirs)

		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		hdr := &tar.Header{
			Name:    p,
			Size:    int64(len(data)),
			Mode:    int64(info.Mode().Perm()),
			ModTime: zeroTime,
			Uid:     0,
			Gid:     0,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, "", err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, "", err
	}

	tarBytes := buf.Bytes()
	digest := computeDigest(tarBytes)
	return tarBytes, digest, nil
}

// SnapshotDir creates a map of relPath -> content hash (or "dir" for dirs).
func SnapshotDir(rootDir string) (map[string]string, error) {
	snapshot := make(map[string]string)
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "." {
			return nil
		}
		if relPath == "proc" || strings.HasPrefix(relPath, "proc/") ||
			relPath == "dev" || strings.HasPrefix(relPath, "dev/") ||
			relPath == "sys" || strings.HasPrefix(relPath, "sys/") {
			return nil
		}
		if info.IsDir() {
			snapshot[relPath] = "dir"
		} else {
			hash, err := hashFileContents(path)
			if err != nil {
				snapshot[relPath] = ""
			} else {
				snapshot[relPath] = hash
			}
		}
		return nil
	})
	return snapshot, err
}

// StoreTar writes tar bytes to the layer store.
func StoreTar(tarBytes []byte, digest string) error {
	if err := store.EnsureDirs(); err != nil {
		return err
	}
	path := store.LayerPath(digest)
	if _, err := os.Stat(path); err == nil {
		return nil // immutable — already exists
	}
	return os.WriteFile(path, tarBytes, 0644)
}

// ExtractLayers extracts layer tars in order into a target directory.
func ExtractLayers(targetDir string, digests []string) error {
	for _, digest := range digests {
		tarPath := store.LayerPath(digest)
		if err := extractTar(tarPath, targetDir); err != nil {
			return fmt.Errorf("extract layer %s: %w", digest, err)
		}
	}
	return nil
}

func extractTar(tarPath, targetDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("open tar %s: %w", tarPath, err)
	}
	defer f.Close()

	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") {
			continue
		}
		target := filepath.Join(targetDir, cleanName)

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, os.FileMode(hdr.Mode))
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", target, err)
			}
			io.Copy(outFile, tr)
			outFile.Close()
		case tar.TypeSymlink:
			os.Remove(target)
			os.Symlink(hdr.Linkname, target)
		case tar.TypeLink:
			linkTarget := filepath.Join(targetDir, filepath.Clean(hdr.Linkname))
			os.Remove(target)
			os.Link(linkTarget, target)
		}
	}
	return nil
}

func resolveSourceFiles(contextDir, src string) ([]string, error) {
	if strings.ContainsAny(src, "*?[]") {
		// Support both '*' and recursive '**' globs as required by the spec.
		matches, err := globUnderDir(contextDir, src)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", src, err)
		}
		return matches, nil
	}

	fullPath := filepath.Join(contextDir, src)
	fi, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("source %s not found: %w", src, err)
	}

	if fi.IsDir() {
		var files []string
		filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			relPath, _ := filepath.Rel(fullPath, path)
			relPath = filepath.ToSlash(relPath)
			if relPath == "." {
				return nil
			}
			base := filepath.Base(path)
			if base == "Docksmithfile" || base == ".git" || base == ".docksmith" {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !info.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		return files, nil
	}

	return []string{fullPath}, nil
}

func addDirFilesToTar(tw *tar.Writer, srcDir, tarBase string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		childPath := filepath.Join(srcDir, name)
		childTarPath := tarBase + "/" + name
		if tarBase == "" {
			childTarPath = name
		}

		info, err := os.Stat(childPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if err := addDirFilesToTar(tw, childPath, childTarPath); err != nil {
				return err
			}
			continue
		}

		data, err := os.ReadFile(childPath)
		if err != nil {
			continue
		}
		hdr := &tar.Header{
			Name:    filepath.ToSlash(childTarPath),
			Size:    int64(len(data)),
			Mode:    int64(info.Mode().Perm()),
			ModTime: zeroTime,
			Uid:     0,
			Gid:     0,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func ensureParentDirs(tw *tar.Writer, entryPath string, addedDirs map[string]bool) {
	parts := strings.Split(entryPath, "/")
	for i := 1; i < len(parts); i++ {
		dirPath := strings.Join(parts[:i], "/")
		if dirPath == "" {
			continue
		}
		if !addedDirs[dirPath] {
			hdr := &tar.Header{
				Typeflag: tar.TypeDir,
				Name:     dirPath + "/",
				Mode:     0755,
				ModTime:  zeroTime,
				Uid:      0,
				Gid:      0,
			}
			_ = tw.WriteHeader(hdr)
			addedDirs[dirPath] = true
		}
	}
}

// globUnderDir matches files under baseDir against a pattern that may include '**'.
// Pattern syntax is the same as filepath.Match for segments, plus:
// - '**' matches zero or more path segments.
func globUnderDir(baseDir, pattern string) ([]string, error) {
	pat := filepath.ToSlash(pattern)
	pat = strings.TrimPrefix(pat, "./")

	var matches []string
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		ok, err := matchGlob(pat, rel)
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func matchGlob(pattern, path string) (bool, error) {
	pSegs := splitPathSegments(pattern)
	sSegs := splitPathSegments(path)
	return matchSegments(pSegs, sSegs)
}

func splitPathSegments(s string) []string {
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func matchSegments(pat, segs []string) (bool, error) {
	if len(pat) == 0 {
		return len(segs) == 0, nil
	}

	if pat[0] == "**" {
		// '**' matches zero or more segments.
		// Try zero segments first (greedy vs non-greedy doesn't matter for boolean match).
		if ok, err := matchSegments(pat[1:], segs); err != nil {
			return false, err
		} else if ok {
			return true, nil
		}
		for i := 0; i < len(segs); i++ {
			if ok, err := matchSegments(pat[1:], segs[i+1:]); err != nil {
				return false, err
			} else if ok {
				return true, nil
			}
		}
		return false, nil
	}

	if len(segs) == 0 {
		return false, nil
	}

	ok, err := filepath.Match(pat[0], segs[0])
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return matchSegments(pat[1:], segs[1:])
}

func computeDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", hash)
}

func hashFileContents(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// GetSourceFileHashes returns sorted map of file path -> content hash for COPY cache keys.
func GetSourceFileHashes(contextDir, src string) (map[string]string, error) {
	files, err := resolveSourceFiles(contextDir, src)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	hashes := make(map[string]string)
	for _, f := range files {
		relPath, err := filepath.Rel(contextDir, f)
		if err != nil {
			relPath = f
		}
		relPath = filepath.ToSlash(relPath)
		hash, err := hashFileContents(f)
		if err != nil {
			return nil, fmt.Errorf("hash file %s: %w", f, err)
		}
		hashes[relPath] = hash
	}
	return hashes, nil
}
