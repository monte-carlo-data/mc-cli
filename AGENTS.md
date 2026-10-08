# mc-cli

> The official command-line interface for the Monte Carlo REST API. The binary is `montecarlo`. Every merge to `main` is a release; see [Releasing](#releasing).

## This repository is public

Treat everything here as customer-facing, including things that are easy to forget:

- **Commit messages and pull request descriptions.** A closed pull request stays visible, a force-pushed commit stays reachable by its sha, and edited descriptions keep an edit history.
- **Help text**, which is what every flag and command description becomes.
- **Generated code**, which ships as-is.

So, **in the contents of any file committed here**: no internal file paths, no ticket identifiers, and no design rationale that only makes sense from the inside. That reasoning belongs in the ticket or in the internal repository that owns generation.

Two internal names are the exception: api-codegen, the generator, and monolith, the service the API spec is exported from. Generated files and `.api-codegen-source.json` already name them. Name no other internal repository.

Branch names and pull request metadata may carry ticket identifiers: `<person>/<ticket-id>-<slug>` is the convention, and a merged pull request displays its head branch permanently.

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
go generate ./...               # rewrites THIRD_PARTY_NOTICES

go build -o . ./cmd/montecarlo  # writes ./montecarlo
./montecarlo --help

# The release build, without publishing, into dist/; then the installer tests against it
RELEASE_TAG=v0.1.0 go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean
.github/scripts/install_test.sh dist v0.1.0
pwsh .github/scripts/install_test.ps1 -Dist dist -Tag v0.1.0   # needs Windows for every case
```

## Key Directories

| Path | Purpose |
|------|---------|
| `cmd/montecarlo/` | The main package. |
| `internal/cmd/` | The command tree: hand-written base files and the generated `*_cmd.gen.go`. |
| `tools/notices/` | Writes `THIRD_PARTY_NOTICES`; `go generate ./...` runs it. |
| `install.sh`, `install.ps1` | The installers, attached to every release. |
| `.github/scripts/` | The release-tag script, the installer tests and the release mirror they run against. |
| `.github/actions/goreleaser/` | The pinned GoReleaser, shared by the snapshot and the release. |

## What is generated

Every `internal/cmd/*_cmd.gen.go`: one file per API tag, holding that tag's group command, its verb containers and one cobra command per exposed operation. A fix to one of those files does not survive regeneration; it belongs in the API or in the generator that reads its spec.

Regeneration replaces files **by name**: exactly `internal/cmd/*_cmd.gen.go` are deleted and rewritten, and every other file in the package is left alone. A hand-written file whose name ends in `_cmd.gen.go` is therefore lost on the next run. CI checks that every file with the suffix carries the generated header and no file without it does.

Every hand-written Go file starts with `// Copyright Monte Carlo AI, Inc.` and `// SPDX-License-Identifier: Apache-2.0`, followed by a blank line. Generated `*_cmd.gen.go` files carry no such header, since regeneration would drop it.

## Third-party notices

The release binaries compile in third-party modules, and their licenses require passing on their license and notice files. `THIRD_PARTY_NOTICES` holds them, and every release archive has to carry it next to `LICENSE` and `README.md`. `go generate ./...` rebuilds it with `tools/notices`, from the modules `go list -deps` reports for `cmd/montecarlo` on every release platform. Never edit it by hand. CI fails when it differs from what `go generate` produces, so a dependency change has to commit it too.

## Regeneration is automatic

api-codegen regenerates this repository and opens a pull request whenever it is behind the API spec or behind `mc-sdk-go`. Nobody runs the generator from outside any more.

That pull request moves the SDK pin in the same commit as the generated commands that need it — they call SDK symbols that do not exist at the older pin — and it has already been built and vetted against that pin before being pushed. It also runs `go generate ./...`, so `THIRD_PARTY_NOTICES` moves with the pin. The generated files and the notices carry no code owner, so it asks nobody for review: a person still approves and merges it, and `go.mod` and `go.sum` are checked to make sure the bot changed nothing there but the SDK's own lines.

`.api-codegen-source.json` at the root records what produced the tree: the api-codegen commit and run, the `mc-sdk-go` commit pinned, and the monolith export the spec came from. It names no generator version, because the commands come from api-codegen's own templates rather than an external tool, and `api_codegen_commit` already says which.

The generated files call these by bare name, all defined in the hand-written files of `internal/cmd/`:

| Helper | Does |
|--------|------|
| `rootCmd` | The root command each group registers itself on in `init()`. |
| `apiClient(cmd)` | The SDK client built from the persistent flags, and the context to call it with. |
| `apiErr(resp, err)` | Renders an API failure from the problem the SDK decoded, else from the response's request line, status and body message. The error carries the exit code for the status: 3 for 404, 4 for 401 and 403, 5 for 503 and 429, else 1. Any other error is returned as it is. |
| `changed(cmd, names...)` | Whether any of the flags was passed. |
| `requireAny(cmd, names...)` | A usage error naming the flags unless one was passed. |
| `requireOneGroup(cmd, groups...)`, `flagGroup` | The name of the one `flagGroup` any of whose flags was passed; none, or flags from several, is a usage error naming the groups. A prompt flag counts as a member of its group. |
| `newUnwind(cmd)`, `record(kind, id, deleteCmd, del)`, `fail(resp, err, uncertain)` | For a command that creates several resources in turn. `record` notes each one after it is created; `fail` deletes them newest first and returns the failed step's error followed by one line per resource, with the command that deletes by hand anything it could not. The deletes run after Ctrl-C too, within 30 seconds in all, retried like `retryOnTransient`, and a resource already gone counts as deleted. `uncertain` is added when the failed step's outcome is unknown: no response, or a server error other than 503. |
| `followValidationRun(cmd, first, fetch, runCmd)` | Follows a validation run to its end through `fetch`, starting from `first` (the run as the validate call returned it), retried like `retryOnTransient`, waiting as `Retry-After` asks, and says whether every validation passed; warnings do not fail it. `followValidationRun` passes `fetch` the run's `revision` as `since`, nil when the run carries none, and the last read's `ETag`. It merges the validations each read returns into the run by name, so a read may list only those that changed. A 304 leaves the run as it is. When the last read listed fewer validations than the run has, the run is read once more whole, with neither: `fetch`'s last successful result is the complete final run, so a caller may keep it as the run to print; a 304 is not a result. If it stops waiting after its time budget, it names `runCmd`, the bare command that reads a run by id, with the run id appended; an empty `runCmd` names no command. Progress goes to stderr, redrawn in place on a terminal, then the problems each validation reported, then a summary line with the run id. Ctrl-C stops the wait. A completed run listing fewer validations than its total, or none, is an error rather than a verdict. |
| `flagString`, `flagInt`, `flagBool`, `flagStringSlice`, `flagStringMap`, `flagSecret` | Typed flag readers. Strings take their value literally. A secret and a map accept `@<path>`; a secret also has a `--<name>-prompt` companion. Their errors are usage errors. The generator emits no call to `flagStringSlice` today. |
| `render(cmd, v, fields...)`, `renderList(cmd, v, columns)` | One object, or a list with the named columns, as a table or JSON. |
| `renderIfJSON(cmd, v)` | Prints v when the output is JSON, and nothing otherwise: for a result whose progress stderr has already shown. |
| `renderPage(cmd, v, columns)`, `renderPages(cmd, columns, fetch)` | `renderPage` prints one page: a table of its items, then `Total: <count>` when the page carries a count, then `Next page: --cursor "<value>"` when there is more. As JSON it prints the whole envelope. `renderPages` follows the cursor through `fetch` to the last page and prints every item as one list. A generated paged command calls `renderPage` when `--cursor`, `--limit` or `--with-count` is passed, and `renderPages` otherwise. |
| `enumCompletion(values)` | Shell completion for an enum flag, from the values the SDK exports. |
| `retryOnTransient(cmd, call)` | Repeats a call while the API answers 503 or 429, every 15s, or as `Retry-After` asks, for up to 5 minutes; a longer Retry-After ends the retries; also stops at the deadline of the context it runs under. The generator wraps writes in it; a generated read calls the API once, except the polls inside `followValidationRun`. |
| `confirm(cmd, question)` | Asks `question? [y/N]` on the terminal; `--yes` skips it and is required without a terminal. Every delete and every write the spec marks destructive use it (`deployments reprovision` today). A decline exits 130, a missing `--yes` 2, and Ctrl-C ends the prompt. |
| `usageError(format, args...)`, `validationsFailed(format, args...)` | Like `fmt.Errorf`, with exit code 2 for a mistake the command line alone shows, or 6 for validations that ran and did not all pass. |

Renaming or removing one is a change to the generator's template as well, landed as a pair. The root command's persistent flag names are part of the same contract: the generator refuses a body or query flag that collides with one.

## Exit codes

The codes are constants in `internal/cmd/exitcode.go`, listed for users in the README, and a change to one is a breaking change. An error carries its code as an `exitError`, which leaves the message alone; `exitCode` maps a command's error to the code, and an interrupted context is 130 whatever the error. The root's flag error function marks flag errors as usage errors. Cobra's other rejections (unknown command, wrong arguments, missing required flag, flag groups) are plain errors. When a command fails, `executeArgs` runs cobra's checks on it again to tell those apart from a failure inside the command. Cobra answers a group given an unknown subcommand with its help and no error; `executeArgs` reports that as a usage error, and the root's help function prints nothing for it (`<group> help` still shows help). The root's `PersistentPreRunE` checks `--output` before any command runs. The root turns on cobra's `EnableTraverseRunHooks`, so a command's own persistent hook runs after this one instead of replacing it.

A plain `fmt.Errorf` exits 1. The generated commands return `usageError` for a mistake the command line alone shows, an enum value the SDK refuses included, and `validationsFailed` when validations did not all pass.

## Configuration

Credentials resolve in this order: flags, then the `MCD_DEFAULT_*` environment variables, then a profile in `~/.mcd/profiles.ini`. The SDK owns that precedence; the CLI adds only the profile chosen with `profile use`, stored in `~/.mcd/cli.ini`, consulted when neither `--profile` nor `MCD_DEFAULT_PROFILE` names one, and the default endpoint, `https://api.getmontecarlo.com`, used when neither a flag nor the profile sets one.

`profiles.ini` is shared with the other Monte Carlo tools. `profile set` edits it line by line: it writes `mcd_oauth_client_id`, `mcd_oauth_client_secret` and `mcd_instance_id`, or `mcd_id` and `mcd_token`, removes the pair it did not write, and leaves every other line, comment and section as it found it. `--instance` is written in either mode, but only when it is given: switching a profile from OAuth to a token leaves an existing `mcd_instance_id` in place unless `--instance` is passed too. `mcd_api_endpoint` is written only when `--endpoint` is passed and is not the default, and always as a GraphQL URL (`<endpoint>/graphql`), the form the other tools read and the SDK strips back to the REST base; passing the default removes it, and omitting `--endpoint` leaves it as it is. `--config-dir` moves both files, for tests and for isolated setups.

`profile set` and `configure` share one path: they trim the values, check them locally (an instance id's form, an API token's 56 characters), then call the current-user endpoint with only those credentials and `--endpoint`, never the environment or the profile being replaced, and write nothing unless it succeeds. `--no-validate` skips the call. `profile set` declares the credential flags itself, shadowing the root's persistent ones, so its help lists them as the values it writes.

Root help lists commands in two groups. Hand-written commands set their `GroupID`; the generated ones register without one, so `groupResourceCommands` in `internal/cmd/root.go` files every ungrouped command except `help`, `completion` and `version` under Resources before the command line runs.

The binary name lives in one constant, `binaryName` in `internal/cmd/root.go`, and in the `cmd/montecarlo/` directory name.

## Branching

Branch from `main` as `<person>/<ticket-id>-<slug>`. Never commit directly to `main`.

## Releasing

Every merge to `main` is a release, and a published version can't be changed or withdrawn: releases are immutable, and the Go module proxy keeps every version it has served. `.github/scripts/next-tag.sh` works out the tag `v<base>.<n>`: `<base>` is the major.minor in `VERSION`, and `<n>` is one past the highest patch already tagged on that base. CI runs its test, and validates `VERSION`, on every pull request. The org App and the `apollo` team are the only actors allowed to create tags. The same script, its test and the `tag` job also live in mc-sdk-go and the Terraform provider; change them together.

Only the patch is bumped automatically. A merge that breaks scripts written against the CLI, by removing or renaming a command or flag, changing an exit code, or changing the JSON output's shape, must bump the minor: change `VERSION` (`0.1` to `0.2`) in the same pull request, and its merge is tagged `v0.2.0`. A regeneration from api-codegen is a release too, and the same rule applies to what it changes. The bot may only pin a tagged mc-sdk-go release, which CI checks.

### How a release is built

On a push to `main`, once the build, the checks, `release-snapshot` and every `install-test` leg pass:

1. `tag` tags the merge commit through the org App.
2. `release` runs GoReleaser (`.goreleaser.yml`) only in a push-to-main run, never on a tag push, which would run whichever commit was tagged; the tag is always that run's commit. Only the highest release is marked latest, since releases from back-to-back merges can publish out of order. It cross-compiles darwin, linux and windows on amd64 and arm64, packs each with `LICENSE`, `README.md` and `THIRD_PARTY_NOTICES`, writes the checksums file, and attaches it all, with both installers, to a GitHub release that is published once every asset is uploaded. The release targets and the `goos` list in `tools/notices` stay in step, so the notices cover every platform shipped.
3. `attest` attests the archives, the checksums and the installers that `release` built, after checking that the published release holds exactly those files.
4. `release-smoke` installs the release from its real URLs on Linux, macOS and Windows, verifies its provenance, and checks `go install` of the tag.

Don't create a release in the GitHub UI. If `release` or `attest` fails, use "Re-run failed jobs": a rerun of `release` finishes the draft it left, and `attest` can be rerun alone. Re-running all jobs finds the commit already tagged and releases nothing. A commit older than an already-tagged one is never tagged.

CI's `release-snapshot` builds the same release on every pull request, at the tag the merge would get, without publishing, and checks what it built; `install-test` then runs both installers against it, served the way GitHub serves a release. A change that would break a release fails there, before it merges. [Common Commands](#common-commands) shows how to run both locally.

The App key that creates tags is an org secret, readable from any branch's workflow, so anyone who can push a branch could tag and publish from it. A release built anywhere else fails `gh attestation verify` pinned to this workflow on `main`. That catches it when the archive or a downloaded installer is verified, which the README documents and the installers do for the archive when `gh` 2.49 or later is logged in. It does not when `latest/install.sh` is piped to a shell, since a rogue latest release serves its own installer; pinning a version guards against that. The `release` environment holds no secret and is no guard.

The installers and the README verify provenance against `.github/workflows/ci.yml` on `main`, so renaming that file or moving `attest` out of it breaks verification for every published installer.
