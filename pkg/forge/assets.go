package forge

import (
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// localAsset is one file to upload: its local path and the asset name to
// publish it under.
type localAsset struct {
	path string
	name string
}

// AssetUpload implements: forgectl asset upload <repo> <version> <path>[=NAME]...
func (c *Client) AssetUpload(repo, version string, paths []string) error {
	files := make([]localAsset, len(paths))
	for i, spec := range paths {
		files[i] = parsePathSpec(spec)
	}

	p, err := c.platform(repo)
	if err != nil {
		return err
	}
	up, err := p.newUploader(version)
	if err != nil {
		return err
	}

	// Try every file, never stopping early; tally the outcome and fail at the
	// end if any file failed (docs/cli.md).
	var failed int
	for _, f := range files {
		if err := up.upload(f); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "failed %s: %v\n", f.name, err)
			continue
		}
		fmt.Printf("uploaded %s\n", f.name)
	}
	n := len(files)
	if failed > 0 {
		return fmt.Errorf("uploaded %d/%d asset(s); %d failed", n-failed, n, failed)
	}
	fmt.Printf("uploaded %d/%d asset(s)\n", n, n)
	return nil
}

// AssetDownload implements:
// forgectl asset download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
func (c *Client) AssetDownload(repo, version string, patterns []string, dir, output string, overwrite bool) error {
	p, err := c.platform(repo)
	if err != nil {
		return err
	}
	assets, err := p.findReleaseAssets(version)
	if err != nil {
		return err
	}

	matched := matchAssets(assets, patterns)
	if len(matched) == 0 {
		// "Match nothing" is success: nothing to download, exit 0 (docs/cli.md).
		fmt.Println("no assets matched; nothing to download")
		return nil
	}
	if output != "" && len(matched) > 1 {
		return fmt.Errorf("-o/--output names a single file but %d assets matched; drop -o or narrow the pattern", len(matched))
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	// Resolve every destination and reject existing targets up front, so a
	// clash fails before any file is written.
	type job struct {
		a    asset
		dest string
	}
	jobs := make([]job, 0, len(matched))
	for _, a := range matched {
		name := a.Name
		if output != "" {
			name = output
		}
		dest := name
		if dir != "" {
			dest = filepath.Join(dir, name)
		}
		if !overwrite {
			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("%s already exists; pass --overwrite to replace it", dest)
			}
		}
		jobs = append(jobs, job{a: a, dest: dest})
	}

	for _, j := range jobs {
		if err := downloadToFile(p, j.a, j.dest); err != nil {
			return err
		}
		fmt.Printf("downloaded %s -> %s\n", j.a.Name, j.dest)
	}
	return nil
}

// downloadToFile streams one asset to dest through a temporary file in the same
// directory, renaming into place only on success so a failed transfer never
// leaves a partial file at dest.
func downloadToFile(p platform, a asset, dest string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".forgectl-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // a no-op once the rename below succeeds

	// os.CreateTemp makes a 0600 file; downloaded assets should use the usual
	// 0644 so they are readable like any other downloaded file.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := p.download(a, tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("saving %s: %w", dest, err)
	}
	return nil
}

// parsePathSpec splits a "<path>[=NAME]" upload argument. It splits on the last
// '=', but if the text after it looks like a path (contains a separator) the
// whole argument is treated as a plain path, so a path that itself contains '='
// still works in the common case (docs/cli.md). NAME must be a flat filename.
// With no usable "=NAME", the asset name is the path's basename.
func parsePathSpec(spec string) localAsset {
	if i := strings.LastIndex(spec, "="); i >= 0 {
		name := spec[i+1:]
		if name != "" && !strings.ContainsAny(name, `/\`) {
			return localAsset{path: spec[:i], name: name}
		}
	}
	return localAsset{path: spec, name: filepath.Base(spec)}
}

// matchAssets returns the assets whose names match any of the glob patterns.
// With no patterns every asset matches; multiple patterns are a union; a
// pattern with no wildcards matches exactly (docs/cli.md).
func matchAssets(assets []asset, patterns []string) []asset {
	if len(patterns) == 0 {
		return assets
	}
	var out []asset
	for _, a := range assets {
		if matchAny(a.Name, patterns) {
			out = append(out, a)
		}
	}
	return out
}

// matchAny reports whether name matches any of the glob patterns.
func matchAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// readLocalFile reads an upload source, rejecting a directory with a clear
// message (only files are accepted; docs/cli.md). Upload reads local files and
// never modifies them.
func readLocalFile(p string) ([]byte, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", p)
	}
	return os.ReadFile(p)
}

// contentType guesses an asset's MIME type from its name, defaulting to
// application/octet-stream when the extension is unknown (docs/cli.md).
func contentType(name string) string {
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
