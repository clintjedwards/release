mod changelog;
mod conf;
mod error;
mod git;
mod llm;

use crate::cli::conf::{CliConfig, Configuration};
use bytes::Bytes;
use clap::{Parser, ValueEnum};
use colored::Colorize;
use octocrab::Octocrab;
use polyfmt::{debug, finish, pause, print, println, question, resume, spacer, success, warning};
use rootcause::prelude::*;
use serde::{Deserialize, Serialize, de};
use std::{collections::HashMap, fmt::Debug, io::Write, path::PathBuf};
use strum_macros::{EnumString, VariantNames};

const RELEASE_DETAILS_TEMPLATE: &str = r#"
Final Release Details:
{{ divider }} Organization: {{ organization }}
{{ divider }} Repository:   {{ repository }}
{{ divider }} Version:      {{ semver }}
{{ divider }} Release Date: {{ date }}
{{ divider }} Changelog:    {{ changelog_path }}
{%- if assets | length > 0 %}
{{ divider }} Assets:
{%- for name, path in assets %}
{{ divider }}  • {{ name }}: {{ path }}
{%- endfor -%}
{%- endif -%}
"#;

#[derive(Default, Debug, Clone, ValueEnum, Serialize, PartialEq, Eq, EnumString, VariantNames)]
#[strum(ascii_case_insensitive)]
#[serde(try_from = "String")]
pub(crate) enum OutputFormat {
    #[default]
    Pretty,
    Plain,
    Silent,
    Json,
}

fn deserialize_output_format<'de, D>(deserializer: D) -> Result<OutputFormat, D::Error>
where
    D: de::Deserializer<'de>,
{
    let s = String::deserialize(deserializer)?;

    OutputFormat::from_str(&s, true).map_err(de::Error::custom)
}

impl From<OutputFormat> for polyfmt::Format {
    fn from(value: OutputFormat) -> Self {
        match value {
            OutputFormat::Pretty => polyfmt::Format::Spinner,
            OutputFormat::Plain => polyfmt::Format::Plain,
            OutputFormat::Silent => polyfmt::Format::Silent,
            OutputFormat::Json => polyfmt::Format::Json,
        }
    }
}

/// Release — Helps with Github releases, changelogs, asset uploading.
#[derive(Debug, Parser, Clone)]
#[command(name = "release")]
#[command(bin_name = "release")]
#[command(version)]
// We don't use default values here for configurations that are mentioned in the conf internal crate. This is to keep
// things simple to follow in the code that resolves flags and config options. If we started allowing default in two
// separate locations then resolving mentally would be come difficult.
pub(crate) struct Args {
    /// Version to release (SemVer), e.g. 1.4.2
    semver: String,

    /// Asset(s) to attach to the release (repeatable)
    #[arg(short, long = "asset")]
    assets: Vec<PathBuf>,

    /// Enable LLM operations. This mostly concerns the automatic creation of the changelog.
    #[arg(long, short)]
    pub use_llm: Option<bool>,

    /// LLM provider, e.g. `openai`, `gemini`
    #[arg(long)]
    pub llm_provider: Option<String>,

    /// LLM model, e.g. `gpt-4o-mini`
    #[arg(long)]
    pub llm_model: Option<String>,

    /// Turns on debugging output.
    #[arg(short, long)]
    debug: bool,

    /// Controls output format.
    ///
    /// Pretty has a spinner for dynamic output and progress bars.
    #[arg(short, long, value_enum)]
    output_format: Option<OutputFormat>,

    /// An alternate location to find the configuration file.
    ///
    /// By default the config file is searched for at `release.toml` and `.config/release.toml`
    #[arg(short, long)]
    config_file_path: Option<PathBuf>,
}

#[derive(Debug, Clone)]
pub struct Cli {
    args: Args,
    conf: CliConfig,
    release: Release,
}

// So we never forget to call [`polyfmt::Formatter::finish`]
impl Drop for Cli {
    fn drop(&mut self) {
        finish!();
    }
}

#[derive(Debug, Clone, ValueEnum, Serialize, PartialEq, Eq, EnumString, VariantNames)]
#[strum(ascii_case_insensitive)]
pub(crate) enum Llm {
    Gemini,
    OpenAI,
}

impl TryFrom<String> for Llm {
    type Error = Report;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        match value.to_ascii_lowercase().as_str() {
            "gemini" => Ok(Self::Gemini),
            "openai" => Ok(Self::OpenAI),
            _ => Err(report!(
                "Could not parse LLM vendor into any accepted values (got `{value}`)"
            )),
        }
    }
}

