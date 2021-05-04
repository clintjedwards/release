package main

import (
	"fmt"
	"os"

	"github.com/clintjedwards/polyfmt"
	"github.com/spf13/cobra"
)

// rootCmd is the base command for the basecoat cli
var rootCmd = &cobra.Command{
	Use:   "release <repository> <version>",
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
	Args: cobra.ExactArgs(2),
	Example: `$ release clintjedwards/release
$ release clintjedwards/release -b /tmp/somebinary`,
}

// First we need to open a file where user can set the semver, changelog contents,
// then we can insert that changelog contents into the source files before we call make to build
func runRootCmd(cmd *cobra.Command, args []string) error {
	format, err := cmd.Flags().GetString("format")
	if err != nil {
		return err
	}

	pfmt, err := polyfmt.NewFormatter(polyfmt.Mode(format))
	if err != nil {
		return err
	}
	defer pfmt.Finish()

	repository := args[0]
	version := args[1]
	binaries, _ := cmd.Flags().GetStringArray("binary")

	pfmt.Println(fmt.Sprintf("Releasing v%s of %s", version, repository))

	newRelease, err := newRelease(version, repository)
	if err != nil {
		pfmt.PrintErr(fmt.Sprintf("%v", err))
		return err
	}

	cl, err := handleChangelog(newRelease.ProjectName, newRelease.Version, newRelease.Date, pfmt)
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

func main() {
	rootCmd.Flags().StringP("tokenFile", "t", "", "github api key file (default is $HOME/.github_token)")
	rootCmd.Flags().StringArrayP("binary", "b", []string{}, "binaries to upload")
	rootCmd.PersistentFlags().StringP("format", "f", "pretty", "output format; accepted values are 'pretty', 'json', 'silent'")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
