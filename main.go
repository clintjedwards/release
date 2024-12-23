package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/semver"
	"github.com/clintjedwards/polyfmt/v2"
	"github.com/fatih/color"
	"github.com/go-git/go-git/plumbing"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/spf13/cobra"
)

var appVersion = "0.0.dev_000000"

// rootCmd represents the base of the CLI command chain. It configures the CLI but also
// provides the interface for the main command which is simply 'release'.
var rootCmd = &cobra.Command{
	Use:   "release",
	Short: "Helper for simple github releases",
	Long: `Helper for simple github releases.

Tool will confirm before pushing any changes.`,
	Version: " ", // We leave this added but empty so that the rootcmd will supply the -v flag
	RunE:    release,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Including these in the pre run hook instead of in the enclosing command definition
		// allows cobra to still print errors and usage for its own cli verifications, but
		// ignore our errors.
		cmd.SilenceUsage = true  // Don't print the usage if we get an upstream error
		cmd.SilenceErrors = true // Let us handle error printing ourselves
		return nil
	},
	Example: `$ release
$ release -s 0.0.4
$ release -s 0.0.2 -a '/tmp/some_binary'
$ release -a '/tmp/*.glob_of_binaries'`,
}

func release(cmd *cobra.Command, _ []string) error {
	// Initiate flags
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return err
	}

	// We panic here since the only way these flags can fail is if the code is incorrect.
	semverVersion, err := cmd.Flags().GetString("semver")
	if err != nil {
		panic(err)
	}
	assets, err := cmd.Flags().GetStringArray("asset")
	if err != nil {
		panic(err)
	}
	githubTokenFile, err := cmd.Flags().GetString("github_token_file")
	if err != nil {
		panic(err)
	}
	useChatGPT, err := cmd.Flags().GetBool("chatgpt")
	if err != nil {
		panic(err)
	}

	// We don't yet have the appropriate functionality for the llm token file yet.
	_, err = cmd.Flags().GetString("chatgpt_token_file")
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
	if !cmd.Flag("semver").Changed || semverVersion == "" {
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

				semverVersion = response

				break
			}

			// If the user has entered nothing, but we have a default, just
			// use that default.
			if response == "" && possibleNextVersion != "" {
				semverVersion = possibleNextVersion
				break
			}

			// If the user has entered nothing and we don't have a default
			// then we simply re-prompt the user.
			if response == "" && possibleNextVersion == "" {
				continue
			}
		}
	}

	// Evaluate the assets attached
	assetResolutions := resolveFilePaths(assets)
	assetPaths := []string{}

	for assetPath, err := range assetResolutions {
		if err != nil {
			pfmt.Err(fmt.Sprintf("Could not find or resolve asset at path %q", assetPath))
			continue
		}

		assetPaths = append(assetPaths, assetPath)
	}

	newRelease, err := newRelease(semverVersion, orgAndRepo)
	if err != nil {
		pfmt.Err(fmt.Sprintf("%v", err))
		return err
	}

	pfmt.Println(fmt.Sprintf("\nReleasing %s of %s", color.BlueString("v"+semverVersion), color.BlueString(orgAndRepo)))

	shortCommitStrs := []string{}
	for _, commit := range commits {
		message := fmt.Sprintf("%s: %s", getAbbreviatedHash(plumbing.Hash(commit.Hash)), getShortMessage(commit))
		shortCommitStrs = append(shortCommitStrs, message)
	}

	longCommitStrs := []string{}
	for _, commit := range commits {
		message := fmt.Sprintf("%s: %s", commit.Hash, commit.Message)
		longCommitStrs = append(longCommitStrs, message)
	}

	cl, err := handleChangelog(newRelease.OrgAndRepo, newRelease.Version, newRelease.Date, shortCommitStrs, longCommitStrs, pfmt, useChatGPT)
	if err != nil {
		pfmt.Err(fmt.Sprintf("%v", err))
		return err
	}

	newRelease.Changelog = cl

	funcMap := template.FuncMap{
		"blue":    color.BlueString,
		"magenta": color.MagentaString,
	}

	const releaseDetails = `
Details:
{{.Divider | magenta}} Organization: {{.Organization | blue}}
{{.Divider | magenta}} Repository: {{.Repository | blue}}
{{.Divider | magenta}} Semver Version: {{.Semver | blue}}
{{.Divider | magenta}} Release Date: {{.Date | blue}}
{{- if gt (len .Assets) 0}}
{{.Divider | magenta}} Assets:
{{- range .Assets}}
{{$.Divider | magenta}}  • {{ . | blue}}
{{- end}}
{{- end}}
{{.Divider | magenta}} Changelog:
{{.ChangelogDivider | magenta}}
{{.Changelog}}`

	var tpl bytes.Buffer
	tmpl := template.Must(template.New("").Funcs(funcMap).Parse(releaseDetails))
	err = tmpl.Execute(&tpl, struct {
		Divider          string
		Organization     string
		Repository       string
		Semver           string
		Date             string
		Assets           []string
		ChangelogDivider string
		Changelog        string
	}{
		Divider:          "│",
		Organization:     newRelease.Organization,
		Repository:       newRelease.Repository,
		Semver:           "v" + newRelease.Version,
		Date:             newRelease.Date,
		Assets:           assetPaths,
		ChangelogDivider: "└─────┐",
		Changelog:        string(newRelease.Changelog),
	})
	if err != nil {
		return err
	}

	pfmt.Println(tpl.String())
	pfmt.Println(color.MagentaString("──────────"))
	answer := pfmt.Question("Proceed with release? (y/N): ")

	if !strings.EqualFold(answer, "y") {
		pfmt.Warning("Release aborted by user")
		return nil
	}

	err = newRelease.createGithubRelease(pfmt, githubTokenFile, assetPaths...)
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

