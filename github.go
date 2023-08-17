package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver"
	"github.com/clintjedwards/polyfmt/v2"
	"github.com/go-git/go-git/plumbing/storer"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/go-github/github"
	"github.com/mitchellh/go-homedir"
	"golang.org/x/oauth2"
)

const (
	tokenEnv      string = "GITHUB_TOKEN"
	tokenFileName string = ".github_token"
	dateFmt       string = "%s %d, %d"
)

// Release contains information pertaining to a specific github release
type Release struct {
	Organization string // The organization without the repository name; ex. clintjedwards
	Date         string // date in format: month day, year
	Changelog    []byte
	OrgAndRepo   string // Organization and repository name; ex. clintjedwards/release
	Repository   string // The name of the repository only, without the prefixed organization. ex. release
	Version      string // semver without the v; ex: 1.0.0
}

// newRelease creates a pre-populated release struct using the config file and other sources
func newRelease(version, repository string) (*Release, error) {
	_, err := semver.NewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("could not parse semver string: %w", err)
	}

	org, repo, err := parseGithubURL(repository)
	if err != nil {
		return nil, fmt.Errorf("could not parse github URL: %w", err)
	}

	// insert date into release struct
	year, month, day := time.Now().Date()
	date := fmt.Sprintf(dateFmt, month, day, year)

	return &Release{
		Date:         date,
		Repository:   repo,
		OrgAndRepo:   repository,
		Organization: org,
		Version:      version,
	}, nil
}

// createGithubRelease cuts a new release, tags the current commit with semver, and uploads the changelog as a description
func (r *Release) createGithubRelease(pfmt polyfmt.Formatter, tokenFile string, assetPaths ...string) error {
	pfmt.Print("Creating release")

	pfmt.Print("Retrieving Github token")
	token, err := getGithubToken(tokenFile)
	if err != nil {
		pfmt.Err(fmt.Sprintf("Could not retrieve Github token from file %q; %v", tokenFile, err))
		return fmt.Errorf("could not get github token from file %q: %w", tokenFile, err)
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(context.Background(), ts)

	client := github.NewClient(tc)

	release := &github.RepositoryRelease{
		TagName: github.String("v" + r.Version),
		Name:    github.String("v" + r.Version),
		Body:    github.String(string(r.Changelog)),
	}

	pfmt.Print("Creating release")
	createdRelease, _, err := client.Repositories.CreateRelease(context.Background(), r.Organization, r.Repository, release)
	if err != nil {
		pfmt.Err(fmt.Sprintf("Could not create release; %v", err))
		return err
	}
	pfmt.Success("Successfully created release!")

	if len(assetPaths) == 0 {
		return nil
	}

	pfmt.Print("Uploading assets")
	for _, assetPath := range assetPaths {
		pfmt.Print(fmt.Sprintf("Uploading asset: %q", assetPath))

		err = r.uploadAsset(assetPath, createdRelease.GetID(), client)
		if err != nil {
			pfmt.Err(fmt.Sprintf("Could not upload asset %q; %v", assetPath, err))
			continue
		}

		pfmt.Success(fmt.Sprintf("Uploaded asset: %q", assetPath))
	}

	return nil
}

func (r *Release) uploadAsset(path string, id int64, c *github.Client) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("could not find asset file: %s; %w", path, err)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, _, err = c.Repositories.UploadReleaseAsset(context.Background(), r.Organization, r.Repository, id,
		&github.UploadOptions{Name: filepath.Base(f.Name())}, f)
	if err != nil {
		return fmt.Errorf("could not upload asset file: %s; %w", path, err)
	}

	return nil
}

// getGithubToken attempts to load a github token and returns an error if none exists
func getGithubToken(tokenFile string) (token string, err error) {
	token = os.Getenv(tokenEnv)

	if token != "" {
		return token, nil
	}

	if tokenFile == "" {
		home, err := homedir.Dir()
		if err != nil {
			return "", fmt.Errorf("could not get user home dir: %w", err)
		}

		tokenFile = fmt.Sprintf("%s/%s", home, tokenFileName)
	}

	rawToken, err := setGithubTokenFromFile(tokenFile)
	if err != nil {
		return "", err
	}

	return string(rawToken), nil
}

func setGithubTokenFromFile(filename string) ([]byte, error) {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not find github token: %s; %w", filename, err)
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("could not load github token contents empty: %s", filename)
	}

	token := bytes.TrimSpace(contents)
	return token, nil
}

// parseGithubURL parses the githubURL and return a username and repo name
func parseGithubURL(githubURL string) (username, projectName string, err error) {
	splitURL := strings.Split(githubURL, "/")

	if len(splitURL) != 2 {
		return "", "", fmt.Errorf("github URL not in correct format: username/repo")
	}

	return splitURL[0], splitURL[1], nil
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

func getSemverFromTag(ref *plumbing.Reference) string {
	index := strings.LastIndex(ref.String(), "/")
	return ref.String()[index+1:]
}
