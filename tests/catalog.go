package tests

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	docsTOOLS          = "TOOLS.md"
	docsESI            = "ESI.md"
	sideList           = "tools/list"
	sideInit           = "initialize"
	sideAbsent         = "(absent)"
	sharedWord         = "shared"
	typeNumber         = "number"
	typeBoolean        = "boolean"
	typeString         = "string"
	typeArray          = "array"
	typeObject         = "object"
	docTypeInt         = "int"
	docTypeFloat       = "float64"
	docTypeBool        = "bool"
	docTypeString      = "string"
	docTypeStringList  = "string list"
	docTypeObjectList  = "object list"
	pageCursor         = "cursor"
	pageNumbered       = "page"
	pageFolded         = "offset"
	pageNone           = "none"
	paramPage          = "page"
	paramOffset        = "offset"
	paramLastMailID    = "last_mail_id"
	paramFromEvent     = "from_event"
	fieldRequired      = "required"
	fieldBounds        = "bounds"
	fieldPagination    = "pagination"
	fieldItems         = "items"
	notInCatalog       = "## Not in this catalog"
	serverInstructions = "## Server instructions"
	methodGET          = "GET"
	methodPOST         = "POST"
	methodPUT          = "PUT"
	methodDELETE       = "DELETE"
	minSharedCols      = 2
	quoteLimit         = 240
	quotePrefixMin     = 48
	quoteShort         = 160
)

var (
	errNoModule       = errors.New("tests: no go.mod above the working directory")
	errUnknownShared  = errors.New("tests: unknown shared parameter")
	errBadHeading     = errors.New("tests: tool heading is not a backticked eve_ name")
	errBadTable       = errors.New("tests: parameter table is missing a Bounds column")
	errBadRequired    = errors.New("tests: required cell is not yes or no")
	errBadBounds      = errors.New("tests: bounds cell is not a range, a minimum, or an em dash")
	errBadESIMethod   = errors.New("tests: ESI.md row is not GET, POST, PUT or DELETE")
	errNoInstructions = errors.New("tests: Server instructions fence is missing")
	reToolHeading     = regexp.MustCompile("^### `([a-z0-9_]+)`\\s*$")
	reBareEveHeading  = regexp.MustCompile(`^### eve_`)
	reTickName        = regexp.MustCompile("`([a-z0-9_]+)`")
	reTickParam       = regexp.MustCompile("`([a-z0-9_]+)`\\s*\\(`([a-z0-9_]+)`\\)")
	reESICell         = regexp.MustCompile("`([A-Z]+) ([^`]+)`")
	reFormatVerb      = regexp.MustCompile(`%#?[0-9]*[vdsxXq]`)
	reBraceParam      = regexp.MustCompile(`\{[^}]+\}`)
)

type catalog struct {
	Tools        map[string]toolSpec
	Instructions string
	Shared       map[string]string
	Paging       map[string]pageClass
}

type toolSpec struct {
	Name        string
	Description string
	Params      map[string]paramSpec
}

type paramSpec struct {
	Name        string
	DocType     string
	SchemaType  string
	ItemType    string
	Required    bool
	Bounds      bound
	Description string
	Fields      map[string]paramSpec
}

type bound struct {
	Min *float64
	Max *float64
	Raw string
}

type pageClass struct {
	Kind  string
	Param string
}

type esiDoc struct {
	Endpoints map[string]esiRow
}

type esiRow struct {
	Method string
	Path   string
}

type esiCall struct {
	Method string
	Path   string
	File   string
}

type finding struct {
	Tool  string
	Field string
	Doc   string
	Got   string
}

func (f finding) String() string {
	doc, got := quotePair(f.Doc, f.Got)

	return fmt.Sprintf("%s %s: %s %s; %s %s", f.Tool, f.Field, docsTOOLS, doc, sideList, got)
}

