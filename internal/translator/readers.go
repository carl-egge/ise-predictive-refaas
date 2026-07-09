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

	files, err := JsonCodeBlockReader(response)
	if err != nil {
		return nil, err
	}
	log.Debugf("found %d files", len(files))
	dp := domain.DeploymentPackage{}

	key, err := selectMainFile(files, original)
	if err != nil {
		return nil, err
	}
	dp.RootFile = files[key]
	delete(files, key)
	if original != nil {
		dp.TestFiles = original.TestFiles
		dp.Suffix = original.Suffix
		dp.Env = original.Env
	}
	dp.BuildFiles = files
	return &dp, nil
}

// selectMainFile deterministically picks the response key holding the main
// source file: an exact "main.<original suffix>" match wins, otherwise the
// lexically first "main*" key with non-empty content. Go map iteration order
// is randomized, so without sorting the picked file could differ between runs
// - and a nullable/empty schema field (e.g. Gemini's fallback schema emits
// main.go, go.mod and main.py) could silently replace the working source with
// an empty string.
func selectMainFile(files map[string]string, original *domain.DeploymentPackage) (string, error) {
	if original != nil && original.Suffix != "" {
		exact := "main." + original.Suffix
		if content, ok := files[exact]; ok && content != "" {
			return exact, nil
		}
	}
	keys := slices.Collect(maps.Keys(files))
	slices.Sort(keys)
	for _, key := range keys {
		if strings.HasPrefix(key, "main") && files[key] != "" {
			return key, nil
		}
	}
	return "", fmt.Errorf("could not find a non-empty main file in response (keys: %v)", keys)
}

// JsonCodeBlockReader unmarshals a JSON object mapping filenames to file
// contents produced by an LLM. Structured-output enforcement is a per-model
// capability and can silently fall back to unconstrained text (verified on
// the ChatAI backend), so before failing this deterministically retries on
// progressively more aggressive extractions: the raw response, the contents
// of a markdown code fence, and the first balanced top-level {...} region.
// Non-string values inside an otherwise valid object are skipped instead of
// failing the whole response.
func JsonCodeBlockReader(response string) (map[string]string, error) {
	for _, candidate := range jsonCandidates(response) {
		if content, ok := parseStringMap(candidate); ok {
			return content, nil
		}
	}
	return nil, fmt.Errorf("LLM response is not a JSON object (no parseable {...} found): %.200s", response)
}

// jsonCandidates yields the extraction attempts for JsonCodeBlockReader in
// order of increasing leniency.
func jsonCandidates(response string) []string {
	candidates := []string{strings.TrimSpace(response)}
	if fenced := stripCodeFences(response); fenced != "" {
		candidates = append(candidates, fenced)
	}
	if balanced := firstJSONObject(response); balanced != "" {
		candidates = append(candidates, balanced)
	}
	return candidates
}

// stripCodeFences returns the contents of the first markdown code fence
// (```json ... ``` or plain ``` ... ```), or "" when the response has none.
func stripCodeFences(s string) string {
	start := strings.Index(s, "```")
	if start == -1 {
		return ""
	}
	rest := s[start+3:]
	if nl := strings.IndexByte(rest, '\n'); nl != -1 {
		// drop the info string ("json", "go", ...) on the opening fence line
		rest = rest[nl+1:]
	}
	if end := strings.Index(rest, "```"); end != -1 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// firstJSONObject returns the first balanced top-level {...} region of s,
// tracking string literals and escapes so braces inside embedded code (a
// very common payload here) don't break the matching. Returns "" when no
// complete object exists (e.g. a truncated response).
func firstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// parseStringMap attempts to decode candidate as a JSON object, keeping the
// string-valued fields and skipping others (a model may attach arrays or
// nested objects the schema didn't ask for; they must not sink the usable
// part of the response).
func parseStringMap(candidate string) (map[string]string, bool) {
	if candidate == "" || candidate[0] != '{' {
		return nil, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &raw); err != nil {
		return nil, false
	}
	content := make(map[string]string, len(raw))
	for key, value := range raw {
		var str string
		if err := json.Unmarshal(value, &str); err != nil {
			log.Debugf("skipping non-string value for response key %q", key)
			continue
		}
		content[key] = str
	}
	return content, true
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

	files, err := JsonCodeBlockReader(response)
	if err != nil {
		return nil, err
	}
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
	// go.mod/go.sum are always regenerated deterministically by the build
	// commands below - LLM-authored module files are a recurring failure
	// class (invalid versions, unknown directives). Keep only additional
	// non-empty .go sources; anything else in the response (module files,
	// chatter keys like "explanation") is dropped.
	dp.BuildFiles = make(map[string]string)
	for name, content := range files {
		if strings.HasSuffix(name, ".go") && content != "" {
			dp.BuildFiles[name] = content
			continue
		}
		log.Debugf("dropping unexpected response key %q (not a Go source file)", name)
	}
	dp.BuildCmd = []string{"go mod init example.com", "go mod tidy", "go build -o fn ."}
	dp.Suffix = "go"
	dp.TestFiles = original.TestFiles
	// carry the uploaded package's env vars forward - dropping them here
	// silently detached test env config (e.g. AWS endpoints) from goTester.
	dp.Env = original.Env

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
	output.WriteString("package ")
	output.WriteString(node.Name.Name)
	output.WriteString("\n\n")
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
