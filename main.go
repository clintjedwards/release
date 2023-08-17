package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/clintjedwards/polyfmt/v2"
	"github.com/fatih/color"
	"github.com/go-git/go-git/plumbing"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/cobra"
)

// rootCmd represents the base of the CLI command chain. It configures the CLI but also
// provides the interface for the main command which is simply 'release'.
var rootCmd = &cobra.Command{
	Use:   "release",
	Short: "Helper for simple github releases",
	Long: `Helper for simple github releases.

Tool will confirm before pushing any changes.`,
	RunE: release,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Including these in the pre run hook instead of in the enclosing command definition
		// allows cobra to still print errors and usage for its own cli verifications, but
		// ignore our errors.
		cmd.SilenceUsage = true  // Don't print the usage if we get an upstream error
		cmd.SilenceErrors = true // Let us handle error printing ourselves
		return nil
	},
	Example: `$ release
$ release -v 0.0.4
$ release -v 0.0.2 -b /tmp/some_binary`,
}

func release(cmd *cobra.Command, _ []string) error {
	// Initiate flags
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return err
	}

	// We panic here since the only way these flags can fail is if the code is incorrect.
	version, err := cmd.Flags().GetString("version")
	if err != nil {
		panic(err)
	}
	binaries, err := cmd.Flags().GetStringArray("binary")
	if err != nil {
		panic(err)
	}
	tokenFile, err := cmd.Flags().GetString("token_file")
	if err != nil {
		panic(err)
	}

	// Init formatter
	pfmt, err := polyfmt.NewFormatter(polyfmt.Mode(format), polyfmt.DefaultOptions())
	if err != nil {
		return err
	}
	defer pfmt.Finish()

	repository, err := git.PlainOpen(".")
	if err != nil {
		pfmt.Err(fmt.Sprintf("Could not open local repository; make sure to run release from inside"+
			"the repository you mean to create a release for; %v", err))
		return err
	}

	orgAndRepo, err := getOrgAndRepo(repository)
	if err != nil {
		pfmt.Err(fmt.Sprintf("Could not parse repository name; %v", err))
		return err
	}

	latestTag, commits, err := getCommitsAfterLatestTag(repository)
	if err != nil {
		pfmt.Err(fmt.Sprintf("Could not find any previous releases; %v", err))
	}

	// If the user hasn't actually set the version flag then we need to determine what it is.
	// We do this by prompting the user for the version, but before doing that taking a best
	// guess on what it might be if we were able to glean a previous version from the proceeding
	// command.
	if !cmd.Flag("version").Changed || version == "" {
		latestVersion := ""
		possibleNextVersion := ""

		if latestTag != nil {
			latestVersion = getSemverFromTag(latestTag)

			// This should never fail, since we run the same command on the latestTag in the previous
			// function.
			latestSemver, _ := semver.NewVersion(latestVersion)
			*latestSemver = latestSemver.IncMinor()
			possibleNextVersion = latestSemver.String()
		}

		if latestVersion != "" {
			pfmt.Println(fmt.Sprintf("The latest version found is %q", latestVersion))
		}

		for {
			question := "What should the next version be? "

			if possibleNextVersion != "" {
				question += fmt.Sprintf("[default %q]: ", possibleNextVersion)
			}

			response := pfmt.Question(question)

			// If the user has entered anything we take that.
			if response != "" {
				_, err := semver.NewVersion(response)
				if err != nil {
					pfmt.Err(fmt.Sprintf("Couldn't parse version %q; %v", response, err))
					continue
				}

				version = response

				break
			}

			// If the user has entered nothing, but we have a default, just
			// use that default.
			if response == "" && possibleNextVersion != "" {
				version = possibleNextVersion
				break
			}

			// If the user has entered nothing and we don't have a default
			// then we simply re-prompt the user.
			if response == "" && possibleNextVersion == "" {
				continue
			}
		}
	}

	newRelease, err := newRelease(version, orgAndRepo)
	if err != nil {
		pfmt.Err(fmt.Sprintf("%v", err))
		return err
	}

	pfmt.Println(fmt.Sprintf("\nReleasing %s of %s", color.BlueString("v"+version), color.BlueString(orgAndRepo)))

	commitStrs := []string{}
	for _, commit := range commits {
		message := fmt.Sprintf("%s: %s", getAbbreviatedHash(plumbing.Hash(commit.Hash)), getShortMessage(commit))
		commitStrs = append(commitStrs, message)
	}

	cl, err := handleChangelog(newRelease.OrgAndRepo, newRelease.Version, newRelease.Date, commitStrs, pfmt)
	if err != nil {
		pfmt.Err(fmt.Sprintf("%v", err))
		return err
	}

	newRelease.Changelog = cl

	releaseDetails := `
Details:
%s Organization: %s
%s Repository: %s
%s Semver Version:%s
%s Release Date: %s
%s Changelog:
%s
%s`
	pfmt.Println(fmt.Sprintf(releaseDetails,
		color.MagentaString("│"), color.BlueString(newRelease.Organization),
		color.MagentaString("│"), color.BlueString(newRelease.Repository),
		color.MagentaString("│"), color.BlueString("v"+newRelease.Version),
		color.MagentaString("│"), color.BlueString(newRelease.Date),
		color.MagentaString("│"), color.MagentaString("└────────┐"), newRelease.Changelog))
	pfmt.Println(color.MagentaString("──────────"))
	answer := pfmt.Question("Proceed with release? (y/N): ")

	if !strings.EqualFold(answer, "y") {
		pfmt.Warning("Release aborted by user")
		return nil
	}

	err = newRelease.createGithubRelease(tokenFile, binaries...)
	if err != nil {
		pfmt.Err(fmt.Sprintf("%v", err))
		return err
	}

	pfmt.Success("Finished release!")
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

// getOrgAndRepo retrieves the "project/repo" name from the local .git configuration.
func getOrgAndRepo(repo *git.Repository) (string, error) {
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
	rootCmd.Flags().StringP("version", "v", "", "The semver version string of the new release; If this is not included release will prompt for it.")
	rootCmd.Flags().StringP("token_file", "t", "", "Github api key file (default is $HOME/.github_token)")
	rootCmd.Flags().StringArrayP("binary", "b", []string{}, "binaries to upload")
	rootCmd.PersistentFlags().StringP("format", "f", "pretty", "output format; accepted values are 'pretty', 'json', 'silent'")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
