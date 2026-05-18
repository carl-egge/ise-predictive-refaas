// Package main defines a simple reader that extracts filename->content
// mappings from JSON responses produced by LLM prompts.
package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	log "github.com/sirupsen/logrus"
)

// BasicLLMDeploymentReader finds a `main` file among returned files
// and constructs a `DeploymentPackage` using the original test set.
type BasicLLMDeploymentReader struct {
}

// makeDeploymentFile converts a JSON map into a `DeploymentPackage`,
// ensuring a main file is present.
func (gr BasicLLMDeploymentReader) makeDeploymentFile(response string, original *DeploymentPackage) (*DeploymentPackage, error) {
	if response == "" {
		return nil, fmt.Errorf("response is empty")
	}

	files := JsonCodeBlockReader(response)
	log.Debugf("found %d files", len(files))
	dp := DeploymentPackage{}

	keys := slices.Collect(maps.Keys(files))
	index := slices.IndexFunc(keys, func(x string) bool {
		return strings.HasPrefix(x, "main")
	})

	if index == -1 {
		return nil, fmt.Errorf("could not find main")
	}
	key := keys[index]
	if root_file, ok := files[key]; ok {
		dp.RootFile = root_file
		delete(files, key)
	}
	dp.BuildFiles = files
	dp.TestFiles = original.TestFiles
	dp.Suffix = original.Suffix
	return &dp, nil
}

// JsonCodeBlockReader unmarshals a JSON object mapping filenames to
// file contents produced by an LLM.
func JsonCodeBlockReader(response string) map[string]string {
	var content map[string]string
	err := json.Unmarshal([]byte(response), &content)
	if err != nil {
		log.Error(err)
	}
	return content
}
