use crate::err;
use anyhow::{Context, Result, anyhow, bail};
use polyfmt::debug;

/// A convenience function for getting the organization name and repo name for a project hosted on Github.
///
/// We parse the URL here because organization/repo combination is really a Github concept not so much a Git concept.
pub fn get_org_and_repo(repo: &git2::Repository) -> Result<(String, String)> {
    let remote = repo.find_remote("origin").context(err!(
        "Could not find remote 'origin'; \
            remote origin required in order to parse organization/repo"
    ))?;

    let url = remote.url().context(err!("Remote has no URL"))?;
    let trimmed = url.trim_end_matches(".git");

    // Split on both ':' and '/' so we cover:
    // - git@github.com:org/repo.git
    // - https://github.com/org/repo.git
    // - ssh://git@github.com/org/repo.git
    let parts: Vec<&str> = trimmed.split(['/', ':']).collect();

    // Next we find the segment containing "github.com" so we can just count from there.
    let idx = parts
        .iter()
        .position(|s| s.contains("github.com"))
        .ok_or_else(|| anyhow!(err!("URL '{}' does not look like a GitHub URL", url)))?;

    if idx + 2 >= parts.len() {
        return Err(anyhow!(err!(
            "Could not parse organization and repo name from '{}'",
            url
        )));
    }

    let org = parts[idx + 1].to_string();
    let repo = parts[idx + 2].to_string();

    Ok((org, repo))
}

/// Attempt to determine the default branch using the symbolic reference
/// `refs/remotes/origin/HEAD`.
///
/// When you run `git fetch`, Git often creates a symbolic reference
/// `refs/remotes/origin/HEAD` that points to the remote’s default branch.
/// For example it might point to:
///
///     refs/remotes/origin/main
///
/// This function tries to:
///   1. Resolve the symbolic target (the branch it points to)
///   2. Convert the remote-tracking branch to a local branch name
///      (e.g., `refs/heads/main`)
///   3. Return whichever branch actually resolves to a valid commit OID
///
/// If either the local branch or the remote-tracking branch resolves to a
/// concrete commit, we treat that as the default branch.
///
/// Returns `None` if the origin/HEAD ref does not exist or cannot be resolved.
fn resolve_from_origin_head(repo: &git2::Repository) -> Option<(String, git2::Oid)> {
    let origin_head = repo.find_reference("refs/remotes/origin/HEAD").ok()?;
    let sym = origin_head.symbolic_target()?;

    // Example: sym = "refs/remotes/origin/main"
    let suffix = sym.strip_prefix("refs/remotes/origin/").unwrap_or(sym);
    let local_heads = format!("refs/heads/{suffix}");

    // Try resolving the corresponding *local* branch first
    let local_oid = repo
        .find_reference(&local_heads)
        .ok()
        .and_then(|r| r.target());

    if let Some(oid) = local_oid {
        debug!("Using local default branch '{}'", local_heads);
        return Some((local_heads, oid));
    }

    // If that fails, try the *remote-tracking* branch directly
    let remote_oid = repo.find_reference(sym).ok().and_then(|r| r.target());

    if let Some(oid) = remote_oid {
        debug!("Using remote-tracking default branch '{}'", sym);
        return Some((sym.to_string(), oid));
    }

    // Symbolic reference exists but does not point to a resolvable commit
    debug!(
        "origin/HEAD resolved to '{}', but no direct target OID found",
        sym
    );
    None
}

/// Attempt to determine the default branch by asking the remote directly.
///
/// Git remotes (e.g., "origin") can advertise their default branch.
/// libgit2 exposes this via `remote.default_branch()`, which typically
/// returns something like:
///
///     "refs/heads/main"
///
/// This function tries:
///   1. Resolving that ref directly as a local reference
///   2. If that fails, constructing the matching remote-tracking ref
///      (e.g., `refs/remotes/origin/main`)
///
/// If either resolves to a commit OID, we treat that as the default branch.
///
/// Returns `None` if:
///   - The remote has no default branch
///   - The ref cannot be resolved locally
///   - The ref is malformed or non-UTF8
fn resolve_from_remote_default(repo: &git2::Repository) -> Option<(String, git2::Oid)> {
    let remote = repo.find_remote("origin").ok()?;
    let buf = remote.default_branch().ok()?;
    let name = buf.as_str()?;

    debug!("Remote default branch: {}", name); // e.g., refs/heads/main

    // Try the advertised ref as-is
    let local_oid = repo.find_reference(name).ok().and_then(|r| r.target());

    if let Some(oid) = local_oid {
        debug!("Using local ref '{}'", name);
        return Some((name.to_string(), oid));
    }

    // Try the corresponding remote-tracking branch if it's a heads ref
    let suffix = name.strip_prefix("refs/heads/")?;
    let remote_tracking = format!("refs/remotes/origin/{suffix}");

    let remote_oid = repo
        .find_reference(&remote_tracking)
        .ok()
        .and_then(|r| r.target());

    if let Some(oid) = remote_oid {
        debug!("Using remote-tracking ref '{}'", remote_tracking);
        return Some((remote_tracking, oid));
    }

    debug!("Could not resolve OID for '{}'", name);
    None
}