func parseTOOLS(text string) (catalog, error) {
	out := catalog{
		Tools:  map[string]toolSpec{},
		Shared: map[string]string{},
		Paging: map[string]pageClass{},
	}
	lines := splitLines(text)
	i := 0
	for i < len(lines) {
		line := lines[i]
		switch {
		case isPipeTable(line) && tableKind(line) == "shared":
			shared, next := parseSharedTable(lines, i)
			maps.Copy(out.Shared, shared)
			i = next
		case strings.HasPrefix(line, serverInstructions):
			block, next, err := parseInstructionFence(lines, i+1)
			if err != nil {
				return catalog{}, err
			}
			out.Instructions = block
			i = next
		case strings.HasPrefix(line, "### Pagination by tool"):
			paging, next := parsePagingTable(lines, i+1)
			out.Paging = paging
			i = next
		case reBareEveHeading.MatchString(line) && !reToolHeading.MatchString(line):
			return catalog{}, fmt.Errorf("%w: %s", errBadHeading, line)
		case reToolHeading.MatchString(line):
			spec, next, err := parseDocTool(lines, i, out.Shared)
			if err != nil {
				return catalog{}, err
			}
			out.Tools[spec.Name] = spec
			i = next
		case strings.HasPrefix(line, notInCatalog):
			return out, nil
		default:
			i++
		}
	}

	return out, nil
}

func parseESI(text string) (esiDoc, error) {
	out := esiDoc{Endpoints: map[string]esiRow{}}
	for _, line := range splitLines(text) {
		if !strings.Contains(line, "|") {
			continue
		}
		cell := firstTableCell(line)
		m := reESICell.FindStringSubmatch(cell)
		if m == nil {
			continue
		}
		method, raw := m[1], m[2]
		if strings.HasPrefix(raw, "https://") {
			continue
		}
		if !isESIMethod(method) {
			return esiDoc{}, fmt.Errorf("%w: %s %s", errBadESIMethod, method, raw)
		}
		row := esiRow{Method: method, Path: normalizePath(raw)}
		out.Endpoints[esiKey(row.Method, row.Path)] = row
	}

	return out, nil
}

