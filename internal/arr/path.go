package arr

import (
	"fmt"
	"path"
	"strings"
)

// isBareFilename reports whether value contains a filename but no directory
// information. VLC's HTTP interface occasionally exposes only meta.filename;
// in that case a manager may use a filename only when it is unique in the
// complete manager library. A directory-qualified path must never fall back
// to basename matching because that would weaken an exact-path lookup.
func isBareFilename(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.ContainsAny(value, "/\\")
}

func sameBasename(left, right string, windows bool) bool {
	left = path.Base(cleanPortablePath(left, windows))
	right = path.Base(cleanPortablePath(right, windows))
	if windows {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func remoteMediaPath(localMediaPath string, mapping *PathMapping, localWindows, remoteWindows bool) (string, error) {
	if mapping == nil {
		// Defer interpretation of separators and /C:/ URI paths until the
		// manager OS is known.
		return localMediaPath, nil
	}
	if strings.TrimSpace(mapping.LocalPrefix) == "" || strings.TrimSpace(mapping.RemotePrefix) == "" {
		return "", fmt.Errorf("path mapping requires both local and remote prefixes")
	}

	mediaPath := cleanPortablePath(localMediaPath, localWindows)
	localPrefix := cleanPortablePath(mapping.LocalPrefix, localWindows)
	suffix, mapped := pathSuffix(mediaPath, localPrefix, localWindows)
	if !mapped {
		return mediaPath, nil
	}

	remotePrefix := cleanPortablePath(mapping.RemotePrefix, remoteWindows)
	if suffix == "" {
		return remotePrefix, nil
	}
	return strings.TrimRight(remotePrefix, "/") + "/" + suffix, nil
}

func normalizeRemotePath(value string, windows bool) string {
	normalized := cleanPortablePath(value, windows)
	if windows {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

func joinPortablePath(prefix, suffix string, windows bool) string {
	return strings.TrimRight(cleanPortablePath(prefix, windows), "/") + "/" +
		strings.TrimLeft(cleanPortablePath(suffix, windows), "/")
}

func cleanPortablePath(value string, windows bool) string {
	if windows {
		value = strings.ReplaceAll(value, `\`, "/")
	}
	// net/url represents file:///C:/... as /C:/... on Windows. Treat that
	// standard URI form as the same drive path returned by the managers.
	if windows && hasURIWindowsDrivePrefix(value) {
		value = value[1:]
	}
	if value == "" {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return ""
	}
	if windows && len(cleaned) >= 2 && cleaned[1] == ':' {
		cleaned = strings.ToUpper(cleaned[:1]) + cleaned[1:]
	}
	return cleaned
}

func sameOrDescendant(candidate, prefix string) bool {
	if candidate == "" || prefix == "" {
		return false
	}
	prefix = strings.TrimRight(prefix, "/")
	return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
}

func pathSuffix(candidate, prefix string, caseInsensitive bool) (string, bool) {
	if candidate == "" || prefix == "" {
		return "", false
	}
	if prefix == "/" {
		if !strings.HasPrefix(candidate, "/") {
			return "", false
		}
		return strings.TrimPrefix(candidate, "/"), true
	}
	if strings.HasPrefix(candidate, "/") != strings.HasPrefix(prefix, "/") {
		return "", false
	}
	candidateParts := strings.Split(strings.Trim(candidate, "/"), "/")
	prefixParts := strings.Split(strings.Trim(prefix, "/"), "/")
	if len(candidateParts) < len(prefixParts) {
		return "", false
	}
	for index, part := range prefixParts {
		matches := candidateParts[index] == part
		if caseInsensitive {
			matches = strings.EqualFold(candidateParts[index], part)
		}
		if !matches {
			return "", false
		}
	}
	return strings.Join(candidateParts[len(prefixParts):], "/"), true
}

func hasURIWindowsDrivePrefix(value string) bool {
	return len(value) >= 4 && value[0] == '/' && isASCIILetter(value[1]) && value[2] == ':' && value[3] == '/'
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
