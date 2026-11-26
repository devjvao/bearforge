# Bearforge

Manage related Git repositories in named workspaces, with a CLI and terminal
dashboard. Each workspace gets its own Git worktrees, so you can keep several
features in progress while sharing repository storage.

- Sign in to GitHub or Bitbucket and import repositories.
- Create workspaces containing the repositories you need for a task.
- Inspect branches, local changes, and pull requests from the dashboard.
- Fetch updates and run commands across repositories.
- Preview workspace changes and recover interrupted operations.

Supports Windows, macOS, and Linux, with GitHub and Bitbucket Cloud,
Server, and Data Center integrations.

## Install

Download an archive for your platform from [Releases](https://github.com/devjvao/bearforge/releases),
follow the [verification instructions](docs/releases.md), and put the
`bearforge` executable on your `PATH`. Git 2.40 or newer is required.

Version v0.2.0 predates the auth/import flow below. Use a current
[source build](docs/development/README.md) to try those features.

## Quick start

### Sign in

Choose the provider you use:

```sh
# GitHub
bearforge auth login --provider github --connection personal-github

# Bitbucket
bearforge auth login --provider bitbucket --connection work-bitbucket
```

Guided login opens the provider's token setup page and saves your token in your
operating system's credential store. See [authentication setup](docs/onboarding.md#authenticate-github-or-bitbucket)
for token permissions and company servers.

### Import repositories and create a workspace

Enter the directory where you want Bearforge to keep repositories and workspaces.
For a new installation, choose a directory outside any existing Bearforge root.

**PowerShell**

```powershell
New-Item -ItemType Directory -Force -Path "$HOME/managed-code" | Out-Null
Set-Location -LiteralPath "$HOME/managed-code"
```

**POSIX shell**

```sh
mkdir -p "$HOME/managed-code"
cd "$HOME/managed-code"
```

From that directory, open the dashboard.

```sh
bearforge tui
```

1. Press `i` to import repositories. Choose your connection and repository scope,
   load the list, select repositories, then press Enter to preview and `y` to import.
2. Press `n` to create a named workspace.
3. Press `a` to add imported repositories to that workspace.
4. Use `s` to fetch updates and `p` to look up pull requests.

Import registers repositories; adding them to a workspace creates the working
directories for your task. The [dashboard guide](docs/dashboard.md) covers the
full controls and confirmation steps.

### Use the CLI

You can also register repositories and create workspaces directly. Replace the
example URL with a repository you can access:

```sh
bearforge repo add api git@github.com:your-org/api.git
bearforge workspace create feature-login
bearforge workspace add feature-login api
bearforge status feature-login
bearforge tui feature-login
```

You can run these commands from a managed worktree too. Bearforge searches the
current directory and its parents for the nearest `.bearforge` directory. If it
finds none, it uses the current directory.

When upgrading, enter your previous root (`~/code` for the old default). Your
catalog, workspaces and operation journals are usable in place. In scripts and CI,
set the command's working directory to that root.

For everyday commands, run `bearforge --help` or `bearforge COMMAND --help`.

## Documentation

| Guide | Contents |
| --- | --- |
| [Onboarding](docs/onboarding.md) | Authentication, import, CLI workflows, configuration, and recovery |
| [Dashboard](docs/dashboard.md) | Keyboard controls, repository selection, and workspace actions |
| [Development](docs/development/README.md) | Source builds, tests, CI, and platform requirements |
| [Releases](docs/releases.md) | Download verification, release builds, and signing |
| [PR reviews](docs/pr-reviews.md) | Automated reviews, GitHub App setup, and contributor approval |
| [Architecture](docs/architecture/0001-bearforge-blueprint.md) | Storage, safety, and implementation design |

## License

[MIT](LICENSE)
