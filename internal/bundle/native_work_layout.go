package bundle

import (
	"embed"
	"io/fs"
	"path"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// These generated inputs mirror canonical template consumers. Fixed Git source
// and native compatibility output have separate digests and provenance records.
//
//go:embed all:work-layout-assets
var nativeWorkLayoutAssets embed.FS

func nativeWorkLayoutSource(raw map[string]sourceFile) ([]NativeTransform, error) {
	transforms := []NativeTransform{}
	record := func(ref string, data []byte, absent bool) {
		original := raw[ref]
		mode := original.mode
		if mode == 0 {
			mode = 0644
		}
		if !absent && safefs.Digest(original.data) == safefs.Digest(data) {
			return
		}
		transforms = append(transforms, NativeTransform{Path: ref, Generator: "native-work-layout-v1", Source: encodedFile(original.data, mode, "managed"), SourceAbsent: absent})
		raw[ref] = sourceFile{data: data, mode: mode}
	}
	e := fs.WalkDir(nativeWorkLayoutAssets, "work-layout-assets", func(file string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		ref := strings.TrimPrefix(file, "work-layout-assets/")
		_, exists := raw[ref]
		if !exists && ref != "scripts/lib/work-layout.mjs" && ref != ".template-spec/process/work-layout.md" {
			return nil
		}
		data, e := nativeWorkLayoutAssets.ReadFile(file)
		if e != nil {
			return e
		}
		if ref == "scripts/lib/lifecycle-transition.mjs" {
			// Lifecycle routes and public exports belong to each Profile. Apply
			// only its Ticket path adaptation, never a root Profile replacement.
			data = layoutLifecycleSource(raw[ref].data)
		}
		if ref == ".gitignore" {
			// All Profiles consume the instance block; source-only ignores must
			// never hide durable work packages in specialist instances.
			data, e = renderSource("spec", ref, data, false, nil)
			if e != nil {
				return e
			}
		}
		record(ref, data, !exists)
		if strings.HasPrefix(ref, ".agents/skills/") {
			for _, platform := range []string{".codex", ".cursor", ".pi"} {
				projected := platform + strings.TrimPrefix(ref, ".agents")
				if _, ok := raw[projected]; ok {
					record(projected, data, false)
				}
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	// Only active authoring guidance is transformed. Fixtures, asset snapshots,
	// schemas and history remain unchanged; Profile-specific text stays intact.
	for _, ref := range sortedKeys(raw) {
		if !(strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, ".agents/skills/") || strings.HasPrefix(ref, ".codex/skills/") || strings.HasPrefix(ref, ".cursor/skills/") || strings.HasPrefix(ref, ".pi/skills/")) {
			continue
		}
		if strings.Contains(ref, "/tests/") || strings.Contains(ref, "/assets/") || strings.Contains(ref, "/schemas/") || strings.Contains(ref, "/scripts/") || ref == ".template-spec/agents/issue-tracker.md" || ref == ".template-spec/process/work-layout.md" {
			continue
		}
		if path.Ext(ref) != ".md" && path.Ext(ref) != ".yaml" {
			continue
		}
		data := raw[ref].data
		output := strings.ReplaceAll(string(data), "docs/.scratch/<feature>", ".work/<feature>")
		if output != string(data) {
			record(ref, []byte(output), false)
		}
	}
	return transforms, nil
}

func layoutLifecycleSource(data []byte) []byte {
	const oldTicket = `return /^docs\/\.scratch\/[^/]+\/issues\/[^/]+\.md$/.test(ref);`
	input := string(data)
	if !strings.Contains(input, oldTicket) {
		return data
	}
	input = strings.Replace(input, "function validateTicketPath(ref)", "function validateTicketPath(ref, root)", 1)
	input = strings.Replace(input, oldTicket, "try { return readWorkLayout(root).isTicket(ref); } catch { return false; }", 1)
	input = strings.Replace(input, "function validateTicketReference(ref, trackerKind)", "function validateTicketReference(ref, trackerKind, root)", 1)
	input = strings.Replace(input, "return validateTicketPath(ref);", "return validateTicketPath(ref, root);", 1)
	root := "decisionOptions.root || ROOT"
	if strings.Contains(input, "STRATEGIC_PROFILE_ID") {
		root = "root"
		input = strings.Replace(input, "validateTicketFormalization(state, { exists =", "validateTicketFormalization(state, { root = ROOT, exists =", 1)
	}
	input = strings.ReplaceAll(input, "validateTicketReference(ticket.ref, trackerKind)", "validateTicketReference(ticket.ref, trackerKind, "+root+")")
	input = strings.ReplaceAll(input, "vertical_slice_ticket.ref under docs/.scratch/<feature>/issues/", "vertical_slice_ticket.ref under configured tracker.root/<feature>/issues/")
	return []byte("import { readWorkLayout } from './work-layout.mjs';\n" + input)
}
