package main

import (
	"fmt"
	"strings"
	"time"

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

func getAllCommitsSinceRelease() ([]*object.Commit, error) {
	repo, err := git.PlainOpen("./")
	if err != nil {
		return nil, err
	}

	_, commit, err := getLatestTagFromRepository(repo)
	if err != nil {
		return nil, err
	}

	// Add a nanosecond so we don't include the commit within the returned results
	since := commit.Author.When.Add(1 * time.Nanosecond)

	commits, err := repo.Log(&git.LogOptions{
		Since: &since,
	})
	if err != nil {
		return nil, err
	}

	commitList := []*object.Commit{}
	err = commits.ForEach(func(c *object.Commit) error {
		commitList = append(commitList, c)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return commitList, nil
}

func getLatestTagFromRepository(repository *git.Repository) (*plumbing.Reference, *object.Commit, error) {
	tagRefs, err := repository.Tags()
	if err != nil {
		return nil, nil, err
	}

	var latestTagCommit *object.Commit
	var latestTagName *plumbing.Reference
	err = tagRefs.ForEach(func(tagRef *plumbing.Reference) error {
		revision := plumbing.Revision(tagRef.Name().String())
		tagCommitHash, err := repository.ResolveRevision(revision)
		if err != nil {
			return err
		}

		commit, err := repository.CommitObject(*tagCommitHash)
		if err != nil {
			return err
		}

		if latestTagCommit == nil {
			latestTagCommit = commit
			latestTagName = tagRef
		}

		if commit.Committer.When.After(latestTagCommit.Committer.When) {
			latestTagCommit = commit
			latestTagName = tagRef
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return latestTagName, latestTagCommit, nil
}
