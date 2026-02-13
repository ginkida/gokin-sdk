package sdk

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PathValidator validates file paths to prevent directory traversal attacks.
type PathValidator struct {
	allowedDirs   []string
	allowSymlinks bool
}

// NewPathValidator creates a new path validator with the given allowed directories.
func NewPathValidator(allowedDirs []string) *PathValidator {
	normalized := make([]string, len(allowedDirs))
	for i, dir := range allowedDirs {
		normalized[i] = filepath.Clean(dir)
	}
	return &PathValidator{
		allowedDirs:   normalized,
		allowSymlinks: false,
	}
}

// NewPathValidatorWithSymlinks creates a path validator that allows symlinks.
func NewPathValidatorWithSymlinks(allowedDirs []string) *PathValidator {
	v := NewPathValidator(allowedDirs)
	v.allowSymlinks = true
	return v
}

// Validate validates that a path is safe and within allowed directories.
// Returns the resolved absolute path.
func (v *PathValidator) Validate(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	if strings.Contains(path, "\x00") {
		return "", fmt.Errorf("null byte in path")
	}

	cleanPath := filepath.Clean(path)

	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(absPath)
			resolvedParent, parentErr := filepath.EvalSymlinks(parentDir)
			if parentErr != nil && !os.IsNotExist(parentErr) {
				return "", fmt.Errorf("failed to resolve parent path: %w", parentErr)
			}
			if resolvedParent != "" {
				resolvedPath = filepath.Join(resolvedParent, filepath.Base(absPath))
			} else {
				resolvedPath = absPath
			}
		} else {
			return "", fmt.Errorf("failed to resolve symlinks: %w", err)
		}
	}

	if !v.allowSymlinks {
		if err := v.checkSymlink(resolvedPath); err != nil {
			return "", err
		}
	}

	if !v.IsWithinAllowed(resolvedPath) {
		return "", fmt.Errorf("path '%s' is outside allowed directories", filepath.Base(path))
	}

	return resolvedPath, nil
}

// IsWithinAllowed checks if the path is within any of the allowed directories.
func (v *PathValidator) IsWithinAllowed(absPath string) bool {
	if len(v.allowedDirs) == 0 {
		return true
	}

	for _, allowedDir := range v.allowedDirs {
		if v.isPathWithin(absPath, allowedDir) {
			return true
		}
	}
	return false
}

func (v *PathValidator) isPathWithin(target, base string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return filepath.VolumeName(target) == filepath.VolumeName(base)
	}

	if strings.HasPrefix(rel, "..") {
		return false
	}

	joined := filepath.Join(base, rel)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(joined, base) || strings.HasPrefix(strings.ToLower(joined), strings.ToLower(base)+string(filepath.Separator))
	}
	return joined == base || strings.HasPrefix(joined, base+string(filepath.Separator))
}

func (v *PathValidator) checkSymlink(path string) error {
	sep := string(filepath.Separator)
	components := strings.Split(filepath.Clean(path), sep)

	current := ""
	if filepath.IsAbs(path) {
		if runtime.GOOS == "windows" {
			current = filepath.VolumeName(path) + sep
		} else {
			current = sep
		}
	}

	for _, comp := range components {
		if comp == "" {
			continue
		}
		current = filepath.Join(current, comp)

		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not allowed: %s", current)
		}
	}

	return nil
}

// SanitizeFilename removes dangerous characters from a filename.
func SanitizeFilename(name string) string {
	dangerous := []string{"\x00", "..", "/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	sanitized := name
	for _, c := range dangerous {
		sanitized = strings.ReplaceAll(sanitized, c, "_")
	}
	return sanitized
}