// Takes in user provided file paths and resolves them with Glob support.
// Also determines if that file exists.
// Returns the full list of concrete file paths along with if they could be resolved properly.
func resolveFilePaths(assetPaths []string) map[string]error {
	concreteAssetPaths := map[string]error{}

	for _, assetPath := range assetPaths {
		filePaths, err := filepath.Glob(assetPath)
		if err != nil {
			concreteAssetPaths[assetPath] = fmt.Errorf("could not resolve assetPath glob: %v", err)
			continue
		}

		for _, filePath := range filePaths {
			if !fileExists(filePath) {
				concreteAssetPaths[filePath] = fmt.Errorf("could not find asset %q", filePath)
				continue
			}

			concreteAssetPaths[filePath] = nil
		}
	}

	return concreteAssetPaths
}

func fileExists(filename string) bool {
	// The os.Stat function returns file info. If the file does not exist, an error is returned.
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir() // Ensure it's not a directory, just a file.
}

func getOrgAndRepo(repo *git.Repository) (string, error) {
	remoteConfig, err := repo.Remote("origin")
	if err != nil {
		return "", fmt.Errorf("could not get origin remote: %w", err)
	}

	// Extract the URL from the remote configuration
	remoteURL := remoteConfig.Config().URLs[0]

	// Handle both SSH and HTTPS URLs
	if strings.HasPrefix(remoteURL, "git@") {
		// SSH format: git@github.com:owner/repo.git
		parts := strings.Split(strings.TrimSuffix(remoteURL, ".git"), ":")
		if len(parts) != 2 {
			return "", fmt.Errorf("unexpected SSH URL format")
		}
		return parts[1], nil
	} else {
		// HTTPS format: https://github.com/owner/repo.git
		parsedURL, err := url.Parse(remoteURL)
		if err != nil {
			return "", fmt.Errorf("could not parse remote URL: %w", err)
		}
		parts := strings.Split(strings.TrimSuffix(parsedURL.Path, ".git"), "/")
		if len(parts) < 2 {
			return "", fmt.Errorf("unexpected HTTPS URL format")
		}
		return parts[len(parts)-2] + "/" + parts[len(parts)-1], nil
	}
}

func humanizeVersion(version string) string {
	semver, hash, err := strings.Cut(version, "_")
	if !err {
		return ""
	}
	return fmt.Sprintf("release %s [%s]\n", semver, hash)
}

func main() {
	rootCmd.SetVersionTemplate(humanizeVersion(appVersion))
	rootCmd.Flags().StringP("semver", "s", "", "The semver version string of the new release; If this is not included release will prompt for it.")
	rootCmd.Flags().StringP("github_token_file", "g", "", "Github api key file (default is $HOME/.github_token)")
	rootCmd.Flags().StringP("chatgpt_token_file", "c", "", "ChatGPT api key file (default is $HOME/.chatgpt_token)")
	rootCmd.Flags().BoolP("chatgpt", "l", false, "Use ChatGPT to help you write changelogs")
	rootCmd.Flags().StringArrayP("asset", "a", []string{}, "Assets to upload; This is usually the binary of "+
		"the software or anything else that needs to be attached to the release."+
		" This flag also supports globbing; make sure to wrap the path in quotes to avoid shell auto-globbing.")
	rootCmd.PersistentFlags().StringP("format", "f", "plain", "Output format; accepted values are 'pretty', 'plain', 'json', 'silent'")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
