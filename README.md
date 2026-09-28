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

## Shell completion

```bash
source <(montecarlo completion zsh)   # or bash, fish, powershell
```

`montecarlo completion --help` shows how to install it permanently. Completion covers commands, flags, enum values, profile names and output formats.
