use polyfmt::debug;
use rootcause::prelude::*;
use std::{env, path::PathBuf, str};

/// EDITOR is an environment variable that usually points to the default editor path for the current user.
/// Typically this can also point to a backup editor in case VISUAL fails.
const EDITOR_ENV_VAR: &str = "EDITOR";

/// VISUAL is an environment variable that usually points to the default gui or full screen editor for the current user.
/// Typically this is used primarily with EDITOR acting as a fallback.
const VISUAL_ENV_VAR: &str = "VISUAL";

/// A default editor we find the path for and then use if all other editors fallthrough.
const DEFAULT_EDITOR: &str = "vi";

pub(crate) static CHANGELOG_TEMPLATE: &str = include_str!("../../changelog_template.md");

// Return a suitable editor path and arguments.
fn get_editor_path() -> Result<String, Report> {
    // First we try VISUAL
    if let Ok(val) = env::var(VISUAL_ENV_VAR)
        && !val.is_empty()
    {
        return Ok(val);
    }

    if let Ok(val) = env::var(EDITOR_ENV_VAR)
        && !val.is_empty()
    {
        return Ok(val);
    }

    if let Ok(val) = which::which(DEFAULT_EDITOR)
        && !val.to_string_lossy().is_empty()
    {
        return Ok(val.to_string_lossy().into());
    }

    bail!(
        "Could not find a suitable editor to edit changelog. Please make sure your VISUAL and/or EDITOR \
        environment variables are set."
    )
}

fn open_file_in_editor(file_path: &str) -> Result<(), Report> {
    let editor_path = get_editor_path()?;

    // Split the path parsed into parts so we can manipulate and add into Command func
    let mut editor_path_parts: Vec<&str> = editor_path.split_whitespace().collect();
    editor_path_parts.push(file_path);

    let (cmd, args) = editor_path_parts
        .split_first()
        .ok_or_else(|| std::io::Error::other("Editor path is empty or invalid"))?;

    std::process::Command::new(cmd)
        .args(args)
        .stdin(std::process::Stdio::inherit())
        .stdout(std::process::Stdio::inherit())
        .stderr(std::process::Stdio::inherit())
        .status()?;

    Ok(())
}

pub(crate) fn get_contents_from_user(file_path: &str) -> Result<(PathBuf, String), Report> {
    open_file_in_editor(file_path)?;

    let content = std::fs::read_to_string(file_path)?;

    let content = remove_file_comments(&content);

    // We save the final contents of the changelog in a file so that the user can choose to view it using their own
    // markdown preview.
    let mut final_file_path = PathBuf::from(file_path);

    let stem = final_file_path.file_stem().unwrap().to_string_lossy();
    let ext = final_file_path
        .extension()
        .and_then(|e| e.to_str())
        .ok_or(report!(
            "Expected extension '.md' not found for changelog file"
        ))
        .context("Could not write final changelog file")?;

    final_file_path.set_file_name(format!("{stem}_final.{ext}"));

    std::fs::write(&final_file_path, &content).context("Could not write final changelog file")?;
    debug!("Wrote final changelog file: {:#?}", final_file_path);

    Ok((final_file_path, content))
}

fn remove_file_comments(data: &str) -> String {
    let mut new_lines = Vec::new();

    for line in data.lines() {
        let trimmed = line.trim_start();
        if !trimmed.starts_with("//") {
            new_lines.push(line);
        }
    }

    new_lines.join("\n")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_get_editor_path() {
        let path = get_editor_path().unwrap();
        assert!(!path.is_empty());
    }
}
