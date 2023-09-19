package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"

	"github.com/clintjedwards/polyfmt/v2"
)

const (
	editorEnvVar  string = "EDITOR"
	visualEnvVar  string = "VISUAL"
	defaultEditor string = "vi"
	filePathFmt   string = "/tmp/%s_%s_%s.%s" // ex. /tmp/changelog_test_1.0.2
)

// pretext is the placeholder text for the input file
const pretext = `// New release for {{.OrgAndRepo}} v{{.Version}}
//
// All lines starting with '//' will be excluded from final changelog
//
// Commits since latest tag:
{{- range .LastCommits}}
// - {{ . }}
{{- end}}
//
// Edit changelog below this comment. An example format has been given:

## v{{.Version}} ({{.Date}})

FEATURES:

* **Feature Name**: Description about new feature this release [<short_commit_hash>]

IMPROVEMENTS:

* **Improvement Name**: Description about new improvement this release [<short_commit_hash>]

BUG FIXES:

* topic: Description of the bug. Example below [<short_commit_hash>]
* api: Fix Go API using lease revocation via URL instead of body [<short_commit_hash>]
`

// getEditorPath attempts to find a suitible editor
// returns an editor binary and argument string
// ex. /usr/bin/vscode --wait
func getEditorPath() (string, error) {
	var editorPath string

	editorPath = os.Getenv(visualEnvVar)
	if editorPath != "" {
		return editorPath, nil
	}

	editorPath = os.Getenv(editorEnvVar)
	if editorPath != "" {
		return editorPath, nil
	}

	path, err := exec.LookPath(defaultEditor)
	if err != nil {
		return "", err
	}

	return path, nil
}

// openFileInEditor attempts to find an editor and open a specific file
func openFileInEditor(filename string) error {
	editorPath, err := getEditorPath()
	if err != nil {
		return err
	}

	// split the path parsed into parts so we can manipulate and add into Command func
	editorPathParts := strings.Split(editorPath, " ")
	editorPathParts = append(editorPathParts, filename)

	cmd := exec.Command(editorPathParts[0], editorPathParts[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func getContentsFromUser(filePath string) ([]byte, error) {
	err := openFileInEditor(filePath)
	if err != nil {
		return nil, err
	}

	bytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	changelog := removeFileComments(bytes)
	return changelog, nil
}

// handleChangelog opens a pre-populated file for editing and returns the final user contents
func handleChangelog(orgAndRepo, version, date string, commits []string, fmtter polyfmt.Formatter) ([]byte, error) {
	fmtter.Print("Creating changelog")

	prefix := "changelog"
	suffix := "md" // markdown
	filePath := fmt.Sprintf(filePathFmt, prefix, strings.ReplaceAll(orgAndRepo, "/", "_"), version, suffix)

	// attempt to recover a changelog file
	_, err := os.Stat(filePath)
	if err == nil {
		fmtter.Success(fmt.Sprintf("Recovered previous changelog (%s)", filePath))
		return getContentsFromUser(filePath)
	}

	// create and populate a new changelog file
	file, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}

	tmpl := template.Must(template.New("").Parse(pretext))
	err = tmpl.Execute(file, struct {
		OrgAndRepo  string
		Version     string
		Date        string
		LastCommits []string
	}{
		OrgAndRepo:  orgAndRepo,
		Version:     version,
		Date:        date,
		LastCommits: commits,
	})
	if err != nil {
		return nil, err
	}

	err = file.Close()
	if err != nil {
		return nil, err
	}

	fmtter.Print("Waiting for user input")
	return getContentsFromUser(filePath)
}

func removeFileComments(data []byte) []byte {
	var newFile [][]byte
	lines := bytes.Split(data, []byte("\n"))

	for _, line := range lines {
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			newFile = append(newFile, line)
		}
	}

	return bytes.Join(newFile, []byte("\n"))
}
