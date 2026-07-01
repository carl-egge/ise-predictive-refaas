package translator

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/carl-egge/ise-predictive-refaas/internal/domain"
	log "github.com/sirupsen/logrus"
)

// BasicLLMDeploymentReader finds a main file among returned files and constructs
// a DeploymentPackage using the original test set.
type BasicLLMDeploymentReader struct{}

// MakeDeploymentFile converts a JSON map into a DeploymentPackage, ensuring a
// main file is present.
func (gr BasicLLMDeploymentReader) MakeDeploymentFile(response string, original *domain.DeploymentPackage) (*domain.DeploymentPackage, error) {
	if response == "" {
		return nil, fmt.Errorf("response is empty")
	}

	files := JsonCodeBlockReader(response)
	log.Debugf("found %d files", len(files))
	dp := domain.DeploymentPackage{}

	keys := slices.Collect(maps.Keys(files))
	index := slices.IndexFunc(keys, func(x string) bool {
		return strings.HasPrefix(x, "main")
	})

	if index == -1 {
		return nil, fmt.Errorf("could not find main")
	}
	key := keys[index]
	if rootFile, ok := files[key]; ok {
		dp.RootFile = rootFile
		delete(files, key)
	}
	if original != nil {
		dp.TestFiles = original.TestFiles
		dp.Suffix = original.Suffix
	}
	dp.BuildFiles = files
	return &dp, nil
}

// JsonCodeBlockReader unmarshals a JSON object mapping filenames to file
// contents produced by an LLM.
func JsonCodeBlockReader(response string) map[string]string {
	var content map[string]string
	err := json.Unmarshal([]byte(response), &content)
	if err != nil {
		log.Error(err)
	}
	return content
}

// GoJsonOllamaReader parses JSON mapping of filename->content returned by an
// LLM and prepares a DeploymentPackage ready for building.
type GoJsonOllamaReader struct{}

// MakeDeploymentFile converts a JSON response into a DeploymentPackage.
func (gr GoJsonOllamaReader) MakeDeploymentFile(response string, original *domain.DeploymentPackage) (*domain.DeploymentPackage, error) {
	if response == "" {
		return nil, fmt.Errorf("response is empty")
	}
	if original == nil {
		return nil, fmt.Errorf("original is empty")
	}

	files := JsonCodeBlockReader(response)
	log.Debugf("found %d files", len(files))
	dp := domain.DeploymentPackage{}
	if rootFile, ok := files["main.go"]; ok {
		rootFile, err := gr.prepareGoRootFile(rootFile)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare root file: %w", err)
		}
		dp.RootFile = rootFile
		delete(files, "main.go")
	} else {
		return nil, fmt.Errorf("main.go not found in response")
	}
	dp.BuildFiles = files
	dp.BuildCmd = []string{"go mod tidy", "go build -o fn ."}
	if _, ok := files["go.mod"]; !ok {
		dp.BuildCmd = append([]string{"go mod init example.com"}, dp.BuildCmd...)
	}
	dp.Suffix = "go"
	dp.TestFiles = original.TestFiles

	return &dp, nil
}

// prepareGoRootFile removes a main function when necessary so the converted
// code can be used as a library entrypoint.
func (gr GoJsonOllamaReader) prepareGoRootFile(file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file is empty")
	}

	if gr.containsGoMainMethod(file) {
		// log.Debugf("file %s contains main method", file)
		log.Debugf("the existing main() method will be removed from the file")
		return gr.removeGoMainMethod(file), nil
	}

	return file, nil
}

func getContentByNode(content string, decl ast.Decl) string {
	pos := int(decl.Pos())
	end := int(decl.End())
	if end > len(content) {
		end = len(content)
	}

	return content[pos-1 : end]
}

// removeGoMainMethod strips a main function and any associated lambda import
// when it appears to be an accidental inclusion.
func (gr GoJsonOllamaReader) removeGoMainMethod(content string) string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "main.go", content, parser.AllErrors)
	if err != nil {
		log.Debugf("failed to parse main.go content: %v", err)
		return content
	}

	removeLambdaImport := false
	var output strings.Builder
	// node.Decls excludes the package clause (it lives on node.Name), so it
	// must be re-emitted explicitly or the rebuilt file loses "package X".
	output.WriteString("package " + node.Name.Name + "\n\n")
	for _, decl := range node.Decls {
		if funcDecl, ok := decl.(*ast.FuncDecl); ok {
			if funcDecl.Name.Name == "main" {
				mainContainsLambdaAPI := strings.Contains(getContentByNode(content, decl), "lambda")
				lambdaAPIUsages := strings.Count(content[:funcDecl.Pos()-1], "lambda.") + strings.Count(content[funcDecl.End()-1:], "lambda.")
				if mainContainsLambdaAPI && lambdaAPIUsages <= 1 {
					removeLambdaImport = true
				}
				continue
			}
		}
		output.WriteString(getContentByNode(content, decl))
	}
	if removeLambdaImport {
		removedImport := strings.Replace(output.String(), "\"github.com/aws/aws-lambda-go/lambda\"", "", 1)
		return removedImport
	}
	return output.String()
}

// containsGoMainMethod detects whether content declares a main function.
func (gr GoJsonOllamaReader) containsGoMainMethod(content string) bool {
	mainMethodRegex := regexp.MustCompile(`func main\(\)`)
	return mainMethodRegex.MatchString(content)
}

// GoDeepSeekOllamaReader adapts DeepSeek responses into a DeploymentPackage
// using the internal GoJsonOllamaReader.
type GoDeepSeekOllamaReader struct {
	internal GoJsonOllamaReader
}

// MakeDeploymentFile extracts JSON blocks and delegates to the internal reader.
func (gr GoDeepSeekOllamaReader) MakeDeploymentFile(response string, original *domain.DeploymentPackage) (*domain.DeploymentPackage, error) {
	if response == "" {
		return nil, fmt.Errorf("response is empty")
	}
	content := response
	if strings.Contains(content, "</think>") {
		_, content, _ = strings.Cut(content, "</think>")
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")

	if start == -1 || end == -1 {
		return nil, fmt.Errorf("response is missing json - %s", content)
	}
	jsonContent := content[start : end+1]
	jsonContent = strings.Replace(jsonContent, "\n", "", -1)
	return gr.internal.MakeDeploymentFile(jsonContent, original)
}