/// Determine the repository's default branch using a multi-strategy fallback.
///
/// Git does not have a *universal* way to know the "default branch."
/// Different remotes and Git versions expose this information in different
/// ways, so we try several approaches in priority order:
///
/// 1. **Local `origin/HEAD` symbolic ref**
///    This is the most accurate if you've recently fetched.
///    It usually points to the remote’s actual default branch.
///
/// 2. **Remote-advertised default branch (`remote.default_branch()`)**
///    This asks the remote what its default branch is. Useful if
///    `origin/HEAD` isn't set locally.
///
/// 3. **Fallback to the user's current HEAD**
///    If all else fails, we assume the current branch/commit is the base.
///
/// Each strategy returns the pair:
///
///     (branch_name, commit_oid)
///
/// `branch_name` is something like `"refs/heads/main"` or
/// `"refs/remotes/origin/main"`.
fn resolve_default_base(repo: &git2::Repository) -> Result<(String, git2::Oid)> {
    if let Some(result) = resolve_from_origin_head(repo) {
        return Ok(result);
    }

    if let Some(result) = resolve_from_remote_default(repo) {
        return Ok(result);
    }

    debug!("Falling back to current HEAD");
    let head = repo.head().context("repository has no HEAD")?;
    let base_oid = head.target().context("HEAD has no target")?;
    let base_name = head.name().unwrap_or("HEAD").to_string();

    Ok((base_name, base_oid))
}

