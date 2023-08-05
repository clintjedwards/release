package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/clintjedwards/polyfmt"
	"github.com/go-git/go-git/plumbing"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/cobra"
)

// rootCmd is the base command for the basecoat cli
var rootCmd = &cobra.Command{
	Use:   "release <version>",
	Short: "Helper for simple github releases",
	RunE:  runRootCmd,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Including these in the pre run hook instead of in the enclosing command definition
		// allows cobra to still print errors and usage for its own cli verifications, but
		// ignore our errors.
		cmd.SilenceUsage = true  // Don't print the usage if we get an upstream error
		cmd.SilenceErrors = true // Let us handle error printing ourselves
		return nil
	},
	Args: cobra.ExactArgs(1),
	Example: `$ release 0.0.5
$ release 0.0.2 -b /tmp/some_binary`,
}

// First we need to open a file where user can set the semver, changelog contents,
// then we can insert that changelog contents into the source files before we call make to build
func runRootCmd(cmd *cobra.Command, args []string) error {
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return err
	}

	pfmt, err := polyfmt.NewFormatter(polyfmt.Mode(format), polyfmt.DefaultOptions())
	if err != nil {
		return err
	}
	defer pfmt.Finish()

	repoRaw, err := git.PlainOpen(".")
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	repository, err := getRepoName(repoRaw)
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	version := args[0]
	binaries, _ := cmd.Flags().GetStringArray("binary")

	pfmt.Println(fmt.Sprintf("Releasing v%s of %s", version, repository))

	newRelease, err := newRelease(version, repository)
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	_, commitsRaw, err := getCommitsAfterLatestTag(repoRaw)
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	commits := []string{}
	for _, commit := range commitsRaw {
		message := fmt.Sprintf("%s: %s", getAbbreviatedHash(plumbing.Hash(commit.Hash)), getShortMessage(commit))
		commits = append(commits, message)
	}

	cl, err := handleChangelog(newRelease.ProjectName, newRelease.Version, newRelease.Date, commits, pfmt)
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	newRelease.Changelog = cl

	tokenFile, _ := cmd.Flags().GetString("tokenFile")
	err = newRelease.createGithubRelease(tokenFile, binaries...)
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	pfmt.PrintSuccess("Finished release!")
	return nil
}

func getShortMessage(commit *object.Commit) string {
	fullMessage := commit.Message
	if i := strings.Index(fullMessage, "\n"); i != -1 {
		return fullMessage[:i]
	}
	return fullMessage
}

func getAbbreviatedHash(hash plumbing.Hash) string {
	fullHash := hash.String()
	if len(fullHash) > 7 {
		return fullHash[:7]
	}
	return fullHash
}

// getRepoName retrieves the "project/repo" name from the local .git configuration.
func getRepoName(repo *git.Repository) (string, error) {
	remoteConfig, err := repo.Remote("origin")
	if err != nil {
		return "", fmt.Errorf("could not get origin remote: %w", err)
	}

	// Extract the URL from the remote configuration
	url := remoteConfig.Config().URLs[0]

	// Parse the URL to get the "project/repo" format
	parts := strings.Split(strings.TrimSuffix(url, ".git"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected repository format")
	}

	return parts[len(parts)-2] + "/" + parts[len(parts)-1], nil
}

func main() {
	rootCmd.Flags().StringP("token_file", "t", "", "github api key file (default is $HOME/.github_token)")
	rootCmd.Flags().StringArrayP("binary", "b", []string{}, "binaries to upload")
	rootCmd.PersistentFlags().StringP("format", "f", "pretty", "output format; accepted values are 'pretty', 'json', 'silent'")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
