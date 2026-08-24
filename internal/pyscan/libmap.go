package pyscan

import "sort"

// This file holds the policy that extract.py deliberately does not: which
// Python libraries map to which Go packages, which have no realistic Go
// equivalent, and which names the feature vector reserves a column for.
// Keeping it here means the table can be edited - as translation experience
// accumulates - without touching the parser or re-deriving any metric.

// LibMapping is one entry of the static Python -> Go API table injected into
// the translate prompt ([C8]). Removing library equivalence from the model's
// job is the "more structure, less reliance on large-model reasoning"
// tradeoff this pipeline needs at 30B scale.
type LibMapping struct {
	Python string // Python top-level module name
	Go     string // the Go package a translation should reach for
	Note   string // the part a model most often gets wrong; may be empty
}

// libMappings covers the third-party surface actually present in the
// evaluation corpus (boto3 in 58 of 95 functions, python-dateutil in 18,
// requests in 4, beautifulsoup4 in 2, urllib3 in 1 - EVALUATION_DATASET.md
// §7) plus the standard-library modules whose Go counterpart is not obvious.
var libMappings = map[string]LibMapping{
	"boto3": {
		Python: "boto3", Go: "github.com/aws/aws-sdk-go-v2/service/<service>",
		Note: "load config with config.LoadDefaultConfig(ctx); every call takes a context and returns (result, error)",
	},
	"botocore": {
		Python: "botocore", Go: "github.com/aws/aws-sdk-go-v2",
		Note: "botocore.exceptions.ClientError becomes a typed API error; check with errors.As",
	},
	"requests": {
		Python: "requests", Go: "net/http",
		Note: "no default timeout in Go either way: build the request, use http.Client, and always close resp.Body",
	},
	"urllib3": {Python: "urllib3", Go: "net/http"},
	"dateutil": {
		Python: "dateutil", Go: "time",
		Note: "dateutil.parser.parse has no stdlib equivalent; enumerate the layouts the function actually needs",
	},
	"pytz":   {Python: "pytz", Go: "time", Note: "time.LoadLocation replaces pytz.timezone"},
	"bs4":    {Python: "bs4", Go: "golang.org/x/net/html", Note: "no CSS-selector API; walk the node tree"},
	"yaml":   {Python: "yaml", Go: "gopkg.in/yaml.v3"},
	"jwt":    {Python: "jwt", Go: "github.com/golang-jwt/jwt/v5"},
	"jinja2": {Python: "jinja2", Go: "text/template"},
	// Standard-library modules whose Go counterpart is worth stating.
	"json":     {Python: "json", Go: "encoding/json", Note: "unmarshal into a struct or map[string]any; Go has no dict"},
	"os":       {Python: "os", Go: "os"},
	"re":       {Python: "re", Go: "regexp", Note: "RE2: no backreferences and no lookaround"},
	"datetime": {Python: "datetime", Go: "time", Note: "format with reference layouts (2006-01-02), not strftime codes"},
	"decimal":  {Python: "decimal", Go: "math/big", Note: "big.Float or a fixed-point int; float64 silently loses precision"},
	"uuid":     {Python: "uuid", Go: "github.com/google/uuid"},
	"base64":   {Python: "base64", Go: "encoding/base64"},
	"hashlib":  {Python: "hashlib", Go: "crypto/sha256 (and siblings)"},
	"hmac":     {Python: "hmac", Go: "crypto/hmac"},
	"logging":  {Python: "logging", Go: "log", Note: "Python's logging writes to stderr; Go's log must too - stdout is the harness's channel"},
	"csv":      {Python: "csv", Go: "encoding/csv"},
	"gzip":     {Python: "gzip", Go: "compress/gzip"},
	"zipfile":  {Python: "zipfile", Go: "archive/zip"},
	"math":     {Python: "math", Go: "math"},
	"random":   {Python: "random", Go: "math/rand"},
	"time":     {Python: "time", Go: "time"},
	"urllib":   {Python: "urllib", Go: "net/url + net/http"},
	"email":    {Python: "email", Go: "net/mail + mime/multipart"},
	"smtplib":  {Python: "smtplib", Go: "net/smtp"},
	"io":       {Python: "io", Go: "io + bytes"},
	"collections": {
		Python: "collections", Go: "maps and slices",
		Note: "defaultdict/Counter have no equivalent; check-and-initialise explicitly",
	},
	"itertools":   {Python: "itertools", Go: "explicit loops"},
	"typing":      {Python: "typing", Go: "static types"},
	"dataclasses": {Python: "dataclasses", Go: "structs with json tags"},
}

// infeasibleLibs have no realistic pure-Go equivalent: they are numerical or
// ML stacks built on C extensions, and a translation that claims to
// reproduce them is almost certainly wrong. A function importing one is a
// near-deterministic skip, which is why this doubles as baseline B4's rule
// list ([I5]) and as a feature column in its own right.
var infeasibleLibs = map[string]string{
	"numpy":      "array semantics and broadcasting have no Go equivalent",
	"pandas":     "no DataFrame equivalent in Go",
	"scipy":      "scientific routines are C/Fortran-backed",
	"sklearn":    "model training/inference stack has no Go port",
	"torch":      "deep-learning runtime has no Go port",
	"tensorflow": "deep-learning runtime has no Go port",
	"cv2":        "OpenCV bindings are C++-backed",
	"PIL":        "Pillow imaging is C-backed; Go's image package is far narrower",
	"matplotlib": "plotting stack has no Go equivalent",
	"lxml":       "libxml2-backed; Go's encoding/xml has different semantics",
	"pydantic":   "runtime validation model has no direct Go equivalent",
	"sqlalchemy": "ORM has no direct Go equivalent",
}

// vocabulary fixes the one-hot library columns of the feature vector. It is
// deliberately *closed*: at N=95 an open vocabulary would add a near-empty
// column per rare import and give the model more features than samples.
// Anything outside it lands in the `lib_other` count instead.
//
// Changing this list changes the feature vector's width, so it is versioned
// with FeatureSchemaVersion in features.go.
var vocabulary = []string{
	"boto3", "botocore", "requests", "urllib3", "dateutil",
	"bs4", "yaml", "pytz", "jwt",
}

// Mapping returns the Go counterpart of a Python module, if one is known.
func Mapping(module string) (LibMapping, bool) {
	m, ok := libMappings[module]
	return m, ok
}

// Mappings returns the known mappings for the given modules, ordered by
// module name so prompt text and test expectations are deterministic.
func Mappings(modules []string) []LibMapping {
	out := make([]LibMapping, 0, len(modules))
	for _, m := range modules {
		if mapping, ok := libMappings[m]; ok {
			out = append(out, mapping)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Python < out[j].Python })
	return out
}

// Infeasible returns the modules among the given ones that have no realistic
// Go equivalent, each with the reason, ordered by module name.
func Infeasible(modules []string) []LibMapping {
	out := make([]LibMapping, 0)
	for _, m := range modules {
		if reason, ok := infeasibleLibs[m]; ok {
			out = append(out, LibMapping{Python: m, Note: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Python < out[j].Python })
	return out
}
