# montecarlo

The command-line interface for the Monte Carlo REST API.

## Install

### macOS and Linux

```bash
curl -fsSL https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.sh | sh
```

The script installs the latest release to `/usr/local/bin` if it can write there, otherwise to `~/.local/bin`, without `sudo`. It checks the download against the release's checksums and, if [`gh`](https://cli.github.com) (2.49 or later) is installed and logged in, its provenance. To verify the script before running it:

```bash
curl -fsSLO https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.sh
gh attestation verify install.sh --repo monte-carlo-data/mc-cli \
  --signer-workflow monte-carlo-data/mc-cli/.github/workflows/ci.yml --source-ref refs/heads/main \
  --deny-self-hosted-runners
sh install.sh
```

Piping the script to `sh` trusts whatever the latest release serves; verifying it first, or pinning a version, does not.

In CI or a script, pin the version, both of the installer and of what it installs:

```bash
curl -fsSL https://github.com/monte-carlo-data/mc-cli/releases/download/vX.Y.Z/install.sh | MONTECARLO_VERSION=vX.Y.Z sh
```

### Windows

In PowerShell:

```powershell
irm https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.ps1 | iex
```

It installs to `%LOCALAPPDATA%\Programs\montecarlo` and adds that directory to your user `PATH`; open a new terminal to pick it up. It runs the same checks as `install.sh`. To verify the script first, with the same `gh attestation verify` command as above on `install.ps1`:

```powershell
irm https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

To pin the version:

```powershell
$env:MONTECARLO_VERSION = 'vX.Y.Z'
irm https://github.com/monte-carlo-data/mc-cli/releases/download/vX.Y.Z/install.ps1 | iex
```

### Installer settings

Both scripts read these environment variables:

| Variable | Effect |
|----------|--------|
| `MONTECARLO_VERSION` | The release to install, `0.1.3` or `v0.1.3`. The latest by default. |
| `MONTECARLO_INSTALL_DIR` | Where to install. |
| `MONTECARLO_BIN_NAME` | The installed name, `montecarlo` by default. |
| `MONTECARLO_FORCE=1` | Replace a `montecarlo` at the target that is not this CLI. |
| `MONTECARLO_DOWNLOAD_BASE` | Where releases are downloaded from, for a mirror of the releases page. |

Run again to upgrade: the installers replace their own earlier install.

### Download an archive

Each [release](https://github.com/monte-carlo-data/mc-cli/releases) has an archive per platform, a `.tar.gz` for macOS and Linux and a `.zip` for Windows, and a checksums file. Check the archive, then put the binary on your `PATH`:

```bash
sha256sum --ignore-missing -c montecarlo_X.Y.Z_checksums.txt     # macOS: shasum -a 256 --ignore-missing -c
tar xzf montecarlo_X.Y.Z_linux_amd64.tar.gz montecarlo
```

In PowerShell, compare the hash with the archive's line in the checksums file, then extract:

```powershell
(Get-FileHash montecarlo_X.Y.Z_windows_amd64.zip).Hash.ToLower()
Expand-Archive montecarlo_X.Y.Z_windows_amd64.zip
```

A file downloaded in a browser is marked as coming from the internet, and macOS or Windows may then refuse to run the binary, which is not notarized or signed. Clear the mark with `xattr -d com.apple.quarantine montecarlo` on macOS, or `Unblock-File montecarlo.exe` in PowerShell. The install scripts, `curl` and `go install` don't set it.

### Verify where a file came from

The checksums catch a damaged download. To check that a file was built by this repository's release workflow on `main`, verify its provenance with `gh`:

```bash
gh attestation verify montecarlo_X.Y.Z_linux_amd64.tar.gz --repo monte-carlo-data/mc-cli \
  --signer-workflow monte-carlo-data/mc-cli/.github/workflows/ci.yml --source-ref refs/heads/main \
  --deny-self-hosted-runners
```

The archives, the checksums file and both install scripts are attested.

### With Go

```bash
go install github.com/monte-carlo-data/mc-cli/cmd/montecarlo@latest
```

### From source

```bash
git clone https://github.com/monte-carlo-data/mc-cli.git
cd mc-cli
go build -o . ./cmd/montecarlo
```

### Alongside the Python CLI

The Python CLI, `pip install montecarlodata`, also installs a command named `montecarlo`. The install scripts don't replace it unless `MONTECARLO_FORCE=1` is set: if it is where they would install, they stop and say so; if it is elsewhere on `PATH`, they install and warn. Keep both by installing this one under another name:

```bash
curl -fsSL https://github.com/monte-carlo-data/mc-cli/releases/latest/download/install.sh | MONTECARLO_BIN_NAME=mc sh
```

If both keep the name `montecarlo`, whichever comes first on `PATH` runs. An activated virtual environment puts its own `bin` first, so inside it `montecarlo` is the Python CLI; call this one by its full path there. `which -a montecarlo` lists every `montecarlo` on `PATH`, in the order they are found.

## Credentials

Set up your credentials once:

```bash
montecarlo configure
```

It asks whether you use an OAuth client or an API token, prompts for the values, with secrets read hidden, and writes them to the `default` profile, or to the one `--profile` names.

In a script or CI, write the profile with `profile set` instead. Either OAuth client credentials with the instance they belong to:

```bash
montecarlo profile set default --client-id <id> --client-secret @<path> --instance us1
```

or an API token:

```bash
montecarlo profile set default --api-id <id> --api-token @<path>
```

`@<path>` reads the secret from a file; `--client-secret-prompt` and `--api-token-prompt` ask for it on a terminal instead.

Both commands check the credentials with Monte Carlo before writing them, and show the user and account they belong to. Nothing is written when they are rejected, which exits 4. `--no-validate` writes them without checking.

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
| 2 | The command line is wrong: an unknown command or flag, a flag value of the wrong type or outside its allowed values, a missing argument or required flag, a `@<path>` that cannot be read, flags that do not go together, or a confirmation without `--yes` when there is no terminal. |
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
