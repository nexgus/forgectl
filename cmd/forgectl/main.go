// Command forgectl queries and operates releases and assets across GitHub and
// GitLab through their REST APIs.
//
// This file defines the command-line surface (the noun-verb grammar described
// in docs/cli.md) and wires each command to a handler. The handlers are not
// implemented yet; this is the CLI skeleton.
package main

import (
	"fmt"

	"github.com/alecthomas/kong"

	"forgectl/pkg/version"
)

// Globals are the flags shared by every command. They mirror the "global
// flags" and "authentication" sections of docs/cli.md.
type Globals struct {
	Source    string `enum:"github,gitlab" required:"" help:"Hosting platform: github or gitlab."`
	Host      string `placeholder:"URL" help:"Base URL of a self-hosted instance; omit for the public site."`
	Insecure  bool   `help:"Skip TLS certificate verification; use only for trusted self-hosted hosts with self-signed certificates."`
	Token     string `help:"Override the token."`
	TokenFile string `type:"path" placeholder:"PATH" help:"Override the token, read from a file."`
	User      string `help:"Override the user."`

	Version kong.VersionFlag `short:"V" help:"Print version information and exit."`
}

// CLI is the root of the command tree.
type CLI struct {
	Globals

	Release ReleaseCmd `cmd:"" help:"Query and manage releases."`
	Asset   AssetCmd   `cmd:"" help:"Upload and download assets."`
}

// ReleaseCmd groups the "release" subcommands.
type ReleaseCmd struct {
	List   ReleaseListCmd   `cmd:"" help:"List all releases of a repository."`
	Create ReleaseCreateCmd `cmd:"" help:"Publish a release for a version and attach its uploaded assets."`
}

// AssetCmd groups the "asset" subcommands.
type AssetCmd struct {
	Upload   AssetUploadCmd   `cmd:"" help:"Upload one or more local files as assets of a version."`
	Download AssetDownloadCmd `cmd:"" help:"Download the assets of a release, optionally selected by glob."`
}

// ReleaseListCmd implements: forgectl release list <repo> [--json]
type ReleaseListCmd struct {
	Repo string `arg:"" name:"repo" help:"Target repository as an owner/repo path."`
	JSON bool   `help:"Emit JSON for scripting instead of human-readable text."`
}

// ReleaseCreateCmd implements:
// forgectl release create <repo> <version> (--note STR | --note-file PATH) [--commit COMMIT]
type ReleaseCreateCmd struct {
	Repo     string `arg:"" name:"repo" help:"Target repository as an owner/repo path."`
	Version  string `arg:"" name:"version" help:"Release tag (for example v1.2.3); created from --commit when it does not exist."`
	Note     string `xor:"note" required:"" help:"Release note text."`
	NoteFile string `xor:"note" required:"" type:"path" placeholder:"PATH" help:"Read the release note from a file (the whole file is the note)."`
	Commit   string `placeholder:"COMMIT" help:"Commit the new tag points to: a commit SHA, or 'latest' for the default branch HEAD. Required only when the tag does not yet exist."`
}

// AssetUploadCmd implements: forgectl asset upload <repo> <version> <path>[=NAME]...
type AssetUploadCmd struct {
	Repo    string   `arg:"" name:"repo" help:"Target repository as an owner/repo path."`
	Version string   `arg:"" name:"version" help:"Version string; the release need not exist yet."`
	Paths   []string `arg:"" name:"path" help:"One or more local files, each optionally suffixed with =NAME to rename the uploaded asset."`
}

// AssetDownloadCmd implements:
// forgectl asset download <repo> <version> [pattern]... [-d DIR] [-o NAME] [--overwrite]
type AssetDownloadCmd struct {
	Repo      string   `arg:"" name:"repo" help:"Target repository as an owner/repo path."`
	Version   string   `arg:"" name:"version" help:"Release tag, or 'latest' for the newest published release."`
	Patterns  []string `arg:"" name:"pattern" optional:"" help:"Glob patterns matched against asset names; omit to download every asset."`
	Dir       string   `short:"d" type:"path" placeholder:"DIR" help:"Directory to download into; created if missing. Defaults to the current directory."`
	Output    string   `short:"o" placeholder:"NAME" help:"Output filename; valid only when a single asset is downloaded."`
	Overwrite bool     `help:"Overwrite the target file if it already exists."`
}

// errNotImplemented is returned by every command handler until the
// corresponding behavior is implemented.
func errNotImplemented(cmd string) error {
	return fmt.Errorf("%s: not implemented yet", cmd)
}

func (c *ReleaseListCmd) Run(g *Globals) error   { return errNotImplemented("release list") }
func (c *ReleaseCreateCmd) Run(g *Globals) error { return errNotImplemented("release create") }
func (c *AssetUploadCmd) Run(g *Globals) error   { return errNotImplemented("asset upload") }
func (c *AssetDownloadCmd) Run(g *Globals) error { return errNotImplemented("asset download") }

func main() {
	var cli CLI
	ctx := kong.Parse(
		&cli,
		kong.Name("forgectl"),
		kong.Description("Query and operate releases and assets across GitHub and GitLab."),
		kong.UsageOnError(),
		kong.Vars{"version": version.Full()},
	)
	ctx.FatalIfErrorf(ctx.Run(&cli.Globals))
}
