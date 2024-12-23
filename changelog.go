package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"

	"github.com/clintjedwards/polyfmt/v2"
	"github.com/mitchellh/go-homedir"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	editorEnvVar         string = "EDITOR"
	visualEnvVar         string = "VISUAL"
	defaultEditor        string = "vi"
	filePathFmt          string = "/tmp/%s_%s_%s.%s" // ex. /tmp/changelog_test_1.0.2
	chatGPTTokenEnv      string = "CHATGPT_TOKEN"
	chatGPTTokenFileName string = ".chatgpt_token"
)

// changelogTemplate is the placeholder text for the input file
const changelogTemplate = `// New release for {{.OrgAndRepo}} v{{.Version}}
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

func getChatGPTToken(tokenFile string) (token string, err error) {
	token = os.Getenv(chatGPTTokenEnv)

	if token != "" {
		return token, nil
	}

	if tokenFile == "" {
		home, err := homedir.Dir()
		if err != nil {
			return "", fmt.Errorf("could not get user home dir: %w", err)
		}

		tokenFile = fmt.Sprintf("%s/%s", home, chatGPTTokenFileName)
	}

	rawToken, err := setChatGPTTokenFromFile(tokenFile)
	if err != nil {
		return "", err
	}

	return string(rawToken), nil
}

func setChatGPTTokenFromFile(filename string) ([]byte, error) {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not find chatGPT token: %s; %w", filename, err)
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("could not load chatGPT token contents empty: %s", filename)
	}

	token := bytes.TrimSpace(contents)
	return token, nil
}

// handleChangelog opens a pre-populated file for editing and returns the final user contents
func handleChangelog(orgAndRepo, version, date string, shortCommits []string, longCommits []string,
	fmtter polyfmt.Formatter, useLLM bool,
) ([]byte, error) {
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

	var changelogBuffer bytes.Buffer

	tmpl := template.Must(template.New("").Parse(changelogTemplate))
	err = tmpl.Execute(&changelogBuffer, struct {
		OrgAndRepo  string
		Version     string
		Date        string
		LastCommits []string
	}{
		OrgAndRepo:  orgAndRepo,
		Version:     version,
		Date:        date,
		LastCommits: shortCommits,
	})
	if err != nil {
		return nil, err
	}

	llmtoken, err := getChatGPTToken("")
	if err != nil {
		return nil, err
	}

	output := changelogBuffer.String()

	if useLLM {
		content, err := generateChangelogWithAI(llmtoken, changelogBuffer.String(), longCommits)
		if err != nil {
			return nil, err
		}

		output = content
	}

	_, err = file.WriteString(output)
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

func generateChangelogWithAI(token, template string, commitMessages []string) (string, error) {
	client := openai.NewClient(option.WithAPIKey(token))

	prompt := "I want you to help me write a changelog. Below I will define the template I want you to follow" +
		" and I'll pass you the commit messages you should use to change and fill in the template and give me a useable " +
		" changelog.\n\n" +
		"```template\n" +
		template +
		"```\n\n" +
		"```commit_messages"

	for _, message := range commitMessages {
		prompt += message
	}

	prompt += "```\n\n"
	prompt += "Some things I'd like you to pay attention to:\n" +
		"* If there is a PR number for the commit, please put it at the end with a link to it.\n" +
		"* Don't change the version numbers, repo name, or comments." +
		"* Only send back the changelog, no extra commentary"

	completion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(prompt),
		}),
		Model: openai.F(openai.ChatModelGPT4o),
	})
	if err != nil {
		return "", err
	}

	// ChatGPT returns everything with markdown formatting so we remove it.
	lines := strings.Split(completion.Choices[0].Message.Content, "\n")
	var cleanedLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "```" {
			cleanedLines = append(cleanedLines, line)
		}
	}

	result := strings.Join(cleanedLines, "\n")

	return result, nil
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
