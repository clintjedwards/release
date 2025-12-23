use figment::{
    Figment,
    providers::{Env, Format, Toml},
};
use rootcause::prelude::*;
use serde::Deserialize;
use std::path::PathBuf;

pub trait ConfigType: Deserialize<'static> {
    fn default_config() -> &'static str;
    fn config_paths() -> Vec<PathBuf>;
    fn env_prefix() -> &'static str;
}

pub struct Configuration<T: ConfigType> {
    _marker: std::marker::PhantomData<T>,
}

impl<T: ConfigType> Configuration<T> {
    pub fn load(path_override: Option<PathBuf>) -> Result<T, Report> {
        let mut config = Figment::new().merge(Toml::string(T::default_config()));

        if let Some(path) = path_override {
            config = config.merge(Toml::file(path));
        } else {
            for path in T::config_paths() {
                config = config.merge(Toml::file(path));
            }
        }

        // The split function below is actually pretty load bearing.
        // We use a double underscore `__` to differentiate the difference between
        // a level of the struct and a key in that same struct when we read in environment variables.
        //
        // For example, if you have a doubly nested struct `app -> general` with a key that also has an
        // underline like `log_level`, when the resolution of configuration happens there is no
        // determinate way to resolve the difference between a key is named `general_log_level` and a key
        // that is simply just `level` with the potential to be nested as `app -> general -> log`.
        //
        // To solve this we use a double underscore which denotes the difference between what are actual
        // keys and what are levels of the struct we need to dive into.
        config = config.merge(Env::prefixed(T::env_prefix()).split("__"));
        let parsed_config: T = config.extract()?;

        Ok(parsed_config)
    }
}

/// This file is used to set the base default for all configuration.
const DEFAULT_CLI_CONFIG: &str = include_str!("./default_cli_config.toml");

#[derive(Deserialize, Debug, Clone, PartialEq, Eq)]
pub struct CliConfig {
    /// Provides extra debug output.
    pub debug: bool,

    /// What format the CLI will write to the terminal in.
    #[serde(deserialize_with = "crate::cli::deserialize_output_format")]
    pub output_format: crate::cli::OutputFormat,

    /// Whether to use LLMs to help create changelog notes.
    pub llm: Llm,

    /// Github specific configurations.
    pub github: Github,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Eq)]
pub struct Llm {
    pub enable: bool,

    #[serde(deserialize_with = "crate::cli::deserialize_llm")]
    pub provider: Option<crate::cli::Llm>,
    pub model: Option<String>,
    pub token: String,

    /// The threshold in which the LLM function will fail over this number of commits.
    /// This is to prevent large token costs.
    pub max_commits: usize,
}

#[derive(Deserialize, Debug, Clone, PartialEq, Eq)]
pub struct Github {
    pub token: String,
}

impl ConfigType for CliConfig {
    fn default_config() -> &'static str {
        DEFAULT_CLI_CONFIG
    }

    // We look for configuration to help developers not mix up their real config from their development config.
    #[cfg(debug_assertions)]
    fn config_paths() -> Vec<std::path::PathBuf> {
        let user_home = dirs::home_dir().expect("Unable to get home directory");

        vec![
            user_home.join(".release_dev.toml"),
            user_home.join(".config/release_dev.toml"),
        ]
    }

    #[cfg(not(debug_assertions))]
    fn config_paths() -> Vec<std::path::PathBuf> {
        let user_home = dirs::home_dir().expect("Unable to get home directory");

        vec![
            user_home.join(".release.toml"),
            user_home.join(".config/release.toml"),
        ]
    }

    fn env_prefix() -> &'static str {
        "RELEASE_"
    }
}
