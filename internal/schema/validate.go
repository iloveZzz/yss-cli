package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	js "github.com/santhosh-tekuri/jsonschema/v6"
)

// Issue is a deterministic instance-validation diagnostic. Path is a JSON
// Pointer; SchemaPath is an absolute schema keyword URI. A resource or parsing
// failure is returned as an error, never as a successful validation.
type Issue struct {
	Code       string `json:"code"`
	Path       string `json:"path"`
	SchemaPath string `json:"schemaPath"`
	Message    string `json:"message"`
}

type resource struct {
	data  any
	bytes []byte
}
type reference struct {
	uri  string
	path string
}
type resources struct {
	docs    map[string]resource
	visited map[string]bool
	pending []reference
	read    func(string) ([]byte, error)
}

type schemaScope struct {
	uri      string
	physical string
}

func fileURI(path string) string {
	name := filepath.ToSlash(path)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return (&url.URL{Scheme: "file", Path: name}).String()
}

func (r *resources) register(uri string, doc resource) error {
	if previous, ok := r.docs[uri]; ok && !bytes.Equal(previous.bytes, doc.bytes) {
		return fail("SCHEMA_ID_CONFLICT", uri, "resource URI is bound to different bytes")
	}
	r.docs[uri] = doc
	return nil
}

func (r *resources) load(file, uri string) error {
	file = filepath.Clean(file)
	read := r.read
	if read == nil {
		read = os.ReadFile
	}
	data, err := read(file)
	if err != nil {
		return fmt.Errorf("SCHEMA_READ: %s: %w", file, err)
	}
	var doc any
	if strings.EqualFold(filepath.Ext(file), ".json") {
		doc, err = parseJSON(data)
	} else {
		doc, err = Parse(data)
	}
	if err != nil {
		return fmt.Errorf("SCHEMA_PARSE: %s: %w", file, err)
	}
	entry := resource{data: doc, bytes: data}
	aliases := []string{uri, fileURI(file)}
	scope := schemaScope{uri: uri, physical: fileURI(file)}
	if obj, ok := doc.(map[string]any); ok {
		scope, err = scopedSchema(obj, scope, file)
		if err != nil {
			return err
		}
		aliases = append(aliases, scope.uri)
		if _, hasID := obj["$id"].(string); hasID {
			// URI aliases must not reapply a relative root ID when compiled.
			normalized := make(map[string]any, len(obj))
			for key, value := range obj {
				normalized[key] = value
			}
			normalized["$id"] = scope.uri
			entry.data = normalized
		}
	}
	for _, alias := range aliases {
		if err := r.register(alias, entry); err != nil {
			return err
		}
	}
	visitKey := file + "\x00" + scope.uri
	if r.visited[visitKey] {
		return nil
	}
	r.visited[visitKey] = true
	// Discover all inline IDs before resolving references, including IDs that
	// occur later than a reference in object order. Their standalone aliases
	// contain the actual subschema, never the enclosing document.
	if err := r.walk(doc, scope, "", true); err != nil {
		return err
	}
	return r.walk(doc, scope, "", false)
}

// scopedSchema keeps logical URI resolution and physical local-file lookup in
// step for relative IDs. An absolute ID is an alias, never a network location.
func scopedSchema(obj map[string]any, parent schemaScope, path string) (schemaScope, error) {
	if dialect, ok := obj["$schema"].(string); ok && dialect != "https://json-schema.org/draft/2020-12/schema" && dialect != "http://json-schema.org/draft/2020-12/schema" {
		return parent, fail("SCHEMA_DRAFT_UNSUPPORTED", path, "only JSON Schema 2020-12 is supported")
	}
	id, ok := obj["$id"].(string)
	if !ok {
		return parent, nil
	}
	parsed, err := url.Parse(id)
	if err != nil || parsed.Fragment != "" || strings.Contains(id, "#") {
		return parent, fail("SCHEMA_ID", path, "$id must be a URI without a fragment")
	}
	logical, err := url.Parse(parent.uri)
	if err != nil {
		return parent, fail("SCHEMA_ID", path, err.Error())
	}
	result := schemaScope{uri: logical.ResolveReference(parsed).String(), physical: parent.physical}
	if !parsed.IsAbs() && !strings.HasPrefix(id, "//") {
		if parsed.RawQuery != "" {
			return parent, fail("SCHEMA_ID", path, "query strings on local relative IDs are unsupported")
		}
		physical, err := url.Parse(parent.physical)
		if err != nil {
			return parent, fail("SCHEMA_ID", path, err.Error())
		}
		result.physical = physical.ResolveReference(parsed).String()
	}
	return result, nil
}

