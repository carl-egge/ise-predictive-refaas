package pipeline

import (
	"fmt"
	"strings"
)

// redactedMarker replaces a secret's value when ConverterOptions is formatted.
const redactedMarker = "[REDACTED]"

// secretArgKeys reports whether an Args key names a credential. Args is
// merged with envDefaults(), so it routinely carries ACADEMIC_CLOUD_API_KEY
// and GEMINI_API_KEY alongside harmless endpoints and ports.
func isSecretArgKey(key string) bool {
	k := strings.ToUpper(key)
	for _, suffix := range []string{"API_KEY", "KEY", "TOKEN", "SECRET", "PASSWORD"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// printableOptions exists only to give String a type without a String method,
// so formatting the copy cannot recurse back into ConverterOptions.String.
type printableOptions ConverterOptions

// String renders the options with credential-shaped Args values masked.
//
// It is defined on ConverterOptions itself, rather than at the one call site
// that logs it, so that every present and future %v/%+v of these options is
// redacted by construction ([F6]). Service startup and /reconfigure both log
// them, and that output is captured into run logs kept as evaluation
// artifacts — a leak there is durable and hard to retract.
func (o ConverterOptions) String() string {
	if len(o.Args) > 0 {
		args := make(map[string]any, len(o.Args))
		for k, v := range o.Args {
			if isSecretArgKey(k) {
				args[k] = redactedMarker
				continue
			}
			args[k] = v
		}
		o.Args = args
	}
	return fmt.Sprintf("%+v", printableOptions(o))
}
