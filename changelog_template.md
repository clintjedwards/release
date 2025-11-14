// New release for {{organization}}/{{repo}} v{{version}}
//
// All lines starting with '//' will be excluded from final changelog
//
// Commits since latest tag:
{%- for commit, message in short_commits %}
//   - {{commit}}: {{message}}
{%- endfor %}
//
// Edit changelog below this comment. An example format has been given:

## v{{version}} ({{date}})

#### NEW FEATURES:

* **Feature Name**: Description about new features this release. (<short_commit_hash>)
* **New Feature**: It is also okay to group a single feature broken up over many commits/PRs into a single changelog note. (<short_commit_hash_1>), (<short_commit_hash_2>) [#3](<link>), [#4](<link>)

#### IMPROVEMENTS:

* **Improvement Name**: Description about any new improvements around established features. (<short_commit_hash>)
* **Example Improvement**: This improvement has a PR attached as an example. (5729511) [#10](https://github.com/clintjedwards/experimental/pull/10)

#### BUG FIXES:

* **Bug Name**: Short description of bug and functionality changes. (<short_commit_hash>)
* **Lease revocation fix**: Fix Go API using lease revocation via URL instead of body (<short_commit_hash>)

#### ADDITIONAL NOTES:

* Things the user might want to know about
* Run `binary update 1.14` against the database in order to fully complete migration.
