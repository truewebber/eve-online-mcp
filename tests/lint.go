package tests

import (
	"fmt"
	"strings"

	"github.com/truewebber/eve-online-mcp/internal/domain/j"
)

type findings struct {
	failures []string
	warnings []string
}

func lintTool(tool map[string]any) []string {
	return inspectTool(tool).failures
}

func inspectTool(tool map[string]any) findings {
	name := j.Str(tool[fieldName])
	description := j.Str(tool[fieldDescription])
	schema := j.Map(tool[fieldInputSchema])
	props := j.Map(schema[fieldProperties])
	if props == nil {
		props = map[string]any{}
	}
	out := findings{}
	if !strings.HasPrefix(name, "eve_") {
		out.failures = append(out.failures, name+": not namespaced under 'eve_'")
	}
	if len(description) < minDescriptionChars {
		out.failures = append(out.failures, fmt.Sprintf("%s: description is only %d chars", name, len(description)))
	}
	if len(description) > maxDescriptionChars {
		out.warnings = append(out.warnings, fmt.Sprintf("%s: description is %d chars, consider trimming", name, len(description)))
	}
	if strings.Contains(description, "\n    ") || description != strings.TrimSpace(description) {
		out.failures = append(out.failures, name+": description carries raw docstring indentation")
	}
	f, w := lintProps(name, props)
	out.failures = append(out.failures, f...)
	out.warnings = append(out.warnings, w...)

	return out
}

func lintProps(name string, props map[string]any) ([]string, []string) {
	var failures, warnings []string
	for param, specAny := range props {
		spec := j.Map(specAny)
		if j.Str(spec[fieldDescription]) == "" {
			failures = append(failures, name+"."+param+": no description in the schema")
		}
		// Game ids are opaque 64-bit values with no meaningful upper bound;
		// only tunables like `limit` benefit from a declared range.
		if j.Str(spec[fieldType]) == typeInteger {
			if _, ok := spec["maximum"]; !ok && !strings.HasSuffix(param, "_id") {
				warnings = append(warnings, name+"."+param+": unbounded integer, no maximum in schema")
			}
		}
		switch param {
		case "user", "id", "target_id", "data", "input":
			warnings = append(warnings, name+"."+param+": ambiguous parameter name")
		}
	}
	if _, hasLimit := props[fieldLimit]; hasLimit {
		_, hasFormat := props["response_format"]
		if !hasFormat && needsResponseFormat(name) {
			warnings = append(warnings, name+": has `limit` but no `response_format`")
		}
	}

	return failures, warnings
}
