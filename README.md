# montecarlo

The command-line interface for the Monte Carlo REST API.

## Install

Until the first release, build from source with a Go toolchain:

```bash
go install github.com/monte-carlo-data/mc-cli/cmd/montecarlo@latest
```

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

A delete asks `Delete deployment <id>? [y/N]` first; `--yes` answers for you, and is required when there is no terminal. Output is a table on a terminal and JSON otherwise; `--output json` or `--output table` forces one. Every string flag accepts `@<path>` to read its value from a file. A secret flag has a `--<name>-prompt` companion that reads it from a hidden prompt.

`montecarlo --help` lists the commands; each API resource is a group, each operation a command under its verb.

## Shell completion

```bash
source <(montecarlo completion zsh)   # or bash, fish, powershell
```

`montecarlo completion --help` shows how to install it permanently. Completion covers commands, flags, enum values, profile names and output formats.
