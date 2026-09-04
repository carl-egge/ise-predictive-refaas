package pyscan

import (
	"fmt"
	"sort"
	"strings"
)

// This is [C8]'s half of the scan: the same facts, rendered for a prompt
// instead of a model. Injecting explicit API-mapping hints removes the
// hardest reasoning step - library equivalence - from the model's job, which
// is the "more structure, less reliance on large-model reasoning" tradeoff
// this pipeline needs at 30B scale.

// LibHints renders the Python -> Go API mapping table for the modules this
// function actually imports. Empty when nothing is known, so a prompt
// template can test it with {{ if .lib_hints }}.
func (r *Result) LibHints() string {
	if r == nil {
		return ""
	}
	mappings := Mappings(r.Imports)
	if len(mappings) == 0 {
		return ""
	}

	var b strings.Builder
	for _, m := range mappings {
		fmt.Fprintf(&b, "- %s -> %s", m.Python, m.Go)
		if m.Note != "" {
			fmt.Fprintf(&b, " (%s)", m.Note)
		}
		b.WriteByte('\n')
	}

	// boto3's mapping is generic; naming the services the function actually
	// constructs turns it into a concrete import list. Naming them was not
	// enough - run 20260831-190900 shows the model inventing "service/
	// stepfunctions", "service/iotdata" and "service/ecstypes" from the service
	// name alone, and one bad path fails `go mod tidy` for the whole module -
	// so the exact import path is spelled out per service.
	if services := r.awsServiceNames(); len(services) > 0 {
		known, unknown := AWSServices(services)
		for _, svc := range known {
			fmt.Fprintf(&b, "- AWS %s -> import %q", svc.Service, svc.Module)
			if svc.Note != "" {
				fmt.Fprintf(&b, " (%s)", svc.Note)
			}
			b.WriteByte('\n')
		}
		if len(unknown) > 0 {
			fmt.Fprintf(&b, "- AWS %s: exact module path not listed here - it is under %q, "+
				"but the Go name often differs from the boto3 name, so verify rather than assume\n",
				strings.Join(unknown, ", "), awsModulePrefix)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// awsServiceNames is the set of AWS services to render import paths for:
// the names boto3 was called with directly, plus the literals passed to a
// project's own client factory. The union matters - f26 of the corpus reaches
// boto3 only through `get_client('ecs', event)`, so without the second source
// its ecs import gets no hint, and that is one of the three functions whose
// invented module path broke the build.
//
// Only the direct set feeds the feature vector; see Result.ClientFactoryLiterals.
// Unknown names are harmless here because AWSServices filters against a closed
// table, so a stray literal that happens to sit in a factory call is dropped
// unless it really is a service name.
func (r *Result) awsServiceNames() []string {
	if r == nil {
		return nil
	}
	total := len(r.Boto3Services) + len(r.ClientFactoryLiterals)
	seen := make(map[string]bool, total)
	out := make([]string, 0, total)
	for _, list := range [][]string{r.Boto3Services, r.ClientFactoryLiterals} {
		for _, s := range list {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// AWSHints renders the AWS SDK for Go v2 idioms a translation gets wrong most
// often. Empty unless the function actually touches AWS, so a prompt can test
// it with {{ if .aws_hints }} and non-AWS translations - 37 of the corpus's 95 -
// pay nothing for it.
//
// Every entry is drawn from the compiler and runtime diagnostics of run
// 20260831-190900 rather than from general SDK advice, with the number of
// diagnostics each accounted for; the point is to spend a small model's
// attention on what actually broke, not on a tour of the SDK.
func (r *Result) AWSHints() string {
	if r == nil || !r.UsesAWS() {
		return ""
	}

	hints := []string{
		// 33 diagnostics: "cannot use aws.Bool(true) (value of type *bool) as
		// bool value in struct literal".
		"`aws.Bool` / `aws.String` / `aws.Int32` return *pointers*. In v2 many fields that were pointers in v1 " +
			"are plain values - pass `true`, not `aws.Bool(true)`, unless the field's declared type really is `*bool`.",
		// 26 diagnostics: "types redeclared in this block" / "other
		// declaration of types".
		"Each service has its own `types` subpackage at `service/<svc>/types` (never `service/<svc>types`). " +
			"Importing two of them without aliases is `types redeclared in this block` - alias them " +
			"(`ddbtypes \"…/service/dynamodb/types\"`, `ecstypes \"…/service/ecs/types\"`).",
		// 12 diagnostics: "out.ResponseMetadata undefined (type
		// *dynamodb.DeleteItemOutput has no field or method ResponseMetadata)".
		"v2 output structs have **no `ResponseMetadata`** field - that is v1. There is no HTTP status code on the " +
			"output either; if the Python checked `response['ResponseMetadata']['HTTPStatusCode'] == 200`, " +
			"the Go equivalent is simply that `err == nil`.",
		// 5 build diagnostics for the v1 name, plus f72 failing at runtime with
		// "cannot unmarshal number into Go value of type types.AttributeValue".
		"DynamoDB items are `map[string]types.AttributeValue`, not JSON. Convert with " +
			"`github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue` (`MarshalMap`/`UnmarshalMap`) - " +
			"`dynamodbattribute` is the v1 name, and `json.Unmarshal` into an `AttributeValue` compiles but fails at runtime.",
		// The other half of the Floci endpoint fix: the Go SDK has no
		// AWS_S3_FORCE_PATH_STYLE, so path-style has to be asked for in code.
		"S3 needs path-style addressing against the emulator, and the Go SDK has **no environment variable** for it " +
			"(unlike boto3): `s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true })`. " +
			"Without it a bucket request goes virtual-host style and fails with a 301 PermanentRedirect.",
		"`botocore.exceptions.ClientError` has no equivalent: match the service's typed error with " +
			"`errors.As(err, &notFound)` where `notFound` is e.g. `*types.ResourceNotFoundException`.",
	}

	var b strings.Builder
	for _, h := range hints {
		fmt.Fprintf(&b, "- %s\n", h)
	}
	return strings.TrimRight(b.String(), "\n")
}

// UsesAWS reports whether the function touches AWS at all. It matches the
// `uses_aws` feature column's definition so hint and feature cannot disagree.
func (r *Result) UsesAWS() bool {
	if r == nil {
		return false
	}
	if len(r.Boto3Services) > 0 {
		return true
	}
	for _, imp := range r.Imports {
		if imp == "boto3" || imp == "botocore" {
			return true
		}
	}
	return false
}

// PyFeatures renders the constructs that most often survive a translation
// incorrectly. Only non-zero findings are listed: an empty section is more
// useful to a small model than a wall of "0 decorators, 0 generators".
func (r *Result) PyFeatures() string {
	if r == nil {
		return ""
	}

	var notes []string
	add := func(cond bool, format string, args ...any) {
		if cond {
			notes = append(notes, fmt.Sprintf(format, args...))
		}
	}

	add(r.Metric("n_decorators") > 0,
		"%d decorator(s): Go has no decorator syntax - inline the wrapper's behaviour into the function",
		int(r.Metric("n_decorators")))
	add(r.Metric("n_yield") > 0,
		"generator function(s) using yield: return a slice, or drive a channel, rather than emulating laziness")
	add(r.Metric("n_async_defs")+r.Metric("n_await") > 0,
		"async/await: Go is synchronous by default - a direct sequential translation is usually correct")
	add(r.Metric("n_star_args") > 0,
		"*args/**kwargs: Go needs an explicit parameter list or a struct")
	add(r.Metric("n_comprehensions") > 0,
		"%d comprehension(s): translate to explicit for loops", int(r.Metric("n_comprehensions")))
	add(r.Metric("n_lambdas") > 0,
		"%d lambda(s): translate to func literals", int(r.Metric("n_lambdas")))
	add(r.Metric("n_classes") > 0,
		"%d class(es): translate to structs with methods", int(r.Metric("n_classes")))
	add(r.Metric("module_level_stmts") > 5,
		"%d module-level statements run at import time - put the equivalent in init() or at the top of the handler, not inside it",
		int(r.Metric("module_level_stmts")))
	add(r.Metric("n_except") > 0,
		"%d except handler(s): Go has no exceptions - return errors and check them; a bare except becomes an error check around each fallible call",
		int(r.Metric("n_except")))
	add(r.Metric("n_raise") > 0,
		"%d raise(s): return an error instead of panicking", int(r.Metric("n_raise")))

	if len(r.DynamicCalls) > 0 {
		names := make([]string, 0, len(r.DynamicCalls))
		for name := range r.DynamicCalls {
			names = append(names, name)
		}
		sort.Strings(names)
		notes = append(notes, fmt.Sprintf(
			"dynamic/reflective calls (%s): Go's static typing has no direct equivalent - resolve them to concrete field or map access",
			strings.Join(names, ", ")))
	}

	if infeasible := Infeasible(r.Imports); len(infeasible) > 0 {
		for _, lib := range infeasible {
			notes = append(notes, fmt.Sprintf(
				"%s has no Go equivalent (%s) - a faithful translation may not be possible",
				lib.Python, lib.Note))
		}
	}

	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// FeasibilityWarning reports the reason a translation looks doomed before it
// is attempted, or "" when nothing stands out.
//
// This is deliberately a *warning*, not a gate: the prediction module's
// learned gate is [I10]'s job and stays off by default. What this does is
// record the deterministic half of that judgement on every job, so the
// signal exists in the metrics long before a model consumes it.
func (r *Result) FeasibilityWarning() string {
	if r == nil {
		return ""
	}
	infeasible := Infeasible(r.Imports)
	if len(infeasible) == 0 {
		return ""
	}
	names := make([]string, 0, len(infeasible))
	for _, lib := range infeasible {
		names = append(names, lib.Python)
	}
	return fmt.Sprintf("imports %s, which have no realistic Go equivalent", strings.Join(names, ", "))
}
