# mc-cli

> The official command-line interface for the Monte Carlo REST API. The binary is `montecarlo`. It is **not released yet**: there is no tag, no release pipeline and no package; see [Releasing](#releasing). Until then, build it locally.

## This repository will be public

Treat everything here as customer-facing, including things that are easy to forget:

- **Commit messages and pull request descriptions.** A closed pull request stays visible, a force-pushed commit stays reachable by its sha, and edited descriptions keep an edit history.
- **Help text**, which is what every flag and command description becomes.
- **Generated code**, which ships as-is.

So, **in the contents of any file committed here**: no internal repository names, no internal file paths, no ticket identifiers, and no design rationale that only makes sense from the inside. That reasoning belongs in the ticket or in the internal repository that owns generation. Branch names and pull request metadata are the exception: `<person>/<ticket-id>-<slug>` is the convention, and a merged pull request displays its head branch permanently.

The generated-file header naming the generator is deliberate and stays.

## Stack

- **Language:** Go, at the version `go.mod`'s `go` directive requires. That directive is the authoritative floor.
- **Framework:** cobra, with pflag for flags
- **Client:** the Monte Carlo Go SDK, which supplies the API client, its authentication, and the profile file reading

## Common Commands

```bash
go build ./...                  # compile check only, produces no binary
go test -race ./...
go vet ./...
gofmt -l .                      # must be empty

go build -o . ./cmd/montecarlo  # writes ./montecarlo
./montecarlo --help
```

## Key Directories

| Path | Purpose |
|------|---------|
| `cmd/montecarlo/` | The main package. Its directory name is the binary's name under `go install`. |
| `internal/cmd/` | The command tree: hand-written base files and the generated `*_cmd.gen.go`. |

## What is generated

Every `internal/cmd/*_cmd.gen.go`: one file per API tag, holding that tag's group command, its verb containers and one cobra command per exposed operation. A fix to one of those files does not survive regeneration; it belongs in the API or in the generator that reads its spec.

Regeneration replaces files **by name**: exactly `internal/cmd/*_cmd.gen.go` are deleted and rewritten, and every other file in the package is left alone. A hand-written file whose name ends in `_cmd.gen.go` is therefore lost on the next run. CI checks that every file with the suffix carries the generated header and no file without it does.

The generated files call these by bare name, all defined in the hand-written files of `internal/cmd/`:

| Helper | Does |
|--------|------|
| `rootCmd` | The root command each group registers itself on in `init()`. |
| `apiClient(cmd)` | The SDK client built from the persistent flags, and the context to call it with. |
| `apiErr(err)` | Renders an API failure from the problem the SDK decoded. |
| `changed(cmd, names...)` | Whether any of the flags was passed. |
| `requireAny(cmd, names...)` | An error naming the flags unless one was passed. |
| `flagString`, `flagInt`, `flagBool`, `flagStringSlice`, `flagStringMap`, `flagSecret` | Typed flag readers. Strings accept `@<path>`; maps take `key=value` pairs or `@<path>` holding JSON; a secret honours its `--<name>-prompt` companion. |
| `render(cmd, v)`, `renderList(cmd, v, columns)` | One object, or a list with the named columns, as a table or JSON. |

Renaming or removing one is a change to the generator's template as well, landed as a pair.

## Configuration

Credentials resolve in this order: flags, then the `MCD_DEFAULT_*` environment variables, then a profile in `~/.mcd/profiles.ini`. The SDK owns that precedence; the CLI adds only the profile chosen with `profile use`, stored in `~/.mcd/cli.ini`, consulted when neither `--profile` nor `MCD_DEFAULT_PROFILE` names one, and the default endpoint, `https://api.getmontecarlo.com`, used when neither a flag nor the profile sets one.

`profiles.ini` is shared with the other Monte Carlo tools. `profile set` edits it line by line: it writes `mcd_oauth_client_id`, `mcd_oauth_client_secret` and `mcd_instance_id`, or `mcd_id` and `mcd_token`, removes the pair it did not write, and leaves every other line, comment and section as it found it. It never writes `mcd_api_endpoint`; the other tools read that key as a GraphQL URL. `--config-dir` moves both files, for tests and for isolated setups.

The binary name lives in one constant, `binaryName` in `internal/cmd/root.go`, and in the `cmd/montecarlo/` directory name.

## Branching

Branch from `main` as `<person>/<ticket-id>-<slug>`. Never commit directly to `main`.

## Releasing

Not yet. Before the first release: the Go SDK this CLI depends on must be public and tagged, so `go.mod` can pin a tag instead of a pseudo-version; a tag-triggered release pipeline must build the per-platform archives and their checksums; and the binary name must be final, since the name reaches every install path.
