package forge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// auth holds the credentials resolved for one connection together with
// human-readable descriptions of where each value came from. ping prints the
// sources (never the token value itself) so the user can see exactly which
// setting took effect.
type auth struct {
	Token       string
	User        string
	TokenSource string // where Token came from, e.g. "--token flag"
	UserSource  string // where User came from
	Warnings    []string
}

// resolveAuth resolves the token and user for the given host, following the
// precedence in docs/credential.md: flags override the credential file, which
// is read from a fixed location (no flag names it). It returns an error only
// for a malformed setup (ambiguous credential files, unreadable token file);
// resolving to no credentials at all is valid (anonymous use).
func resolveAuth(cfg Config, host string) (auth, error) {
	var a auth

	file, fileToken, fileUser, err := readCredential(host)
	if err != nil {
		return auth{}, err
	}
	if file != "" {
		if w := worldReadableWarning(file); w != "" {
			a.Warnings = append(a.Warnings, w)
		}
	}

	// token: --token > --token-file > credential file > none.
	switch {
	case cfg.Token != "":
		a.Token = cfg.Token
		a.TokenSource = "--token flag"
	case cfg.TokenFile != "":
		tok, err := readTokenFile(cfg.TokenFile)
		if err != nil {
			return auth{}, err
		}
		a.Token = tok
		a.TokenSource = fmt.Sprintf("--token-file (%s)", cfg.TokenFile)
	case fileToken != "":
		a.Token = fileToken
		a.TokenSource = fmt.Sprintf("credential file (%s)", file)
	default:
		a.TokenSource = "none"
	}

	// user: --user > credential file > none.
	switch {
	case cfg.User != "":
		a.User = cfg.User
		a.UserSource = "--user flag"
	case fileUser != "":
		a.User = fileUser
		a.UserSource = fmt.Sprintf("credential file (%s)", file)
	default:
		a.UserSource = "none"
	}

	return a, nil
}

// credentialDirs returns the directories to search for the credential file, in
// order, per docs/credential.md: $XDG_CONFIG_HOME wins over ~/.config on
// non-Windows hosts; Windows uses %AppData%.
func credentialDirs() []string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("AppData"); appData != "" {
			return []string{filepath.Join(appData, "forgectl")}
		}
		if dir, err := os.UserConfigDir(); err == nil {
			return []string{filepath.Join(dir, "forgectl")}
		}
		return nil
	}
	var dirs []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dirs = append(dirs, filepath.Join(xdg, "forgectl"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "forgectl"))
	}
	return dirs
}

// credentialExts are the recognized credential-file extensions; the parser is
// chosen by extension.
var credentialExts = []string{"toml", "yaml", "yml", "json"}

// findCredentialFile returns the path of the single credential.* file in the
// search directories, or "" if none exists. It errors when one directory holds
// more than one credential.* file, since docs/credential.md forbids that
// ambiguity rather than guess a precedence.
func findCredentialFile() (string, error) {
	for _, dir := range credentialDirs() {
		var found []string
		for _, ext := range credentialExts {
			p := filepath.Join(dir, "credential."+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				found = append(found, p)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return "", fmt.Errorf("multiple credential files in %s (%s); keep only one", dir, strings.Join(baseNames(found), ", "))
		}
	}
	return "", nil
}

// readCredential locates and parses the credential file, returning its path and
// the token/user that apply to the given host. A missing file is not an error
// (it yields empty values); a malformed file is.
func readCredential(host string) (path, token, user string, err error) {
	path, err = findCredentialFile()
	if err != nil {
		return "", "", "", err
	}
	if path == "" {
		return "", "", "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", fmt.Errorf("reading credential file %s: %w", path, err)
	}
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	token, user, err = parseCredential(data, ext, host)
	if err != nil {
		return "", "", "", fmt.Errorf("credential file %s: %w", path, err)
	}
	return path, token, user, nil
}

// parseCredential parses the credential bytes for the given extension and
// returns the token/user for host. A file is either flat (top-level token/user
// shared by every host) or hierarchical (host name as the top-level key);
// mixing the two forms is an error, as is a host entry that is not a table.
func parseCredential(data []byte, ext, host string) (token, user string, err error) {
	var raw map[string]any
	switch ext {
	case "toml":
		if err := toml.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
	case "json":
		if err := json.Unmarshal(data, &raw); err != nil {
			return "", "", err
		}
	default:
		return "", "", fmt.Errorf("unsupported credential format %q", ext)
	}

	flat, hier := classify(raw)
	switch {
	case flat && hier:
		return "", "", fmt.Errorf("mixes the flat form (top-level token/user) with the hierarchical form (per-host); use one")
	case flat:
		t, _ := stringField(raw, "token")
		u, _ := stringField(raw, "user")
		return t, u, nil
	case hier:
		entry, ok := raw[host]
		if !ok {
			return "", "", nil // host not listed; no credentials for it
		}
		m, ok := entry.(map[string]any)
		if !ok {
			return "", "", fmt.Errorf("host %q entry is not a table", host)
		}
		t, _ := stringField(m, "token")
		u, _ := stringField(m, "user")
		return t, u, nil
	default:
		return "", "", nil // empty file
	}
}

// classify reports whether a parsed credential map looks flat (carries a
// top-level token/user) and whether it looks hierarchical (carries any
// host-table value). A well-formed file is exactly one of the two.
func classify(raw map[string]any) (flat, hier bool) {
	for k, v := range raw {
		if _, isMap := v.(map[string]any); isMap {
			hier = true
			continue
		}
		if k == "token" || k == "user" {
			flat = true
		}
	}
	return flat, hier
}

// stringField returns the string value of key in m, if present and a string.
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// baseNames returns the base name of each path.
func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// readTokenFile reads a token from a file, trimming surrounding whitespace,
// control characters, and invisible characters (newline, tab, BOM, zero-width
// space) so only the token body remains, per docs/cli.md.
func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading --token-file %s: %w", path, err)
	}
	tok := strings.TrimFunc(string(data), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || isInvisible(r)
	})
	if tok == "" {
		return "", fmt.Errorf("--token-file %s contains no token", path)
	}
	return tok, nil
}

// isInvisible reports whether r is one of the zero-width / byte-order-mark code
// points that should be trimmed from a token but are not caught by
// unicode.IsSpace or unicode.IsControl. They are matched by code point so the
// source stays free of literal invisible characters.
func isInvisible(r rune) bool {
	switch r {
	case 0xFEFF, // BOM / zero-width no-break space
		0x200B, // zero-width space
		0x200C, // zero-width non-joiner
		0x200D, // zero-width joiner
		0x2060: // word joiner
		return true
	}
	return false
}

// worldReadableWarning returns a warning when the credential file is readable
// by group or others; credentials should be private (mode 0600). It is a no-op
// on Windows, whose permission model differs.
func worldReadableWarning(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Sprintf("credential file %s is accessible by other users (mode %#o); consider chmod 600", path, perm)
	}
	return ""
}