/// Gather commits on the repository’s default branch that occurred
/// *after the most recent SemVer tag*, using GitHub-style comparison semantics.
///
/// ## What this does
///
/// This mirrors how GitHub computes the “Compare: <tag>...<branch>” view:
///
/// 1. **Find all tags that look like SemVer**
///    - We ignore tags that don’t parse as SemVer (e.g. "test", "alpha", etc.)
///    - We select the *numerically highest* SemVer (`max_by`), regardless of
///      which branch it appears on.
///
/// 2. **Find the commit the tag points to**
///    - Git tags can point directly to a commit or to an annotated tag object.
///      `peel_to_commit()` resolves that automatically.
///
/// 3. **Determine the repo’s default branch**
///    - We use `resolve_default_base()` to determine the default branch name
///      and its HEAD commit (usually "refs/heads/main").
///
/// 4. **Find the merge-base between the tag and the default branch**
///    - The merge-base is the “best common ancestor” of the two commits.
///    - GitHub’s `A...B` syntax shows commits reachable from `B` but *not* from
///      the merge-base.
///
/// 5. **Walk the commit history of the default branch**
///    - Starting at its HEAD
///    - Hide the merge-base (if it exists)
///    - Collect all commits since that point
///
/// ## Returned values
///
/// The function returns:
///
/// - **The reference for the latest SemVer tag**
/// - **A list of commits on the default branch since that tag**
pub fn get_commits_after_latest_tag<'repo>(
    repo: &'repo git2::Repository,
) -> Result<(git2::Reference<'repo>, Vec<git2::Commit<'repo>>)> {
    //
    // ---- 1. Collect all SemVer-looking tags ----
    //
    let refs = repo
        .references_glob("refs/tags/*")
        .context("could not retrieve tags")?;

    let mut tags: Vec<(semver::Version, git2::Reference<'repo>)> = Vec::new();

    for reference_result in refs {
        let reference = reference_result.context("error iterating over tags")?;

        // Tag must have a “shorthand”, e.g. "v1.2.3"
        let Some(name) = reference.shorthand() else {
            debug!("Skipping tag without shorthand {:?}", reference.name());
            continue;
        };

        // Allow "v1.2.3" or "1.2.3"
        let semver_str = name.strip_prefix('v').unwrap_or(name);

        // Only include valid SemVer tags
        let Ok(ver) = semver::Version::parse(semver_str) else {
            debug!("Skipping non-semver tag {:?}", reference.name());
            continue;
        };

        tags.push((ver, reference));
    }

    // No SemVer tags found → cannot compute compare range
    let Some((latest_ver, latest_tag_ref)) = tags.into_iter().max_by(|(a, _), (b, _)| a.cmp(b))
    else {
        bail!("no semver tags found");
    };

    //
    // ---- 2. Peel the tag to the commit it ultimately refers to ----
    //
    let tag_commit = latest_tag_ref.peel_to_commit().with_context(|| {
        format!(
            "could not peel tag {}",
            latest_tag_ref.name().unwrap_or("?")
        )
    })?;

    let tag_oid = tag_commit.id();
    debug!("Latest semver tag chosen: {} @ {}", latest_ver, tag_oid);

    //
    // ---- 3. Determine the repository’s default branch ----
    //
    // Usually resolves to something like:
    //   ("refs/heads/main", <oid>)
    //
    let (base_name, base_oid) = resolve_default_base(repo)?;
    debug!("Default base resolved to {} @ {}", base_name, base_oid);

    //
    // ---- 4. Find merge-base of <tag> and <default branch> ----
    //
    // If the tag is from an entirely unrelated branch, merge-base may not exist.
    //
    let merge_base = repo.merge_base(tag_oid, base_oid).ok();

    //
    // ---- 5. Walk commits on the default branch since the merge-base ----
    //
    // This reproduces GitHub’s "A...B" behavior (three-dot syntax):
    //   - Start at B (the default branch)
    //   - Exclude the merge-base
    //   - Walk backwards in time
    //
    let mut revwalk = repo.revwalk().context("could not create revwalk")?;
    revwalk
        .set_sorting(git2::Sort::TIME)
        .context("could not set revwalk sorting")?;
    revwalk.push(base_oid).context("could not push base OID")?;

    if let Some(mb) = merge_base {
        revwalk.hide(mb).context("could not hide merge-base")?;
    }

    //
    // ---- 6. Collect resulting commits ----
    //
    let mut commits = Vec::new();

    for oid_res in revwalk {
        let oid = oid_res.context("error iterating through commits")?;
        let commit = repo
            .find_commit(oid)
            .context("could not look up commit during revwalk")?;
        commits.push(commit);
    }

    debug!(
        "Collected {} commits since latest tag {}",
        commits.len(),
        latest_ver
    );

    Ok((latest_tag_ref, commits))
}

pub fn get_short_message(commit: &git2::Commit) -> String {
    let full_message = commit.message().unwrap_or_default();
    if let Some(pos) = full_message.find('\n') {
        full_message[..pos].to_string()
    } else {
        full_message.to_string()
    }
}

pub fn get_abbreviated_hash(hash: git2::Oid) -> String {
    let full_hash = hash.to_string();
    if full_hash.len() > 7 {
        full_hash[..7].to_string()
    } else {
        full_hash
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use git2::{Commit, Repository, Signature};
    use std::{env, fs, path::Path};

    fn init_repo_with_remote(url: &str, name_suffix: &str) -> Repository {
        // Build a unique-ish temp path without extra crates
        let mut path = env::temp_dir();
        path.push(format!("git2_org_repo_test_{name_suffix}"));
        // Clean up any leftovers from previous runs
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();

        let repo = Repository::init(&path).unwrap();
        repo.remote("origin", url).unwrap();
        repo
    }

    // New: init a repo without a remote (for commit/tag tests)
    fn init_repo(name_suffix: &str) -> Repository {
        let mut path = env::temp_dir();
        path.push(format!("git2_commits_after_tag_test_{name_suffix}"));
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();

        Repository::init(&path).unwrap()
    }

    fn commit_file<'a, P: AsRef<Path>>(
        repo: &'a Repository,
        path: P,
        contents: &'a str,
        message: &'a str,
    ) -> Commit<'a> {
        let workdir = repo.workdir().expect("repo must have a workdir");
        let full_path = workdir.join(path.as_ref());

        fs::write(&full_path, contents).unwrap();

        let mut index = repo.index().unwrap();
        index.add_path(path.as_ref()).unwrap();
        index.write().unwrap();

        let tree_oid = index.write_tree().unwrap();
        let tree = repo.find_tree(tree_oid).unwrap();

        let sig = Signature::now("Test User", "test@example.com").unwrap();

        // If HEAD exists, use it as parent; otherwise create an initial commit
        let parents: Vec<Commit> = match repo.head() {
            Ok(head) => {
                let parent = head.peel_to_commit().unwrap();
                vec![parent]
            }
            Err(_) => Vec::new(),
        };
        let parent_refs: Vec<&Commit> = parents.iter().collect();

        let oid = repo
            .commit(Some("HEAD"), &sig, &sig, message, &tree, &parent_refs)
            .unwrap();

        repo.find_commit(oid).unwrap()
    }

    #[test]
    fn parses_git_ssh_github_url() {
        let repo = init_repo_with_remote("git@github.com:my-org/my-repo.git", "ssh");

        let (org, name) = get_org_and_repo(&repo).unwrap();
        assert_eq!(org, "my-org");
        assert_eq!(name, "my-repo");
    }

    #[test]
    fn parses_https_github_url() {
        let repo =
            init_repo_with_remote("https://github.com/another-org/awesome-repo.git", "https");

        let (org, name) = get_org_and_repo(&repo).unwrap();
        assert_eq!(org, "another-org");
        assert_eq!(name, "awesome-repo");
    }

    #[test]
    fn parses_ssh_scheme_with_host() {
        let repo = init_repo_with_remote(
            "ssh://git@github.com/yet-another-org/cool-repo.git",
            "ssh_scheme",
        );

        let (org, name) = get_org_and_repo(&repo).unwrap();
        assert_eq!(org, "yet-another-org");
        assert_eq!(name, "cool-repo");
    }

    #[test]
    fn errors_when_no_origin_remote() {
        // Repo with no remotes at all
        let mut path = env::temp_dir();
        path.push("git2_org_repo_test_no_origin");
        let _ = fs::remove_dir_all(&path);
        fs::create_dir_all(&path).unwrap();

        let repo = Repository::init(&path).unwrap();

        let err = get_org_and_repo(&repo).unwrap_err();
        let msg = format!("{err:#}");
        assert!(
            msg.contains("Could not find remote 'origin'"),
            "unexpected error message: {msg}"
        );
    }

    #[test]
    fn returns_commits_after_latest_semver_tag() {
        let repo = init_repo("with_tags");

        // commit 1
        let c1 = commit_file(&repo, "file.txt", "one", "commit 1");

        // tag 1.0.0 on c1
        let sig = git2::Signature::now("Test User", "test@example.com").unwrap();
        repo.tag("1.0.0", c1.as_object(), &sig, "tag 1.0.0", false)
            .unwrap();

        // commit 2
        let c2 = commit_file(&repo, "file.txt", "two", "commit 2");

        // tag 1.1.0 on c2 (this should be the *latest* semver tag)
        repo.tag("1.1.0", c2.as_object(), &sig, "tag 1.1.0", false)
            .unwrap();

        // commit 3 (after latest tag)
        let c3 = commit_file(&repo, "file.txt", "three", "commit 3");

        // New API: GitHub-style compare <latest tag>...<default branch>
        let (latest_tag_ref, commits_after) = get_commits_after_latest_tag(&repo).unwrap();

        assert_eq!(latest_tag_ref.shorthand(), Some("1.1.0"));

        // We only created one commit after 1.1.0, so we expect exactly that one.
        assert_eq!(commits_after.len(), 1);
        assert_eq!(commits_after[0].id(), c3.id());
    }

    #[test]
    fn errors_when_no_tags() {
        let repo = init_repo("no_tags");

        // A couple of commits but *no tags*
        let _c1 = commit_file(&repo, "file.txt", "one", "commit 1");
        let _c2 = commit_file(&repo, "file.txt", "two", "commit 2");

        // New API returns an error when no SemVer tags are present
        let res = get_commits_after_latest_tag(&repo);

        assert!(res.is_err());
        let msg = res.err().unwrap().to_string();
        assert!(
            msg.contains("no semver tags found"),
            "unexpected error message: {msg}"
        );
    }

    #[test]
    fn get_short_message_truncates_at_first_newline() {
        let repo = init_repo("short_message_truncate");

        // Multi-line commit message
        let msg = "First line of message\nSecond line\nThird line";
        let commit = commit_file(&repo, "file.txt", "contents", msg);

        let short = get_short_message(&commit);
        assert_eq!(short, "First line of message");
    }

    #[test]
    fn get_short_message_returns_full_when_no_newline() {
        let repo = init_repo("short_message_full");

        let msg = "Single line message";
        let commit = commit_file(&repo, "file.txt", "contents", msg);

        let short = get_short_message(&commit);
        assert_eq!(short, "Single line message");
    }

    #[test]
    fn get_short_message_handles_empty_message() {
        let repo = init_repo("short_message_empty");

        let msg = "";
        let commit = commit_file(&repo, "file.txt", "contents", msg);

        let short = get_short_message(&commit);
        assert_eq!(short, "");
    }

    #[test]
    fn get_abbreviated_hash_truncates_to_7_chars() {
        // Construct a known Oid from a full 40-char hex string
        let oid = git2::Oid::from_str("0123456789abcdef0123456789abcdef01234567").unwrap();
        let full = oid.to_string();

        // Sanity: git2 always gives a 40-char hex string here
        assert!(full.len() > 7);

        let short = get_abbreviated_hash(oid);

        assert_eq!(short.len(), 7);
        assert_eq!(short, &full[..7]);
    }
}
