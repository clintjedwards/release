use crate::cli::Release;
use crate::err;
use anyhow::{Context, Result, anyhow};
use llm::builder::{LLMBackend, LLMBuilder};
use llm::chat::ChatMessage;
use polyfmt::{print, warning};

static LLM_TEMPLATE: &str = include_str!("../../llm_template.md");

pub fn generate_changelog_with_llm(
    llm_settings: &crate::cli::conf::Llm,
    changelog_template: &str,
    release: &Release,
) -> Result<String> {
    print!("Generating changelog"; vec![polyfmt::Format::Spinner]);

    validate_llm_settings(llm_settings).context(err!("Could not validate LLM settings"))?;

    if release.short_commits.len() > llm_settings.max_commits {
        warning!(
            "Could not query LLM due to max commits threshold being exceeded. {} found / {} max",
            release.short_commits.len(),
            llm_settings.max_commits
        );
        return Ok(changelog_template.to_owned());
    }

    let mut tera = tera::Tera::default();
    tera.add_raw_template("llm_template", LLM_TEMPLATE)
        .context(err!("Could not render prompt from template"))?;

    let mut context = tera::Context::new();
    context.insert("org", &release.organization);
    context.insert("repo", &release.repo);
    context.insert("version", &release.version);
    context.insert("template", changelog_template);
    context.insert("commits", &release.full_commits);

    let prompt = tera.render("llm_template", &context)?;

    let rt = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .context("failed to create Tokio runtime")?;

    let raw = rt.block_on(async {
        // unwrap is okay due to validate call earlier.
        let backend: LLMBackend = llm_settings.provider.clone().unwrap().into();

        let mut provider = LLMBuilder::new()
            .backend(backend)
            .api_key(llm_settings.token.clone());

        if let Some(model) = &llm_settings.model {
            provider = provider.model(model);
        }

        let provider = provider.build().context("failed to build LLM provider")?;
        let msgs = vec![ChatMessage::user().content(prompt).build()];

        let resp = provider.chat(&msgs).await.context("chat request failed")?;
        let text = resp.text().unwrap_or_default();

        Ok::<_, anyhow::Error>(text)
    })?;

    // Strip lone triple-backtick fences if the model wrapped output in Markdown
    let cleaned = raw
        .lines()
        .filter(|l| l.trim() != "```")
        .collect::<Vec<_>>()
        .join("\n");

    Ok(cleaned)
}

fn validate_llm_settings(llm_settings: &crate::cli::conf::Llm) -> Result<()> {
    // This only runs if enable has been checked in the early part of the program so we don't
    // have to validate that here.

    if llm_settings.token.is_empty() || llm_settings.token == "replace_me" {
        return Err(anyhow!(err!(
            "LLM token missing; Please set required LLM auth token in configuration file or via env var `RELEASE_LLM__TOKEN`"
        )));
    };

    if llm_settings.provider.is_none() {
        return Err(anyhow!(err!(
            "LLM provider missing. Please provide one via config file or env var `RELEASE_LLM__PROVIDER` \
            (e.g. provider='gemini')"
        )));
    };

    Ok(())
}
