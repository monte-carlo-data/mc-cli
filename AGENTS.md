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
| `cmd/montecarlo/` | The main package. |
| `internal/cmd/` | The command tree: hand-written base files and the generated `*_cmd.gen.go`. |

## What is generated

Every `internal/cmd/*_cmd.gen.go`: one file per API tag, holding that tag's group command, its verb containers and one cobra command per exposed operation. A fix to one of those files does not survive regeneration; it belongs in the API or in the generator that reads its spec.

Regeneration replaces files **by name**: exactly `internal/cmd/*_cmd.gen.go` are deleted and rewritten, and every other file in the package is left alone. A hand-written file whose name ends in `_cmd.gen.go` is therefore lost on the next run. CI checks that every file with the suffix carries the generated header and no file without it does.

## Regeneration is automatic

api-codegen regenerates this repository and opens a pull request whenever it is behind the API spec or behind `mc-sdk-go`. Nobody runs the generator from outside any more.

That pull request moves the SDK pin in the same commit as the generated commands that need it — they call SDK symbols that do not exist at the older pin — and it has already been built and vetted against that pin before being pushed. The generated files carry no code owner, so it asks nobody for review: a person still approves and merges it, and `go.mod` and `go.sum` are checked to make sure the bot changed nothing there but the SDK's own lines.

`.api-codegen-source.json` at the root records what produced the tree: the api-codegen commit and run, the `mc-sdk-go` commit pinned, and the monolith export the spec came from. It names no generator version, because the commands come from api-codegen's own templates rather than an external tool, and `api_codegen_commit` already says which.

The generated files call these by bare name, all defined in the hand-written files of `internal/cmd/`:

| Helper | Does |
|--------|------|
| `rootCmd` | The root command each group registers itself on in `init()`. |
| `apiClient(cmd)` | The SDK client built from the persistent flags, and the context to call it with. |
| `apiErr(resp, err)` | Renders an API failure from the problem the SDK decoded, else from the response's request line, status and body message. |
| `changed(cmd, names...)` | Whether any of the flags was passed. |
| `requireAny(cmd, names...)` | An error naming the flags unless one was passed. |
| `requireOneGroup(cmd, groups...)`, `flagGroup` | The name of the one `flagGroup` any of whose flags was passed; none, or flags from several, is an error naming the groups. A prompt flag counts as a member of its group. |
| `newUnwind(cmd)`, `record(kind, id, deleteCmd, del)`, `fail(resp, err, uncertain)` | For a command that creates several resources in turn. `record` notes each one after it is created; `fail` deletes them newest first and returns the failed step's error followed by one line per resource, with the command that deletes by hand anything it could not. The deletes run after Ctrl-C too, within 30 seconds in all, retried like `retryOnTransient`, and a resource already gone counts as deleted. `uncertain` is added when the failed step's outcome is unknown: no response, or a server error other than 503. |
| `waitForValidations(cmd, first, fetch, runCmd)` | Follows a validation run to its end through `fetch`, starting from `first` (the run as the validate call returned it), retried like `retryOnTransient`, waiting as `Retry-After` asks, and says whether every validation passed; warnings do not fail it. If it stops waiting after its time budget, it names `runCmd`, the bare command that reads a run by id, with the run id appended; an empty `runCmd` names no command. Progress goes to stderr, redrawn in place on a terminal, then the problems each validation reported, then a summary line with the run id. Ctrl-C stops the wait. A completed run listing fewer validations than its total, or none, is an error rather than a verdict. |
| `flagString`, `flagInt`, `flagBool`, `flagStringSlice`, `flagStringMap`, `flagSecret` | Typed flag readers. Strings take their value literally. A secret and a map accept `@<path>`; a secret also has a `--<name>-prompt` companion. The generator emits no call to `flagStringSlice` today. |
| `render(cmd, v, fields...)`, `renderList(cmd, v, columns)` | One object, or a list with the named columns, as a table or JSON. |
| `renderIfJSON(cmd, v)` | Prints v when the output is JSON, and nothing otherwise: for a result whose progress stderr has already shown. |
| `renderPage(cmd, v, columns)`, `renderPages(cmd, columns, fetch)` | `renderPage` prints one page: a table of its items, then `Total: <count>` when the page carries a count, then `Next page: --cursor "<value>"` when there is more. As JSON it prints the whole envelope. `renderPages` follows the cursor through `fetch` to the last page and prints every item as one list. A generated paged command calls `renderPage` when `--cursor`, `--limit` or `--with-count` is passed, and `renderPages` otherwise. |
| `enumCompletion(values)` | Shell completion for an enum flag, from the values the SDK exports. |
| `retryOnTransient(cmd, call)` | Repeats a call while the API answers 503 or 429, every 15s, or as `Retry-After` asks, for up to 5 minutes; a longer Retry-After ends the retries; also stops at the deadline of the context it runs under. The generator wraps writes in it; a generated read calls the API once, except the polls inside `waitForValidations`. |
| `confirm(cmd, question)` | Asks `question? [y/N]` on the terminal; `--yes` skips it and is required without a terminal. Every delete and every write the spec marks destructive use it (`deployments reprovision` today). |

Renaming or removing one is a change to the generator's template as well, landed as a pair. The root command's persistent flag names are part of the same contract: the generator refuses a body or query flag that collides with one.

## Configuration

Credentials resolve in this order: flags, then the `MCD_DEFAULT_*` environment variables, then a profile in `~/.mcd/profiles.ini`. The SDK owns that precedence; the CLI adds only the profile chosen with `profile use`, stored in `~/.mcd/cli.ini`, consulted when neither `--profile` nor `MCD_DEFAULT_PROFILE` names one, and the default endpoint, `https://api.getmontecarlo.com`, used when neither a flag nor the profile sets one.

`profiles.ini` is shared with the other Monte Carlo tools. `profile set` edits it line by line: it writes `mcd_oauth_client_id`, `mcd_oauth_client_secret` and `mcd_instance_id`, or `mcd_id` and `mcd_token`, removes the pair it did not write, and leaves every other line, comment and section as it found it. `--instance` is written in either mode, but only when it is given: switching a profile from OAuth to a token leaves an existing `mcd_instance_id` in place unless `--instance` is passed too. It never writes `mcd_api_endpoint`; the other tools read that key as a GraphQL URL. `--config-dir` moves both files, for tests and for isolated setups.

The binary name lives in one constant, `binaryName` in `internal/cmd/root.go`, and in the `cmd/montecarlo/` directory name.

## Branching

Branch from `main` as `<person>/<ticket-id>-<slug>`. Never commit directly to `main`.

## Releasing

Not yet. Before the first release: the Go SDK this CLI depends on must be public and tagged, so `go.mod` can pin a tag instead of a pseudo-version; a tag-triggered release pipeline must build the per-platform archives and their checksums; and the binary name must be final, since the name reaches every install path. On a pull request from a fork, CI's SDK token step fails until mc-sdk-go is public; that is accepted.
