package domain

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
)

// MetaFileName is the per-function metadata file the dataset pipeline ships
// at the root of every artifact ZIP, alongside main.py and test/.
const MetaFileName = "meta.json"

// FunctionMeta is the dataset's per-function metadata (meta.json). Its fields
// are the axes the evaluation groups results by - complexity bucket, AWS
// usage - so they have to travel with the job's metrics or a finished run
// cannot be broken down at all (see TODO.md [H1]).
//
// The schema is owned by the dataset pipeline, not by this repo, so every
// field is optional and Raw keeps the original bytes verbatim: a field added
// on that side survives into the run log without a change here.
//
// Note that the dataset's own documentation warns that Type over-reports
// network usage (it counts urllib.parse and http.HTTPStatus), so grouping
// should prefer the AWS flag.
type FunctionMeta struct {
	// Name and ID are optional identity hints. The dataset's canonical
	// identity is the artifact's filename stem (f42.zip -> "f42"), which the
	// archive itself cannot carry - see ResolveFunctionID.
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`

	Bucket      string   `json:"bucket,omitempty"`
	CC          int      `json:"cc,omitempty"`
	LLOC        int      `json:"lloc,omitempty"`
	Type        string   `json:"type,omitempty"`
	AWS         bool     `json:"aws,omitempty"`
	Imports     []string `json:"imports,omitempty"`
	Description string   `json:"description,omitempty"`

	Provenance json.RawMessage `json:"provenance,omitempty"`

	// Raw is the verbatim meta.json content, preserved so nothing is lost to
	// a field this struct does not model yet.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// ParseFunctionMeta decodes a meta.json payload.
//
// Content that is not a JSON object is an error: that means a mispackaged
// artifact, which is worth failing fast on rather than translating. A field
// whose type differs from what this struct models is *not* an error - the
// typed fields are simply left empty and Raw still carries everything, since
// the dataset owns the schema and a guessed-wrong field must not cost a whole
// translation run.
func ParseFunctionMeta(raw []byte) (*FunctionMeta, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", MetaFileName, err)
	}

	meta := &FunctionMeta{}
	if err := json.Unmarshal(raw, meta); err != nil {
		meta = &FunctionMeta{}
	}
	meta.Raw = append(json.RawMessage(nil), raw...)
	return meta, nil
}

// FunctionStem reduces an artifact name to the dataset's function id:
// "f42.zip" -> "f42", "dataset/evaluation_set/f42.zip" -> "f42". Returns ""
// when nothing usable remains. Backslashes are normalized first so a filename
// submitted by a Windows client still yields its stem.
func FunctionStem(sourceName string) string {
	normalized := strings.ReplaceAll(sourceName, "\\", "/")
	base := path.Base(normalized)
	if base == "." || base == "/" {
		return ""
	}
	return strings.TrimSuffix(base, path.Ext(base))
}

// ResolveFunctionID determines which dataset element a job corresponds to,
// preferring an explicit id from meta.json, then the artifact's filename stem
// (the dataset's own convention), and falling back to a short form of the job
// UUID so a record is never completely unattributable.
func ResolveFunctionID(meta *FunctionMeta, sourceName string, id uuid.UUID) string {
	if meta != nil {
		if meta.Name != "" {
			return meta.Name
		}
		if meta.ID != "" {
			return meta.ID
		}
	}
	if stem := FunctionStem(sourceName); stem != "" {
		return stem
	}
	if id != uuid.Nil {
		return "job-" + id.String()[:8]
	}
	return ""
}