pub fn deserialize_llm<'de, D>(deserializer: D) -> Result<Option<Llm>, D::Error>
where
    D: de::Deserializer<'de>,
{
    // First deserialize to Option<String>
    let opt = Option::<String>::deserialize(deserializer)?;

    // Then map String -> Llm via TryFrom
    opt.map(|s| Llm::try_from(s).map_err(de::Error::custom))
        .transpose()
}

impl From<Llm> for ::llm::builder::LLMBackend {
    fn from(value: Llm) -> Self {
        match value {
            Llm::Gemini => ::llm::builder::LLMBackend::Google,
            Llm::OpenAI => ::llm::builder::LLMBackend::OpenAI,
        }
    }
}

impl Cli {
    pub fn new() -> Result<Self, Report> {
        let args = Args::parse();

        let conf = Cli::resolve_config(&args).context("Could not load configuration")?;

        let output_format = polyfmt::Format::from(conf.output_format.clone());

        error::alter_error_formatter(conf.debug);

        let fmtter_options = polyfmt::Options {
            debug: conf.debug,
            padding: 1,
            ..Default::default()
        };

        let fmtter = polyfmt::new(output_format, fmtter_options);

        polyfmt::set_global_formatter(fmtter);

        let release =
            get_release_info(&args.assets, &args.semver).context("Could not get release info")?;

        let cli = Cli {
            args,
            conf,
            release,
        };

        Ok(cli)
    }

    /// Build the final runtime configuration by layering all supported sources and applying command-line overrides.
    ///
    /// Precedence (lowest to highest):
    ///   1. File-based configuration
    ///   2. Environment variables
    ///   3. Command-line flags
    ///
    /// The merged configuration is then used to set process-wide defaults such as the global debug flag.
    pub fn resolve_config(args: &Args) -> Result<conf::CliConfig, Report> {
        // Load base configuration from file + env.
        let mut conf = Configuration::<CliConfig>::load(args.config_file_path.clone())
            .context("Could not parse configuration")?;

        if let Some(enable_llm) = args.use_llm {
            conf.llm.enable = enable_llm;
        }

        if let Some(llm_provider) = &args.llm_provider {
            let llm_provider: Llm = llm_provider
                .clone()
                .try_into()
                .context("Could not parse llm provider")?;

            conf.llm.provider = Some(llm_provider);
        }

        if let Some(llm_model) = &args.llm_model {
            conf.llm.model = Some(llm_model.to_string());
        }

        if let Some(output_format) = &args.output_format {
            conf.output_format = output_format.clone()
        }

        conf.debug = args.debug;

        Ok(conf)
    }

    pub fn run(&mut self) -> Result<(), Report> {
        print!("Creating release v{}", &self.args.semver; vec![polyfmt::Format::Spinner]);

        (self.release.changelog.0, self.release.changelog.1) = self
            .process_changelog()
            .context("Could not create changelog")?;

        print!("Creating release v{}", &self.args.semver; vec![polyfmt::Format::Spinner]);

        let release_details = self
            .render_release_details()
            .context("Could not render release details")?;

        println!("{}", release_details);
        spacer!();
        let answer = question!("Proceed with release? (y/N): ");

        if !answer.eq_ignore_ascii_case("y") {
            warning!("Release aborted by user");
            return Ok(());
        }

        self.create_github_release()
            .context("Could not create release on Github")?;

        success!("Release successfully created!");

        Ok(())
    }

