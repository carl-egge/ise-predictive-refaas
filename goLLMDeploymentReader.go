// Package main contains readers that convert JSON/code blocks from LLM
// responses into `DeploymentPackage` values suitable for build and
// testing.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
)

// GoJsonOllamaReader parses JSON mapping of filename->content returned
// by an LLM and prepares a `DeploymentPackage` ready for building.
type GoJsonOllamaReader struct {
}

// makeDeploymentFile converts a JSON response into a `DeploymentPackage`.
func (gr GoJsonOllamaReader) makeDeploymentFile(response string, original *DeploymentPackage) (*DeploymentPackage, error) {
	if response == "" {
		return nil, fmt.Errorf("response is empty")
	}
	if original == nil {
		return nil, fmt.Errorf("original is empty")
	}

	files := JsonCodeBlockReader(response)
	log.Debugf("found %d files", len(files))
	dp := DeploymentPackage{}
	if root_file, ok := files["main.go"]; ok {
		root_file, err := gr.prepareGoRootFile(root_file)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare root file: %w", err)
		}
		dp.RootFile = root_file
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

// prepareGoRootFile removes a `main` function when necessary so the
// converted code can be used as a library entrypoint.
func (gr GoJsonOllamaReader) prepareGoRootFile(file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("file is empty")
	}

	if gr.containsGoMainMethod(file) {
		log.Debugf("file %s contains main method", file)
		return gr.removeGoMainMethod(file), nil
	} else {
		return file, nil
	}
}

func getContentByNode(content string, decl ast.Decl) string {
	pos := int(decl.Pos())
	end := int(decl.End())
	if end > len(content) {
		end = len(content)
	}

	return content[pos-1 : end]

}

// removeGoMainMethod strips a `main` function and any associated
// lambda import when it appears to be an accidental inclusion.
func (gr GoJsonOllamaReader) removeGoMainMethod(content string) string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "main.go", content, parser.AllErrors)
	if err != nil {
		log.Debugf("failed to parse main.go content: %v", err)
		return content
	}

	var remove_lambda_import = false
	var output strings.Builder
	for _, decl := range node.Decls {
		if funcDecl, ok := decl.(*ast.FuncDecl); ok {
			if funcDecl.Name.Name == "main" {
				//XXX: fixing a typical mistake but in a somewhat crude way. A better approach would be repromting...
				main_contains_Lambda_api := strings.Contains(getContentByNode(content, decl), "lambda")
				lambda_api_usages := strings.Count(content[:funcDecl.Pos()-1], "lambda.") + strings.Count(content[funcDecl.End()-1:], "lambda.")
				if main_contains_Lambda_api && lambda_api_usages <= 1 {
					//We need to remove the import.
					remove_lambda_import = true
				}
				continue // Skip the main function
			}
		}
		output.WriteString(getContentByNode(content, decl)) // Append remaining code
	}
	if remove_lambda_import {
		removed_import := strings.Replace(output.String(), "\"github.com/aws/aws-lambda-go/lambda\"", "", 1)
		return removed_import
	}
	return output.String()
}

// containsGoMainMethod detects whether content declares a `main()`
// function.
func (gr GoJsonOllamaReader) containsGoMainMethod(content string) bool {
	mainMethodRegex := regexp.MustCompile(`func main\(\)`) // Regex to check for main function
	return mainMethodRegex.MatchString(content)
}
