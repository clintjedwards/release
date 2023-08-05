package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/go-git/go-git/plumbing/storer"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type commitType string

const (
	ci       commitType = "ci"
	docs     commitType = "docs"
	feat     commitType = "feat"
	fix      commitType = "fix"
	refactor commitType = "refactor"
	revert   commitType = "revert"
	other    commitType = "other"
)

type commitInfo struct {
	kind     commitType
	commit   *object.Commit
	breaking bool
}

var commitTypes = map[commitType]struct{}{ci: {}, docs: {}, feat: {}, fix: {}, refactor: {}, revert: {}, other: {}}

// Loosely follows conventional commits
// https://github.com/conventional-changelog/commitlint/tree/master/%40commitlint/config-conventional
// returns malformed commit strings along with successfully parsed strings
func parseConventionalCommits(commits []*object.Commit) ([]commitInfo, []string) {
	parsedCommits := []commitInfo{}
	malformedCommits := []string{}

	for _, commit := range commits {
		commitInfo, err := parseCommitToInfo(commit)
		if err != nil {
			malformedCommits = append(malformedCommits, commit.Message)
			continue
		}

		parsedCommits = append(parsedCommits, commitInfo)
	}

	return parsedCommits, malformedCommits
}

// parseCommitToInfo returns the given commit as a commitInfo type
func parseCommitToInfo(commit *object.Commit) (commitInfo, error) {
	msgSplit := strings.SplitN(commit.Message, ":", 2)
	if len(msgSplit) < 2 {
		return commitInfo{}, fmt.Errorf("could not properly split commit")
	}

	commitTag := msgSplit[0]
	lastchar := commitTag[len(commitTag)-1]
	breaking := false
	if lastchar == '!' {
		breaking = true
		commitTag = commitTag[:len(commitTag)-1]
	}

	kind := commitType(commitTag)
	if _, found := commitTypes[kind]; !found {
		return commitInfo{}, fmt.Errorf("could not parse commit type; %s is not a valid type", kind)
	}

	return commitInfo{
		kind:     kind,
		commit:   commit,
		breaking: breaking,
	}, nil
}

func getCommitsAfterLatestTag(repo *git.Repository) (*plumbing.Reference, []*object.Commit, error) {
	// Get all the tags
	tagRefs, err := repo.Tags()
	if err != nil {
		return nil, nil, fmt.Errorf("could not retrieve tags: %w", err)
	}

	// Store all tags in a slice
	var tags []*plumbing.Reference
	err = tagRefs.ForEach(func(t *plumbing.Reference) error {
		if _, err := semver.NewVersion(t.Name().Short()); err == nil {
			tags = append(tags, t)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("could not iterate over tags")
	}

	// If there are no tags, return nil for latestTag and an empty commits list
	if len(tags) == 0 {
		return nil, []*object.Commit{}, nil
	}

	// Sort the tags by SemVer
	sort.Slice(tags, func(i, j int) bool {
		v1, _ := semver.NewVersion(tags[i].Name().Short())
		v2, _ := semver.NewVersion(tags[j].Name().Short())
		return v1.LessThan(v2)
	})

	// Get the latest tag
	latestTag := tags[len(tags)-1]

	// Get all commits
	cIter, err := repo.Log(&git.LogOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("could not retrieve commits: %w", err)
	}

	var commits []*object.Commit
	found := false

	// Get all commits after the latest tag
	err = cIter.ForEach(func(c *object.Commit) error {
		if c.Hash.String() == latestTag.Hash().String() {
			found = true
			return storer.ErrStop
		}

		commits = append(commits, c)
		return nil
	})

	if err != nil && err != storer.ErrStop {
		return nil, nil, fmt.Errorf("error iterating through commits: %w", err)
	}

	if !found {
		return nil, nil, fmt.Errorf("latest tag not found in commit history")
	}

	return latestTag, commits, nil
}
