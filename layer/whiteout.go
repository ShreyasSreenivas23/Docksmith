package layer

import (
	"strings"
)

// Docker-style whiteout prefixes for hiding lower-layer files during extraction.
const (
	WhiteoutPrefix    = ".wh."
	OpaqueWhiteout    = ".wh..wh..opq"
	WhiteoutDirPrefix = ".wh..wh.."
)

// WhiteoutPath returns the tar entry name for a whiteout that hides targetPath.
func WhiteoutPath(targetPath string) string {
	targetPath = strings.TrimPrefix(filepathToSlash(targetPath), "/")
	dir, base := splitDirBase(targetPath)
	if dir == "" {
		return WhiteoutPrefix + base
	}
	return dir + "/" + WhiteoutPrefix + base
}

// OpaqueDirWhiteoutPath returns the opaque-directory whiteout for dirPath.
func OpaqueDirWhiteoutPath(dirPath string) string {
	dirPath = strings.TrimPrefix(filepathToSlash(dirPath), "/")
	dirPath = strings.TrimSuffix(dirPath, "/")
	if dirPath == "" {
		return OpaqueWhiteout
	}
	return dirPath + "/" + OpaqueWhiteout
}

// IsWhiteout reports whether a tar entry name is a whiteout file.
func IsWhiteout(name string) bool {
	return strings.Contains(name, "/"+WhiteoutPrefix) || strings.HasPrefix(name, WhiteoutPrefix)
}

// IsOpaqueWhiteout reports whether name is an opaque-directory whiteout entry.
func IsOpaqueWhiteout(name string) bool {
	return strings.HasSuffix(name, "/"+OpaqueWhiteout) || name == OpaqueWhiteout
}

// HiddenPathFromWhiteout converts a whiteout entry name to the hidden path.
func HiddenPathFromWhiteout(whiteoutName string) string {
	idx := strings.LastIndex(whiteoutName, "/"+WhiteoutPrefix)
	if idx >= 0 {
		dir := whiteoutName[:idx]
		base := whiteoutName[idx+len("/"+WhiteoutPrefix):]
		if base == "" {
			return ""
		}
		if dir == "" {
			return base
		}
		return dir + "/" + base
	}
	if strings.HasPrefix(whiteoutName, WhiteoutPrefix) {
		return strings.TrimPrefix(whiteoutName, WhiteoutPrefix)
	}
	return ""
}

// OpaqueDirFromWhiteout returns the directory path made opaque by a whiteout entry.
func OpaqueDirFromWhiteout(whiteoutName string) string {
	if !IsOpaqueWhiteout(whiteoutName) {
		return ""
	}
	if whiteoutName == OpaqueWhiteout {
		return ""
	}
	return strings.TrimSuffix(whiteoutName, "/"+OpaqueWhiteout)
}

func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

func splitDirBase(path string) (string, string) {
	path = strings.Trim(path, "/")
	if path == "" {
		return "", ""
	}
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return "", path
	}
	return path[:idx], path[idx+1:]
}