func diffTools(doc catalog, live []map[string]any) []finding {
	var out []finding
	seen := map[string]struct{}{}
	for _, raw := range live {
		name := jStr(raw[fieldName])
		seen[name] = struct{}{}
		want, ok := doc.Tools[name]
		if !ok {
			out = append(out, finding{Tool: name, Field: fieldName, Doc: sideAbsent, Got: name})

			continue
		}
		out = append(out, diffTool(want, raw, doc.Paging[name])...)
	}
	var missing []string
	for name := range doc.Tools {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	for _, name := range missing {
		out = append(out, finding{Tool: name, Field: fieldName, Doc: name, Got: sideAbsent})
	}
	sortFindings(out)

	return out
}

func diffInstructions(want, got string) []finding {
	if strings.TrimSpace(want) == strings.TrimSpace(got) {
		return nil
	}

	return []finding{{
		Tool:  "instructions",
		Field: "text",
		Doc:   strings.TrimSpace(want),
		Got:   strings.TrimSpace(got),
	}}
}

func diffESI(doc esiDoc, calls []esiCall) []finding {
	have := map[string][]string{}
	for _, c := range calls {
		k := esiKey(c.Method, c.Path)
		have[k] = appendUnique(have[k], c.File)
	}
	var out []finding
	for k, row := range doc.Endpoints {
		if _, ok := have[k]; ok {
			continue
		}
		out = append(out, finding{
			Tool:  "esi",
			Field: k,
			Doc:   row.Method + " " + row.Path,
			Got:   sideAbsent,
		})
	}
	var extra []string
	for k := range have {
		if _, ok := doc.Endpoints[k]; !ok {
			extra = append(extra, k)
		}
	}
	slices.Sort(extra)
	for _, k := range extra {
		out = append(out, finding{
			Tool:  "esi",
			Field: k,
			Doc:   sideAbsent,
			Got:   k + " at " + strings.Join(have[k], ", "),
		})
	}
	sortFindings(out)

	return out
}

func diffTool(want toolSpec, live map[string]any, paging pageClass) []finding {
	var out []finding
	gotDesc := jStr(live[fieldDescription])
	if normalizeWS(want.Description) != normalizeWS(gotDesc) {
		out = append(out, finding{Tool: want.Name, Field: fieldDescription, Doc: want.Description, Got: gotDesc})
	}
	schema := jMap(live[fieldInputSchema])
	props := jMap(schema[fieldProperties])
	required := requiredSet(schema["required"])
	seen := map[string]struct{}{}
	names := make([]string, 0, len(want.Params))
	for name := range want.Params {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		seen[name] = struct{}{}
		out = append(out, diffParam(want.Name, name, want.Params[name], jMap(props[name]), required[name])...)
	}
	var extra []string
	for name := range props {
		if _, ok := seen[name]; !ok {
			extra = append(extra, name)
		}
	}
	slices.Sort(extra)
	for _, name := range extra {
		out = append(out, finding{Tool: want.Name, Field: name, Doc: sideAbsent, Got: schemaTypeOf(jMap(props[name]))})
	}
	out = append(out, diffPaging(want.Name, paging, props)...)

	return out
}

func diffParam(tool, name string, want paramSpec, prop map[string]any, required bool) []finding {
	if len(prop) == 0 {
		return []finding{{Tool: tool, Field: name, Doc: want.DocType, Got: sideAbsent}}
	}
	var out []finding
	gotType := schemaTypeOf(prop)
	if want.SchemaType != "" && gotType != want.SchemaType {
		out = append(out, finding{Tool: tool, Field: name + "." + fieldType, Doc: want.SchemaType, Got: gotType})
	}
	if want.ItemType != "" {
		items := jMap(prop[fieldItems])
		gotItem := schemaTypeOf(items)
		if gotItem != want.ItemType {
			out = append(out, finding{Tool: tool, Field: name + "." + fieldItems, Doc: want.ItemType, Got: emptyAsAbsent(gotItem)})
		}
	}
	if want.Required != required {
		out = append(out, finding{
			Tool: tool, Field: name + "." + fieldRequired,
			Doc: strconv.FormatBool(want.Required), Got: strconv.FormatBool(required),
		})
	}
	gotDesc := jStr(prop[fieldDescription])
	if normalizeWS(want.Description) != normalizeWS(gotDesc) {
		out = append(out, finding{Tool: tool, Field: name + "." + fieldDescription, Doc: want.Description, Got: gotDesc})
	}
	gotBound := boundFromSchema(prop)
	if !sameBound(want.Bounds, gotBound) {
		out = append(out, finding{Tool: tool, Field: name + "." + fieldBounds, Doc: formatBound(want.Bounds), Got: formatBound(gotBound)})
	}
	if len(want.Fields) == 0 {
		return out
	}
	items := jMap(prop[fieldItems])
	nested := jMap(items[fieldProperties])
	nestedReq := requiredSet(items["required"])
	var fields []string
	for f := range want.Fields {
		fields = append(fields, f)
	}
	slices.Sort(fields)
	for _, f := range fields {
		out = append(out, diffParam(tool, name+"."+f, want.Fields[f], jMap(nested[f]), nestedReq[f])...)
	}

	return out
}

func diffPaging(tool string, paging pageClass, props map[string]any) []finding {
	if paging.Kind != pageNone {
		return nil
	}
	for _, name := range []string{paramPage, paramOffset, paramLastMailID, paramFromEvent} {
		if _, ok := props[name]; ok {
			return []finding{{Tool: tool, Field: fieldPagination, Doc: pageNone, Got: name}}
		}
	}

	return nil
}

func parseDocTool(lines []string, start int, shared map[string]string) (toolSpec, int, error) {
	m := reToolHeading.FindStringSubmatch(lines[start])
	spec := toolSpec{Name: m[1], Params: map[string]paramSpec{}}
	i := start + 1
	var desc []string
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, notInCatalog) || reToolHeading.MatchString(line) || reBareEveHeading.MatchString(line) {
			break
		}
		if strings.HasPrefix(line, "*Source:") || strings.HasPrefix(line, "*Source :") {
			i++

			continue
		}
		if strings.HasPrefix(line, "_No parameters._") {
			spec.Description = strings.TrimSpace(strings.Join(desc, "\n"))
			return spec, i + 1, nil
		}
		if isPipeTable(line) && tableKind(line) == "params" {
			spec.Description = strings.TrimSpace(strings.Join(desc, "\n"))
			params, next, err := parseParamTable(lines, i, shared)
			if err != nil {
				return toolSpec{}, 0, fmt.Errorf("%s: %w", spec.Name, err)
			}
			spec.Params = params
			i = next
			for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
				i++
			}
			if i < len(lines) && strings.Contains(lines[i], "Each `modules` entry") {
				fields, after, err := parseFieldTable(lines, i+1)
				if err != nil {
					return toolSpec{}, 0, fmt.Errorf("%s modules: %w", spec.Name, err)
				}
				mod := spec.Params["modules"]
				mod.Fields = fields
				spec.Params["modules"] = mod
				i = after
			}

			return spec, i, nil
		}
		if isPipeTable(line) {
			return toolSpec{}, 0, fmt.Errorf("%s: %w", spec.Name, errBadTable)
		}
		desc = append(desc, line)
		i++
	}
	spec.Description = strings.TrimSpace(strings.Join(desc, "\n"))

	return spec, i, nil
}

