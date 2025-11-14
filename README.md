# Release

Release is a lightweight, ergonomic CLI tool for automating GitHub releases, generating and editing changelogs,
attaching release assets, and optionally using an LLM to help polish your changelogs. It is designed for developers who
want a predictable, configurable, and transparent workflow that runs directly in their local repository.

---

## Features

### Core Capabilities

- **Easy GitHub Release Creation**: Uses your local repository metadata to create new GitHub releases.
- **Automatic Changelog Generation**: Incorporates commits since your most recent SemVer tag.
- **Optional LLM-powered Changelog Editing**: If enabled, Release can run your commits through an LLM to draft a polished changelog based on your commits since
  last release.
- **Editor Workflow**: Allows editing of your changelog in your favorite editor. Have to run? Release will store the changes made
  and you can continue where you last left off.
- **Asset Attachments**: Pass multiple `--asset <path>` flags and Release uploads them to GitHub with sane names.
- **Flexible Output Modes**: Pretty, Plain, Silent, JSON: Compatible with `NO_COLOR`.

---

## Configuration

Release requires _some_ configuration in order to interact with Github and different LLMs.

### Sample Configuration

Just put this at `~/.release.toml` and update with the appropriate settings/auth tokens.

```toml
# release.toml — sample configuration
debug = false
output_format = "pretty"

[llm]
enable = false
provider = "openai"
model = "gpt-4o-mini"
token = "replace_me"
max_commits = 120

[github]
token = "replace_me"
```

_Most things set here can be overridden via env vars or CLI flags._

Release loads configuration from **three layers**, lowest to highest precedence:

1. **Sane defaults**
2. **Configuration file**
3. **Environment variables**
4. **Command-line flags**

This allows seamless defaults while still permitting overrides.

### Configuration File Locations

Release looks for a configuration file in the following locations:

1. `~/.release.toml`
2. `~/.config/release.toml`

You may override this by passing the `--config-file-path <path>` flag.

#### For Devs/Development

Any build *not* compiled with `--release` automatically uses a `_dev` suffix:

1. `~/.release_dev.toml`
2. `~/.config/release_dev.toml`

In order to aid in development by not confusing your dev settings with actual ones.

### Environment Variables

Environment variables override file-based settings. This makes it easy to change settings on the fly via `export`.

| Variable                  | Description                              |
|---------------------------|------------------------------------------|
| `NO_COLOR`                | Disables all terminal colors.            |
| `RELEASE_DEBUG`           | Turns on extra debugging output.         |
| `RELEASE_OUTPUT_FORMAT`   | Enables different CLI outputting formats.|
| `RELEASE_LLM__ENABLE`     | Enables/disables LLM usage.              |
| `RELEASE_LLM__PROVIDER`   | LLM backend: e.g. "openai" or "gemini".  |
| `RELEASE_LLM__MODEL`      | LLM Model name, e.g. `gpt-4o-mini`.      |
| `RELEASE_LLM__TOKEN`      | Authentication token for chosen LLM.     |
| `RELEASE_LLM__MAX_COMMITS`| The number of commits in which LLM functions will disable to save on costs.|
| `RELEASE_GITHUB__TOKEN`   | Authentication token for Github.         |

> Note: All environment variables map to the internal configuration structure of `CliConfig`.

--

## Usage

From the root directory of your project (must be a GitHub-hosted repo):

```bash
release 1.4.2 --asset ./my_binary --asset ./docs.zip
```

The workflow:

1. Release determines the commits since your last SemVer tag to whatever your current main is.
2. A changelog template is generated with the commit messages.
3. Release opens your `$EDITOR` so you can refine it.
4. You confirm the release details.
5. GitHub release is created, and assets are uploaded.
6. The release will also tag the commit at current main with the SemVer you defined.

### Requirements

- You must run Release from inside the root of a git repository.
- Your repo must have a GitHub remote (`origin`).
- Your version must be a valid **SemVer** string.
- A GitHub personal access token must be configured.