    /// Opens a pre-populated file for editing and returns the final changelog file path and user contents.
    pub fn process_changelog(&self) -> Result<(PathBuf, String), Report> {
        // First we establish a file name and path. This naming of this file specifically includes the project and the
        // semver so that if the user abandons the changelog editing for any reason we can simply just restore the file on
        // their behalf.

        let prefix = "changelog";
        let file_extension = "md";
        let changelog_filepath = format!(
            "/tmp/{}_{}_{}_{}.{}",
            prefix,
            self.release.organization,
            self.release.repo,
            self.release.version,
            file_extension
        );

        // Next we attempt to recover a changelog file if one already exists and if not we create a new one with the
        // default template. If the file was recovered we skip the rest of the steps and just simply give the user editing
        // control again.

        let changelog_file_recovered = std::fs::metadata(&changelog_filepath).is_ok();

        if changelog_file_recovered {
            success!("Recovered previous changelog ({})", changelog_filepath);

            // We call pause in case we are in "pretty" mode with the spinner so we don't interrupt any terminal
            // based editors.
            print!("Waiting for user input..."; vec![polyfmt::Format::Spinner]);
            println!("Waiting for user input..."; vec![polyfmt::Format::Plain]);

            // We need to pause the spinner to avoid attempting to draw over terminal editors.
            pause!();
            let contents = changelog::get_contents_from_user(&changelog_filepath);
            resume!();

            return contents;
        }

        // If we're working with a new file first build the template then insert it into the new file.

        let mut tera = tera::Tera::default();
        tera.add_raw_template("changelog_template", changelog::CHANGELOG_TEMPLATE)?;

        let mut context = tera::Context::new();
        context.insert("organization", &self.release.organization);
        context.insert("date", &self.release.date);
        context.insert("repo", &self.release.repo);
        context.insert("version", &self.release.version);
        context.insert("short_commits", &self.release.short_commits);

        let content = tera.render("changelog_template", &context)?;

        // If the user prefers to use LLMs to create the changelog we do so here so that they can still have an opportunity
        // to edit said changelog afterwards.

        let content = if self.conf.llm.enable {
            llm::generate_changelog_with_llm(&self.conf.llm, &content, &self.release)
                .context("Could not alter changelog using LLM")?
        } else {
            content
        };

        let mut file = std::fs::OpenOptions::new()
            .write(true)
            .create(true)
            .truncate(false)
            .open(&changelog_filepath)?;

        file.write_all(content.as_bytes())
            .context("Could not write content to file")?;

        // We drop the file explicitly so that we don't try to write to a file open in two places.
        drop(file);

        // We call pause in case we are in "pretty" mode with the spinner so we don't interrupt any terminal
        // based editors.
        print!("Waiting for user input..."; vec![polyfmt::Format::Spinner]);
        println!("Waiting for user input..."; vec![polyfmt::Format::Plain]);

        // We need to pause the spinner to avoid attempting to draw over terminal editors.
        pause!();
        let contents = changelog::get_contents_from_user(&changelog_filepath);
        resume!();

        contents
    }
}

fn get_release_info(assets: &Vec<PathBuf>, semver: &str) -> Result<Release, Report> {
    let repo = match git2::Repository::open(".") {
        Ok(repo) => repo,
        Err(e) => bail!("failed to open local repo: {:#}", e),
    };

    // Process assets so they have names and paths.
    let mut parsed_assets = vec![];
    for asset_path in assets {
        if !asset_path.exists() {
            bail!("Listed asset {:#?} does not exist at path", asset_path);
        }

        // Derive asset name from filename
        let file_name = asset_path
            .file_name()
            .and_then(|s| s.to_str())
            .ok_or_else(|| report!("asset path has no valid UTF-8 file name"))?;

        parsed_assets.push(Asset {
            name: file_name.to_string(),
            path: asset_path.to_path_buf(),
        });
    }

    let release = Release::new(&repo, semver, parsed_assets).context("Could not create release")?;

    Ok(release)
}

#[derive(Debug, Clone)]
pub struct Asset {
    pub name: String,
    pub path: PathBuf,
}

#[derive(Debug, Clone)]
pub struct Release {
    /// The organization for the current repo. In 'clintjedwards/gofer' it would be 'clintjedwards'.
    pub organization: String,

    /// The repo name for the current repo. In 'clintjedwards/gofer' it would be 'gofer'.
    pub repo: String,

    /// The SEMVER version formatted as <Major>.<Minor>.<Path>. ex: 0.9.1
    pub version: String,

    /// The current date formatted as <Month> <Date>, <Year>. ex: January 26, 2025.
    pub date: String,

    /// The path and contents of the changelog.
    pub changelog: (PathBuf, String),

    /// Short commit hashes and their short descriptions. This is included in the changelog template so that users
    /// correctly understand which range of commits is being used here.
    pub short_commits: HashMap<String, String>,

    /// The same corpus as `short_commits` but with the full long commit descriptions. This is not included in the
    /// template but instead given to LLM, should the user choose to use one. This helps the LLM create better
    /// descriptions according to the template provided.k
    pub full_commits: HashMap<String, String>,

    /// The name and path to all the assets included in the release.
    pub assets: Vec<Asset>,
}

