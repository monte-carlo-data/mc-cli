# montecarlo

The command-line interface for the Monte Carlo REST API.

## Install

Until the first release, build from source with a Go toolchain:

```bash
git clone https://github.com/monte-carlo-data/mc-cli.git
cd mc-cli
go build -o . ./cmd/montecarlo
```

Once released, `go install github.com/monte-carlo-data/mc-cli/cmd/montecarlo@latest` will work too.

## Credentials

Write a profile once. Either OAuth client credentials with the instance they belong to:

```bash
montecarlo profile set default --client-id <id> --client-secret-prompt --instance us1
```

or an API token:

```bash
montecarlo profile set default --api-id <id> --api-token-prompt
```

Profiles live in `~/.mcd/profiles.ini`, the file the other Monte Carlo tools read too. `profile list` shows them, `profile use <name>` picks the one commands run with, and `--profile <name>` overrides that for one command. Any credential can also be passed as a flag or as an `MCD_DEFAULT_*` environment variable.

## Use

```bash
montecarlo whoami
montecarlo deployments list
montecarlo deployments get <deployment_id> --output json
montecarlo collection-agents register aws --deployment-id <id> --lambda-function-arn <arn> --role-arn <arn>
```

A destructive command asks first, `Delete deployment <id>? [y/N]`; `--yes` answers for you, and is required when there is no terminal. `--output table`, `--output wide` (the table with every field) or `--output json` forces one; the default is a table on a terminal and JSON otherwise. A secret flag accepts `@<path>` to read its value from a file, and has a `--<name>-prompt` companion. A transient 503 or 429 is retried for up to five minutes, with progress on stderr; Ctrl-C ends the wait. A list the API serves in pages prints every item by default, following the cursor page by page. Pass `--cursor`, `--limit` or `--with-count` to see one page instead: a table ending with `Total: <count>` when the page carries a count, and `Next page: --cursor "<value>"` when there is more. As JSON, the whole list is an array of items and one page is the full envelope.

`montecarlo --help` lists the commands; each API resource is a group, each operation a command under its verb.

Each request names the CLI and the command that made it, e.g. `deployments get`, in `x-mcd-telemetry-*` headers, as the other Monte Carlo tools do. Arguments and flag values are never sent in them.

## Exit codes

Scripts can tell failures apart by the exit code:

| Code | Meaning |
|------|---------|
| 0 | Success. |
| 1 | The command failed for a reason not listed below: an API error such as a 500 or a 409, a network failure, or a local error. |
| 2 | The command line is wrong: an unknown command or flag, a flag value of the wrong type or outside its allowed values, a missing argument or required flag, flags that do not go together, or a confirmation without `--yes` when there is no terminal. |
| 3 | Not found: the API answered 404. |
| 4 | The API refused the request: it answered 401 (credentials rejected) or 403 (not permitted). |
| 5 | The API answered 503 or 429, unavailable or limiting requests, and was still answering it when the command ended, after any retries. |
| 6 | The validations the command ran did not all pass. |
| 130 | A confirmation was declined, or the command was interrupted with Ctrl-C or SIGTERM. |

## Shell completion

```bash
source <(montecarlo completion zsh)   # or bash, fish, powershell
```

`montecarlo completion --help` shows how to install it permanently. Completion covers commands, flags, enum values, profile names and output formats.
