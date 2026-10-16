package scaffold

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const platformCatalogRef = ".template-spec/engineering/backend-platforms.json"

var exactPatch = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Catalog is the maintenance-owned backend platform catalog shipped in the Bundle.
type Catalog struct {
	Profiles      []*OMap
	Compatibility []*OMap
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sha256Ref(data []byte) string { return "sha256:" + sha256Hex(data) }

// platformDigest hashes a value like the Node helper: strings are hashed as they
// are, everything else as JSON with recursively sorted object keys.
func platformDigest(value any) string {
	if text, ok := value.(string); ok {
		return sha256Ref([]byte(text))
	}
	if value == nil {
		return sha256Ref([]byte("null"))
	}
	return sha256Ref([]byte(JSON(stable(value), "")))
}

func loadCatalog(src Source) (*Catalog, error) {
	data, err := src.Read(platformCatalogRef)
	if err != nil {
		return nil, err
	}
	return parseCatalog(data)
}

func parseCatalog(data []byte) (*Catalog, error) {
	parsed, err := ParseJSON(data)
	if err != nil {
		return nil, fail("backend-platform: invalid catalog")
	}
	root := asMap(parsed)
	if root == nil || fieldString(root, "schema_version") != "2" || asString(mustGet(root, "kind")) != "backend-platform-catalog" || asList(mustGet(root, "profiles")) == nil || asList(mustGet(root, "compatibility")) == nil {
		return nil, fail("backend-platform: invalid catalog")
	}
	catalog := &Catalog{}
	seen := map[string]bool{}
	for _, item := range asList(mustGet(root, "profiles")) {
		profile := asMap(item)
		if profile == nil {
			return nil, fail("backend-platform: invalid catalog")
		}
		key := fieldString(profile, "id") + ":" + fieldString(profile, "spring_boot_version") + ":" + fieldString(profile, "java_version")
		if seen[key] {
			return nil, fail("backend-platform: duplicate catalog release/id")
		}
		seen[key] = true
		if !exactPatch.MatchString(fieldString(profile, "spring_boot_version")) {
			return nil, fail("backend-platform: catalog requires exact patch versions")
		}
		catalog.Profiles = append(catalog.Profiles, profile)
	}
	ids := map[string]bool{}
	for _, item := range asList(mustGet(root, "compatibility")) {
		entry := asMap(item)
		if entry == nil || ids[fieldString(entry, "id")] {
			return nil, fail("backend-platform: duplicate catalog release/id")
		}
		ids[fieldString(entry, "id")] = true
		catalog.Compatibility = append(catalog.Compatibility, entry)
	}
	return catalog, nil
}

func (c *Catalog) profile(id, version string) (*OMap, error) {
	var matches []*OMap
	for _, profile := range c.Profiles {
		if fieldString(profile, "id") == id && (version == "" || fieldString(profile, "spring_boot_version") == version) {
			matches = append(matches, profile)
		}
	}
	if len(matches) > 1 {
		return nil, fail("backend-platform: multiple patches: select an exact spring_boot_version")
	}
	if len(matches) == 0 {
		return nil, fail("backend-platform: unsupported platform_profile: " + id)
	}
	return matches[0], nil
}

func mustGet(m *OMap, key string) any {
	value, _ := m.Get(key)
	return value
}

// fieldString is String(value) for a field; an absent field is "undefined".
func fieldString(m *OMap, key string) string {
	value, ok := m.Get(key)
	if !ok {
		return "undefined"
	}
	return jsString(value)
}

func stringField(m *OMap, key string) string { return fieldString(m, key) }

func intField(m *OMap, key string) int {
	n, _ := strconv.Atoi(fieldString(m, key))
	return n
}

func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case Num:
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return string(v)
		}
		return jsNumber(f)
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case float64:
		return jsNumber(v)
	default:
		return fmt.Sprint(v)
	}
}

// platformTemplateVars mirrors platformTemplateVars(profile) in backend-platform.mjs.
func platformTemplateVars(profile *OMap) *OMap {
	java := intField(profile, "java_version")
	javaVersion, javaRange := strconv.Itoa(java), "["+strconv.Itoa(java)+","+strconv.Itoa(java+1)+")"
	if java == 8 {
		javaVersion, javaRange = "1.8", "[1.8,1.9)"
	}
	vars := newMap(
		"boot_version", fieldString(profile, "spring_boot_version"),
		"java_version", javaVersion,
		"java_range", javaRange,
		"web_starter", fieldString(profile, "web_starter"),
		"web_test_starter", fieldString(profile, "web_test_starter"),
		"aop_starter", fieldString(profile, "aop_starter"),
		"swagger_artifact", fieldString(profile, "swagger_artifact"),
	)
	if versions := asMap(mustGet(profile, "versions")); versions != nil {
		for _, key := range versions.Keys() {
			vars.Set(key+"_version", jsString(mustGet(versions, key)))
		}
	}
	return vars
}

// replaceVars applies `{{key}}` replacements one key at a time, in key order.
func replaceVars(text string, vars *OMap) string {
	for _, key := range vars.Keys() {
		text = strings.ReplaceAll(text, "{{"+key+"}}", jsString(mustGet(vars, key)))
	}
	return text
}
