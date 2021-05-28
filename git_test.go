package main

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestParseCommitToInfo(t *testing.T) {
	tests := map[string]struct {
		commit   *object.Commit
		expected commitInfo
	}{
		"ci": {
			commit: &object.Commit{
				Message: "ci: test message",
			},
			expected: commitInfo{
				kind:     ci,
				commit:   nil,
				breaking: false,
			},
		},
		"docs": {
			commit: &object.Commit{
				Message: "docs: test message",
			},
			expected: commitInfo{
				kind:     docs,
				commit:   nil,
				breaking: false,
			},
		},
		"feat": {
			commit: &object.Commit{
				Message: "feat: test message",
			},
			expected: commitInfo{
				kind:     feat,
				commit:   nil,
				breaking: false,
			},
		},
		"fix": {
			commit: &object.Commit{
				Message: "fix: test message",
			},
			expected: commitInfo{
				kind:     fix,
				commit:   nil,
				breaking: false,
			},
		},
		"refactor": {
			commit: &object.Commit{
				Message: "refactor: test message",
			},
			expected: commitInfo{
				kind:     refactor,
				commit:   nil,
				breaking: false,
			},
		},
		"revert": {
			commit: &object.Commit{
				Message: "revert: test message",
			},
			expected: commitInfo{
				kind:     revert,
				commit:   nil,
				breaking: false,
			},
		},
		"breaking": {
			commit: &object.Commit{
				Message: "ci!: test message",
			},
			expected: commitInfo{
				kind:     ci,
				commit:   nil,
				breaking: true,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(_ *testing.T) {
			result, err := parseCommitToInfo(tc.commit)
			if err != nil {
				t.Error(err)
			}

			if result.breaking != tc.expected.breaking {
				t.Errorf("parsing failure for message %q; 'breaking' tag mismatch", tc.commit.Message)
			}

			if result.kind != tc.expected.kind {
				t.Errorf("parsing failure for message %q; unexpected kind; want %q; got %q", tc.commit.Message, tc.expected.kind, result.kind)
			}

		})
	}
}

func TestParseConventionalCommits(t *testing.T) {
	commits := []*object.Commit{
		{
			Message: "ci!: breaking ci message",
		},
		{
			Message: "incorrect_kind: test message",
		},
		{
			Message: "incorrect_breaking_kind!: test message",
		},
		{
			Message: "ci no dividing line ",
		},
		{
			Message: "docs: test message",
		},
	}

	parsedCommits, malformedCommits := parseConventionalCommits(commits)
	if len(malformedCommits) != 3 {
		t.Errorf("incorrect number of malformed commits; found %d; expected %d", len(malformedCommits), 4)
		t.Log("Malformed:")
		for _, commit := range malformedCommits {
			t.Log("\t", commit)
		}
	}

	if len(parsedCommits) != 2 {
		t.Errorf("incorrect number of successfully parsed commits; found %d; expected %d", len(parsedCommits), 1)
		t.Log("Parsed:")
		for _, commit := range parsedCommits {
			t.Logf("\t %+v", commit)
		}
	}
}
