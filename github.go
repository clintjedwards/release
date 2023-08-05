package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver"
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
	User        string
	Date        string // date in format: month day, year
	Changelog   []byte
	Repository  string // full repository name from config
	ProjectName string // the project name grabbed from the repository
	Version     string // semver without the v; ex: 1.0.0
	VersionFull string // ex: <semver>_<epoch>_<commit>
}

// newRelease creates a prepopulated release struct using the config file and other sources
func newRelease(version, repository string) (*Release, error) {
	_, err := semver.NewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("could not parse semver string: %w", err)
	}

	user, projectName, err := parseGithubURL(repository)
	if err != nil {
		return nil, fmt.Errorf("could not parse github URL: %w", err)
	}

	// insert date into release struct
	year, month, day := time.Now().Date()
	date := fmt.Sprintf(dateFmt, month, day, year)

	return &Release{
		Date:        date,
		ProjectName: projectName,
		Repository:  repository,
		User:        user,
		Version:     version,
	}, nil
}

// createGithubRelease cuts a new release, tags the current commit with semver, and uploads the changelog as a description
func (r *Release) createGithubRelease(tokenFile string, binaryPaths ...string) error {
	token, err := getGithubToken(tokenFile)
	if err != nil {
		return fmt.Errorf("could not get github token: %w", err)
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

	createdRelease, _, err := client.Repositories.CreateRelease(context.Background(), r.User, r.ProjectName, release)
	if err != nil {
		return err
	}

	if len(binaryPaths) == 0 {
		return nil
	}

	for _, bin := range binaryPaths {
		err = r.uploadBinary(bin, createdRelease.GetID(), client)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *Release) uploadBinary(path string, id int64, c *github.Client) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("could not find binary file: %s; %w", path, err)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, _, err = c.Repositories.UploadReleaseAsset(context.Background(), r.User, r.ProjectName, id,
		&github.UploadOptions{Name: filepath.Base(f.Name())}, f)
	if err != nil {
		return fmt.Errorf("could not upload binary file: %s; %w", path, err)
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
