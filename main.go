package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/theckman/yacspin"
)

// rootCmd is the base command for the basecoat cli
var rootCmd = &cobra.Command{
	Use:   "release <repository> <version>",
	Short: "Helper for simple github releases",
	RunE:  runRootCmd,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Including these in the pre run hook instead of in the enclosing command definition
		// allows cobra to still print errors and usage for its own cli verifications, but
		// ignore our errors.
		cmd.SilenceUsage = true  // Don't print the usage if we get an upstream error
		cmd.SilenceErrors = true // Let us handle error printing ourselves
		return nil
	},
	Args: cobra.ExactArgs(2),
	Example: `$ release clintjedwards/release 1.0.0
$ release clintjedwards/release 1.0.0 -b /tmp/somebinary`,
}

func initSpinner(suffix string) (*yacspin.Spinner, error) {
	cfg := yacspin.Config{
		Frequency:         100 * time.Millisecond,
		CharSet:           yacspin.CharSets[14],
		Suffix:            " " + suffix,
		SuffixAutoColon:   true,
		StopCharacter:     "✓",
		StopColors:        []string{"fgGreen"},
		StopFailCharacter: "x",
		StopFailColors:    []string{"fgRed"},
	}

	spinner, err := yacspin.New(cfg)
	if err != nil {
		return nil, err
	}

	return spinner, nil
}

// First we need to open a file where user can set the semver, changelog contents,
// then we can insert that changelog contents into the source files before we call make to build
func runRootCmd(cmd *cobra.Command, args []string) error {

	repository := args[0]
	version := args[1]
	binaries, _ := cmd.Flags().GetStringArray("binary")

	spinner, err := initSpinner(fmt.Sprintf("Releasing v%s of %s", version, repository))
	if err != nil {
		return fmt.Errorf("could not init spinner: %v", err)
	}
	spinner.Start()

	newRelease, err := newRelease(version, repository)
	if err != nil {
		spinner.StopFailMessage(fmt.Sprintf("%v", err))
		spinner.StopFail()
		return err
	}

	cl, err := handleChangelog(newRelease.ProjectName, newRelease.Version, newRelease.Date, spinner)
	if err != nil {
		spinner.StopFailMessage(fmt.Sprintf("%v", err))
		spinner.StopFail()
		return err
	}

	newRelease.Changelog = cl

	tokenFile, _ := cmd.Flags().GetString("tokenFile")
	err = newRelease.createGithubRelease(tokenFile, binaries...)
	if err != nil {
		spinner.StopFailMessage(fmt.Sprintf("%v", err))
		spinner.StopFail()
		return err
	}

	spinner.Suffix(" Finished release!")
	spinner.Stop()
	return nil
}

func main() {
	rootCmd.Flags().StringP("tokenFile", "t", "", "github api key file (default is $HOME/.github_token)")
	rootCmd.Flags().StringArrayP("binary", "b", []string{}, "binaries to upload")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