// walk visits only schema-bearing locations understood by the 2020-12
// compiler. const/enum/default/examples and extension data are opaque values.
func (r *resources) walk(value any, parent schemaScope, path string, discover bool) error {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	scope := parent
	if path != "" {
		var err error
		scope, err = scopedSchema(obj, parent, path)
		if err != nil {
			return err
		}
		if discover {
			if _, hasID := obj["$id"].(string); hasID {
				standalone := make(map[string]any, len(obj))
				for key, data := range obj {
					standalone[key] = data
				}
				standalone["$id"] = scope.uri
				encoded, err := json.Marshal(standalone)
				if err != nil {
					return fail("SCHEMA_ID", path, err.Error())
				}
				if err := r.register(scope.uri, resource{data: standalone, bytes: encoded}); err != nil {
					return err
				}
			}
		}
	}
	if !discover {
		for _, key := range []string{"$ref", "$dynamicRef"} {
			ref, ok := obj[key].(string)
			if !ok || strings.HasPrefix(ref, "#") {
				continue
			}
			child := path + "/" + key
			part := strings.SplitN(ref, "#", 2)[0]
			refURI, err := url.Parse(part)
			if err != nil {
				return fail("SCHEMA_REF", child, err.Error())
			}
			baseURI, _ := url.Parse(scope.uri)
			resolved := baseURI.ResolveReference(refURI).String()
			if _, exists := r.docs[resolved]; exists {
				continue
			}
			if refURI.IsAbs() || filepath.IsAbs(part) || strings.HasPrefix(part, "//") {
				r.pending = append(r.pending, reference{uri: resolved, path: child})
				continue
			}
			if refURI.RawQuery != "" {
				return fail("SCHEMA_REF", child, "query strings on local file references are unsupported")
			}
			physicalURI, _ := url.Parse(scope.physical)
			local := physicalURI.ResolveReference(refURI)
			if local.Scheme != "file" || local.Host != "" {
				return fail("SCHEMA_OFFLINE", child, "local schema closure must resolve to a file")
			}
			localPath := local.Path
			// A Windows file URI contains /C:/..., whereas native filesystem
			// paths begin C:\...; the URI separator is not a root-relative drive.
			if runtime.GOOS == "windows" && len(localPath) >= 3 && localPath[0] == '/' && localPath[2] == ':' {
				localPath = localPath[1:]
			}
			if err := r.load(filepath.FromSlash(localPath), resolved); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"additionalProperties", "contains", "contentSchema", "else", "if", "items", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties", "additionalItems"} {
		if err := r.walk(obj[key], scope, path+"/"+key, discover); err != nil {
			return err
		}
	}
	for _, key := range []string{"$defs", "definitions", "dependentSchemas", "patternProperties", "properties", "dependencies"} {
		if children, ok := obj[key].(map[string]any); ok {
			keys := make([]string, 0, len(children))
			for name := range children {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			for _, name := range keys {
				if err := r.walk(children[name], scope, path+"/"+key+"/"+pointerEscape(name), discover); err != nil {
					return err
				}
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems", "items"} {
		if children, ok := obj[key].([]any); ok {
			for i, item := range children {
				if err := r.walk(item, scope, fmt.Sprintf("%s/%s/%d", path, key, i), discover); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type offlineLoader struct{ docs map[string]resource }

func (l offlineLoader) Load(uri string) (any, error) {
	if doc, ok := l.docs[uri]; ok {
		return doc.data, nil
	}
	return nil, fail("SCHEMA_OFFLINE", uri, "unregistered schema resource; network and arbitrary file loading are disabled")
}

// Validate validates a single JSON/YAML asset against JSON Schema 2020-12.
// Formats are asserted. All referenced bytes must belong to the registered
// local closure. The result slice is empty for a valid instance.
func Validate(schemaPath, dataPath string) ([]Issue, error) {
	compiled, err := compileSchema(schemaPath)
	if err != nil {
		return nil, err
	}
	data, err := LoadFile(dataPath)
	if err != nil {
		return nil, err
	}
	return validateInstance(compiled, data)
}

// ValidateValue validates an in-memory JSON-compatible value without writing
// an instance file. JSON struct tags and marshalers are respected; strict
// re-parsing preserves json.Number precision and rejects ambiguous JSON emitted
// by custom marshalers. Schema loading and diagnostics match Validate.
func ValidateValue(schemaPath string, value any) ([]Issue, error) {
	return ValidateValueWithReader(schemaPath, value, nil)
}

// ValidateValueWithReader binds every file in the offline schema closure to
// the caller's observed read view. A nil reader retains the ordinary API.
func ValidateValueWithReader(schemaPath string, value any, read func(string) ([]byte, error)) ([]Issue, error) {
	compiled, err := compileSchemaWithReader(schemaPath, read)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("ASSET_VALUE: %w", err)
	}
	data, err := parseJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("ASSET_VALUE: %w", err)
	}
	return validateInstance(compiled, data)
}

func compileSchema(schemaPath string) (*js.Schema, error) {
	return compileSchemaWithReader(schemaPath, nil)
}

func compileSchemaWithReader(schemaPath string, read func(string) ([]byte, error)) (*js.Schema, error) {
	absolute, err := filepath.Abs(schemaPath)
	if err != nil {
		return nil, err
	}
	r := &resources{docs: map[string]resource{}, visited: map[string]bool{}, read: read}
	if err := r.load(absolute, fileURI(absolute)); err != nil {
		return nil, err
	}
	for _, ref := range r.pending {
		if _, ok := r.docs[ref.uri]; !ok {
			return nil, fail("SCHEMA_OFFLINE", ref.path, "unregistered schema resource "+ref.uri)
		}
	}
	c := js.NewCompiler()
	c.DefaultDraft(js.Draft2020)
	c.AssertFormat()
	c.UseLoader(offlineLoader{docs: r.docs})
	c.UseRegexpEngine(compatibleRegexpCompile)
	keys := make([]string, 0, len(r.docs))
	for uri := range r.docs {
		keys = append(keys, uri)
	}
	sort.Strings(keys)
	for _, uri := range keys {
		if err := c.AddResource(uri, r.docs[uri].data); err != nil {
			return nil, fmt.Errorf("SCHEMA_RESOURCE: %w", err)
		}
	}
	// Validate every resource's schema shape, including local closure members
	// whose validation branches may not be exercised by the instance.
	for _, uri := range keys {
		if _, err := c.Compile(uri); err != nil {
			return nil, fmt.Errorf("SCHEMA_COMPILE: %w", err)
		}
	}
	compiled, err := c.Compile(fileURI(absolute))
	if err != nil {
		return nil, fmt.Errorf("SCHEMA_COMPILE: %w", err)
	}
	return compiled, nil
}

func validateInstance(compiled *js.Schema, data any) ([]Issue, error) {
	if err := compiled.Validate(data); err != nil {
		var invalid *js.ValidationError
		if !errors.As(err, &invalid) {
			return nil, fmt.Errorf("SCHEMA_VALIDATE: %w", err)
		}
		issues := []Issue{}
		var collect func(*js.ValidationError)
		collect = func(e *js.ValidationError) {
			if len(e.Causes) > 0 {
				for _, cause := range e.Causes {
					collect(cause)
				}
				return
			}
			issues = append(issues, Issue{Code: "SCHEMA_VALIDATION", Path: jsonPointer(e.InstanceLocation), SchemaPath: e.SchemaURL + jsonPointer(e.ErrorKind.KeywordPath()), Message: e.Error()})
		}
		collect(invalid)
		sort.Slice(issues, func(i, j int) bool {
			if issues[i].Path != issues[j].Path {
				return issues[i].Path < issues[j].Path
			}
			if issues[i].SchemaPath != issues[j].SchemaPath {
				return issues[i].SchemaPath < issues[j].SchemaPath
			}
			return issues[i].Message < issues[j].Message
		})
		return issues, nil
	}
	return []Issue{}, nil
}

func jsonPointer(parts []string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteByte('/')
		b.WriteString(pointerEscape(part))
	}
	return b.String()
}

// Existing validation uses Python regular expressions. Reject known syntax or
// Unicode-class differences instead of silently accepting Go-only semantics.
// A compatible replacement pattern must be chosen explicitly by its owner.
func compatibleRegexpCompile(pattern string) (js.Regexp, error) {
	translated, err := translatePattern(pattern, "<regex>")
	if err != nil {
		return nil, err
	}
	return regexp.Compile(translated)
}

func translatePattern(pattern, path string) (string, error) {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			i++
			escape := pattern[i]
			if body, ok := pythonUnicode15Classes[escape]; ok {
				if !inClass {
					b.WriteByte('[')
				}
				b.WriteString(body)
				if !inClass {
					b.WriteByte(']')
				}
				continue
			}
			if strings.ContainsRune("bB0123456789pPNzQE", rune(escape)) {
				if escape == 'b' && inClass {
					b.WriteString("\\x{08}")
					continue
				}
				return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "word boundaries, backreferences, octal escapes and non-Python escapes require an explicit compatibility decision")
			}
			if escape == 'u' || escape == 'U' {
				count := 4
				if escape == 'U' {
					count = 8
				}
				if i+count >= len(pattern) {
					return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "truncated Unicode escape")
				}
				value, err := strconv.ParseUint(pattern[i+1:i+1+count], 16, 32)
				if err != nil || value > 0x10ffff || (value >= 0xd800 && value <= 0xdfff) {
					return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "invalid Unicode escape")
				}
				fmt.Fprintf(&b, "\\x{%x}", value)
				i += count
				continue
			}
			if escape == 'Z' {
				if inClass {
					return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "anchor inside character class")
				}
				b.WriteString("\\z")
				continue
			}
			if escape == 'a' {
				b.WriteString("\\x{07}")
				continue
			}
			if escape == 'x' && (i+2 >= len(pattern) || !isHex(pattern[i+1]) || !isHex(pattern[i+2])) {
				return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "Python hex escapes require exactly two hex digits")
			}
			if ((escape >= 'a' && escape <= 'z') || (escape >= 'A' && escape <= 'Z')) && !strings.ContainsRune("nrtfvxA", rune(escape)) {
				return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "unsupported Python escape")
			}
			b.WriteByte(ch)
			b.WriteByte(escape)
			continue
		}
		if !inClass && ch == '(' && i+1 < len(pattern) && pattern[i+1] == '?' && !strings.HasPrefix(pattern[i:], "(?:") {
			return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "lookaround, flags, or named groups require an explicit compatibility decision")
		}
		if ch == '[' {
			if inClass && i+1 < len(pattern) && strings.ContainsRune(":.=", rune(pattern[i+1])) {
				return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, "POSIX character classes have different Python semantics")
			}
			inClass = true
		}
		if ch == ']' {
			inClass = false
		}
		if ch == '$' && !inClass {
			b.WriteString("(?:\\n)?\\z")
		} else {
			b.WriteByte(ch)
		}
	}
	translated := b.String()
	if _, err := regexp.Compile(translated); err != nil {
		return "", fail("SCHEMA_REGEX_INCOMPATIBLE", path, err.Error())
	}
	return translated, nil
}

func isHex(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
