// Command forgectl queries and operates releases and assets across GitHub and
// GitLab through their REST APIs.
//
// This file defines the command-line surface (the noun-verb grammar described
// in docs/cli.md) and dispatches via kong's Run methods. Each command's Run
// builds a pkg/forge client from the globals and hands the command's own fields
// to it; the work lives in pkg/forge, which receives what it needs as
// parameters and reads no global state.
package main

import (
	"os"

	"github.com/alecthomas/kong"

	"forgectl/pkg/forge"
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

// client builds a forge client from the globals. Each Run method calls it so
// forge depends on no CLI type and reads no shared state.
func (g *Globals) client() *forge.Client {
	return forge.New(forge.Config{
		Source:    g.Source,
		Host:      g.Host,
		Insecure:  g.Insecure,
		Token:     g.Token,
		TokenFile: g.TokenFile,
		User:      g.User,
	})
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

func (c *ReleaseListCmd) Run(g *Globals) error {
	return g.client().ReleaseList(c.Repo, c.JSON)
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

func (c *ReleaseCreateCmd) Run(g *Globals) error {
	return g.client().ReleaseCreate(c.Repo, c.Version, c.Note, c.NoteFile, c.Commit)
}

// AssetUploadCmd implements: forgectl asset upload <repo> <version> <path>[=NAME]...
type AssetUploadCmd struct {
	Repo    string   `arg:"" name:"repo" help:"Target repository as an owner/repo path."`
	Version string   `arg:"" name:"version" help:"Version string; the release need not exist yet."`
	Paths   []string `arg:"" name:"path" help:"One or more local files, each optionally suffixed with =NAME to rename the uploaded asset."`
}

func (c *AssetUploadCmd) Run(g *Globals) error {
	return g.client().AssetUpload(c.Repo, c.Version, c.Paths)
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

func (c *AssetDownloadCmd) Run(g *Globals) error {
	return g.client().AssetDownload(c.Repo, c.Version, c.Patterns, c.Dir, c.Output, c.Overwrite)
}

// newParser builds the kong parser bound to target. main binds it to the parsed
// CLI; tests bind it to a fresh CLI to exercise the grammar in isolation.
func newParser(target *CLI) (*kong.Kong, error) {
	return kong.New(
		target,
		kong.Name("forgectl"),
		kong.Description("Query and operate releases and assets across GitHub and GitLab."),
		kong.UsageOnError(),
		kong.Vars{"version": version.Full()},
	)
}

func main() {
	var cli CLI
	parser, err := newParser(&cli)
	if err != nil {
		panic(err)
	}
	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)

	// kong dispatches to the selected command's Run method, injecting the
	// globals it asks for.
	ctx.FatalIfErrorf(ctx.Run(&cli.Globals))
}