impl Release {
    pub fn new(
        repository: &git2::Repository,
        version: &str,
        assets: Vec<Asset>,
    ) -> Result<Self, Report> {
        semver::Version::parse(version).context(format!(
            "Could not parse version '{}' according to SEMVER syntax",
            version
        ))?;

        let (org, repo) = git::get_org_and_repo(repository)
            .context("Could not get organization and repo from git")?;

        let (_last_tag, commits) = git::get_commits_after_latest_tag(repository)
            .context("Could not get commits after latest tag while creating new release")?;

        let mut short_commits = HashMap::new();
        let mut full_commits = HashMap::new();

        for commit in &commits {
            short_commits.insert(
                git::get_abbreviated_hash(commit.id()),
                git::get_short_message(commit),
            );

            full_commits.insert(
                commit.id().to_string(),
                commit.message().unwrap_or("").into(),
            );
        }

        let now = chrono::Local::now();

        // e.g., "January 26, 2025"
        let date = now.format("%B %d, %Y").to_string();

        Ok(Self {
            organization: org,
            repo,
            version: version.to_string(),
            date,
            changelog: (PathBuf::new(), "".to_string()),
            full_commits,
            short_commits,
            assets,
        })
    }
}

async fn upload_asset(
    client: &Octocrab,
    owner: &str,
    repo: &str,
    release_id: u64,
    asset: &Asset,
) -> Result<(), Report> {
    use tokio::fs;

    let data = fs::read(&asset.path)
        .await
        .context(format!("could not read asset from {:#?}", asset.path))?;

    let body = Bytes::from(data);

    client
        .repos(owner.to_owned(), repo.to_owned())
        .releases()
        .upload_asset(release_id, &asset.name, body)
        // optionally .label("Some nice label")
        .send()
        .await
        .context(format!(
            "GitHub upload_asset call failed for {}",
            asset.name
        ))?;

    Ok(())
}

impl Cli {
    pub fn create_github_release(&self) -> Result<(), Report> {
        debug!("Starting Github release");

        let tag = format!("v{}", self.release.version);

        let rt = tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
            .context("failed to create Tokio runtime")?;

        debug!("Contacting Github to create release");

        rt.block_on(async {
            let client = Octocrab::builder()
                .personal_token(self.conf.github.token.clone())
                .build()
                .context("failed to build GitHub client")?;

            let created_release = client
                .repos(&self.release.organization, &self.release.repo)
                .releases()
                .create(&tag)
                .name(&tag)
                .body(&self.release.changelog.1)
                .send()
                .await
                .context("failure while attempting to create Github release")?;

            success!(
                "Created new Github release: {}",
                created_release.html_url.as_str()
            );

            print!("Uploading release assets"; vec![polyfmt::Format::Spinner]);
            for asset in &self.release.assets {
                upload_asset(
                    &client,
                    &self.release.organization,
                    &self.release.repo,
                    created_release.id.0,
                    asset,
                )
                .await
                .context(format!(
                    "Could not upload asset {} ({:#?}) to release",
                    asset.name, asset.path,
                ))?;

                success!("Successfully uploaded asset '{}'", asset.name);
            }

            Ok::<_, Report>(())
        })?;

        Ok(())
    }

    fn render_release_details(&self) -> Result<String, Report> {
        let mut tera = tera::Tera::default();
        tera.add_raw_template("release_details", RELEASE_DETAILS_TEMPLATE)
            .context("Could not create text template")?;

        let colored_assets: HashMap<String, String> = self
            .release
            .assets
            .iter()
            .map(|asset| {
                (
                    asset.name.blue().to_string(),
                    asset.path.display().to_string(),
                )
            })
            .collect();

        let mut context = tera::Context::new();
        context.insert("divider", &"│".magenta().to_string());
        context.insert(
            "organization",
            &self.release.organization.blue().to_string(),
        );
        context.insert("repository", &self.release.repo.blue().to_string());
        context.insert(
            "semver",
            &format!("v{}", self.release.version).blue().to_string(),
        );
        context.insert("date", &self.release.date.blue().to_string());
        context.insert("assets", &colored_assets);
        context.insert(
            "changelog_path",
            &self
                .release
                .changelog
                .0
                .to_string_lossy()
                .blue()
                .to_string(),
        );

        let rendered = tera
            .render("release_details", &context)
            .context("Could not render text template")?;
        Ok(rendered)
    }
}