func parseSharedTable(lines []string, start int) (map[string]string, int) {
	rows, next := readTable(lines, start)
	out := map[string]string{}
	for _, row := range rows {
		if len(row) < minSharedCols {
			continue
		}
		name := stripTicks(row[0])
		if name == "" || name == "Parameter" {
			continue
		}
		out[name] = strings.TrimSpace(row[1])
	}

	return out, next
}

func parseParamTable(lines []string, start int, shared map[string]string) (map[string]paramSpec, int, error) {
	header := splitRow(lines[start])
	if !hasBoundsColumn(header) {
		return nil, 0, errBadTable
	}
	rows, next := readTable(lines, start)
	out := map[string]paramSpec{}
	for _, row := range rows {
		if len(row) < 5 || stripTicks(row[0]) == "Parameter" {
			continue
		}
		p, err := paramFromRow(row, shared)
		if err != nil {
			return nil, 0, err
		}
		out[p.Name] = p
	}

	return out, next, nil
}

func parseFieldTable(lines []string, start int) (map[string]paramSpec, int, error) {
	for start < len(lines) && !isPipeTable(lines[start]) {
		if strings.TrimSpace(lines[start]) == "" {
			start++

			continue
		}

		break
	}
	if start >= len(lines) || !isPipeTable(lines[start]) {
		return nil, start, nil
	}
	rows, next := readTable(lines, start)
	out := map[string]paramSpec{}
	for _, row := range rows {
		if len(row) < 4 || stripTicks(row[0]) == "Field" {
			continue
		}
		req, err := parseRequired(row[2])
		if err != nil {
			return nil, 0, err
		}
		docType := strings.TrimSpace(row[1])
		p := paramSpec{
			Name: stripTicks(row[0]), DocType: docType, SchemaType: mapDocType(docType),
			Required: req, Description: strings.TrimSpace(row[3]),
		}
		out[p.Name] = p
	}

	return out, next, nil
}

func parsePagingTable(lines []string, start int) (map[string]pageClass, int) {
	for start < len(lines) && !isPipeTable(lines[start]) {
		start++
	}
	if start >= len(lines) {
		return map[string]pageClass{}, start
	}
	rows, next := readTable(lines, start)
	out := map[string]pageClass{}
	for _, row := range rows {
		if len(row) < 3 || strings.Contains(row[0], "Shape") {
			continue
		}
		kind, def := pagingKind(row[0])
		toolsCell := row[len(row)-1]
		for _, hit := range reTickParam.FindAllStringSubmatch(toolsCell, -1) {
			out[hit[1]] = pageClass{Kind: kind, Param: hit[2]}
		}
		stripped := reTickParam.ReplaceAllString(toolsCell, "")
		for _, hit := range reTickName.FindAllStringSubmatch(stripped, -1) {
			name := hit[1]
			if !strings.HasPrefix(name, "eve_") {
				continue
			}
			if _, ok := out[name]; ok {
				continue
			}
			out[name] = pageClass{Kind: kind, Param: def}
		}
	}

	return out, next
}

func parseInstructionFence(lines []string, start int) (string, int, error) {
	i := start
	for i < len(lines) && strings.TrimSpace(lines[i]) != "```" {
		i++
	}
	if i >= len(lines) {
		return "", 0, errNoInstructions
	}
	i++
	var b strings.Builder
	for i < len(lines) && strings.TrimSpace(lines[i]) != "```" {
		b.WriteString(lines[i])
		b.WriteByte('\n')
		i++
	}
	if i >= len(lines) {
		return "", 0, errNoInstructions
	}

	return strings.TrimSpace(b.String()), i + 1, nil
}

func paramFromRow(row []string, shared map[string]string) (paramSpec, error) {
	name := stripTicks(row[0])
	docType := strings.TrimSpace(row[1])
	req, err := parseRequired(row[2])
	if err != nil {
		return paramSpec{}, fmt.Errorf("%s: %w", name, err)
	}
	b, err := parseBound(row[3])
	if err != nil {
		return paramSpec{}, fmt.Errorf("%s: %w", name, err)
	}
	desc := strings.TrimSpace(row[4])
	if desc == sharedWord {
		got, ok := shared[name]
		if !ok {
			return paramSpec{}, fmt.Errorf("%s: %w", name, errUnknownShared)
		}
		desc = got
	}
	p := paramSpec{
		Name: name, DocType: docType, SchemaType: mapDocType(docType),
		Required: req, Bounds: b, Description: desc,
	}
	switch docType {
	case docTypeStringList:
		p.ItemType = typeString
	case docTypeObjectList:
		p.ItemType = typeObject
	}

	return p, nil
}

func parseRequired(raw string) (bool, error) {
	s := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, "*", "")))
	switch s {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %q", errBadRequired, raw)
	}
}

func parseBound(raw string) (bound, error) {
	s := strings.TrimSpace(raw)
	out := bound{Raw: s}
	if s == "" || s == "—" || s == "-" || s == "–" {
		return out, nil
	}
	norm := strings.Map(func(r rune) rune {
		switch r {
		case '−', '–', '—':
			return '-'
		case '≥':
			return '>'
		case ' ':
			return -1
		default:
			return r
		}
	}, s)
	if strings.HasPrefix(norm, ">=") || strings.HasPrefix(norm, ">") {
		n := strings.TrimPrefix(strings.TrimPrefix(norm, ">="), ">")
		v, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return bound{}, fmt.Errorf("%w: %q", errBadBounds, raw)
		}
		out.Min = &v

		return out, nil
	}
	lo, hi, ok := splitRange(norm)
	if !ok {
		return bound{}, fmt.Errorf("%w: %q", errBadBounds, raw)
	}
	a, err := strconv.ParseFloat(lo, 64)
	if err != nil {
		return bound{}, fmt.Errorf("%w: %q", errBadBounds, raw)
	}
	b, err := strconv.ParseFloat(hi, 64)
	if err != nil {
		return bound{}, fmt.Errorf("%w: %q", errBadBounds, raw)
	}
	out.Min, out.Max = &a, &b

	return out, nil
}

func splitRange(s string) (string, string, bool) {
	start := 0
	if strings.HasPrefix(s, "-") {
		start = 1
	}
	i := strings.Index(s[start:], "-")
	if i < 0 {
		return "", "", false
	}
	i += start

	return s[:i], s[i+1:], true
}

func mapDocType(doc string) string {
	switch doc {
	case docTypeInt:
		return typeInteger
	case docTypeFloat:
		return typeNumber
	case docTypeBool:
		return typeBoolean
	case docTypeString:
		return typeString
	case docTypeStringList, docTypeObjectList:
		return typeArray
	default:
		return doc
	}
}

func pagingKind(shape string) (string, string) {
	s := strings.ToLower(shape)
	switch {
	case strings.Contains(s, "cursor"):
		return pageCursor, ""
	case strings.Contains(s, "numbered"):
		return pageNumbered, paramPage
	case strings.Contains(s, "folded"):
		return pageFolded, paramOffset
	default:
		return pageNone, ""
	}
}

func boundFromSchema(prop map[string]any) bound {
	var out bound
	if v, ok := floatPtr(prop["minimum"]); ok {
		out.Min = v
	}
	if v, ok := floatPtr(prop["maximum"]); ok {
		out.Max = v
	}

	return out
}

func sameBound(a, b bound) bool {
	return sameFloat(a.Min, b.Min) && sameFloat(a.Max, b.Max)
}

func sameFloat(a, b *float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	return *a == *b
}

func formatBound(b bound) string {
	switch {
	case b.Min != nil && b.Max != nil:
		return fmt.Sprintf("%g–%g", *b.Min, *b.Max)
	case b.Min != nil:
		return fmt.Sprintf("≥ %g", *b.Min)
	case b.Max != nil:
		return fmt.Sprintf("≤ %g", *b.Max)
	default:
		return "—"
	}
}

func schemaTypeOf(prop map[string]any) string {
	if len(prop) == 0 {
		return ""
	}
	if t := concreteType(prop[fieldType]); t != "" {
		return t
	}
	if _, ok := prop[fieldItems]; ok {
		return typeArray
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		for _, alt := range jSlice(prop[key]) {
			if t := schemaTypeOf(jMap(alt)); t != "" {
				return t
			}
		}
	}

	return ""
}

func concreteType(v any) string {
	switch t := v.(type) {
	case string:
		if t == "null" {
			return ""
		}

		return t
	case []any:
		for _, x := range t {
			if s := concreteType(x); s != "" {
				return s
			}
		}
	}

	return ""
}

func jSlice(v any) []any {
	s, ok := v.([]any)
	if !ok {
		return nil
	}

	return s
}

func requiredSet(v any) map[string]bool {
	out := map[string]bool{}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out[s] = true
			}
		}
	case []string:
		for _, s := range t {
			out[s] = true
		}
	}

	return out
}

func floatPtr(v any) (*float64, bool) {
	switch t := v.(type) {
	case float64:
		return &t, true
	case int:
		f := float64(t)

		return &f, true
	case jsonNumber:
		f, err := t.Float64()
		if err != nil {
			return nil, false
		}

		return &f, true
	default:
		return nil, false
	}
}

type jsonNumber interface {
	Float64() (float64, error)
}

func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = reFormatVerb.ReplaceAllString(p, "{}")
	p = reBraceParam.ReplaceAllString(p, "{}")

	return p
}

func esiKey(method, path string) string {
	return method + " " + path
}

func isESIMethod(m string) bool {
	switch m {
	case methodGET, methodPOST, methodPUT, methodDELETE:
		return true
	default:
		return false
	}
}

func splitLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func isPipeTable(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

func tableKind(header string) string {
	cells := splitRow(header)
	joined := strings.ToLower(strings.Join(cells, " "))
	switch {
	case strings.Contains(joined, "bounds") && strings.Contains(joined, "parameter"):
		return "params"
	case strings.Contains(joined, "field") && strings.Contains(joined, "type"):
		return "fields"
	case strings.Contains(joined, "shape"):
		return "paging"
	case len(cells) == 2 && strings.Contains(joined, "parameter") && strings.Contains(joined, "description"):
		return "shared"
	default:
		return ""
	}
}

func hasBoundsColumn(header []string) bool {
	for _, c := range header {
		if strings.EqualFold(strings.TrimSpace(c), "Bounds") {
			return true
		}
	}

	return false
}

func readTable(lines []string, start int) ([][]string, int) {
	var rows [][]string
	i := start
	for i < len(lines) && isPipeTable(lines[i]) {
		row := splitRow(lines[i])
		if !isAlignRow(row) {
			rows = append(rows, row)
		}
		i++
	}

	return rows, i
}

func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}

	return out
}

func isAlignRow(row []string) bool {
	if len(row) == 0 {
		return false
	}
	for _, c := range row {
		if strings.Trim(c, ":-") != "" {
			return false
		}
	}

	return true
}

func firstTableCell(line string) string {
	row := splitRow(line)
	if len(row) == 0 {
		return ""
	}

	return row[0]
}

func stripTicks(s string) string {
	return strings.Trim(strings.TrimSpace(s), "`")
}

func quoteSide(s string) string {
	if s == sideAbsent {
		return s
	}
	if len(s) > quoteLimit {
		return strconv.Quote(s[:quoteLimit] + "…")
	}

	return strconv.Quote(s)
}

func quotePair(doc, got string) (string, string) {
	if doc == sideAbsent || got == sideAbsent || (len(doc) < quoteShort && len(got) < quoteShort) {
		return quoteSide(doc), quoteSide(got)
	}
	n := 0
	for n < len(doc) && n < len(got) && doc[n] == got[n] {
		n++
	}
	if n < quotePrefixMin {
		return quoteSide(doc), quoteSide(got)
	}

	return quoteSide("…" + doc[n:]), quoteSide("…" + got[n:])
}

func emptyAsAbsent(s string) string {
	if s == "" {
		return sideAbsent
	}

	return s
}

func appendUnique(dst []string, v string) []string {
	if slices.Contains(dst, v) {
		return dst
	}

	return append(dst, v)
}

func sortFindings(fs []finding) {
	slices.SortFunc(fs, func(a, b finding) int {
		if a.Tool != b.Tool {
			return strings.Compare(a.Tool, b.Tool)
		}

		return strings.Compare(a.Field, b.Field)
	})
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("tests: working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errNoModule
		}
		dir = parent
	}
}

func readDoc(root, name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "docs", name))
	if err != nil {
		return "", fmt.Errorf("tests: read %s: %w", name, err)
	}

	return string(raw), nil
}

func jStr(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}

	return s
}

func jMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}

	return m
}

func isPagingParam(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	switch base {
	case paramPage, paramOffset, paramLastMailID, paramFromEvent, fieldPagination:
		return true
	default:
		return false
	}
}
