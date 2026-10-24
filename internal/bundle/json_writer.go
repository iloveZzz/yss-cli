package bundle

import (
	"bufio"
	"encoding/json"
	"io"
)

// writeBundleJSON emits exactly encoding/json's compact Bundle representation,
// without a trailing newline. Field order and omitempty rules follow Bundle.
// Large content containers are emitted one record at a time; leaf encoding stays
// with encoding/json so escaping, numbers and nil values keep their contract.
func writeBundleJSON(w io.Writer, b *Bundle) error {
	buffer := bufio.NewWriterSize(w, 64*1024)
	s := bundleJSONWriter{out: buffer}
	if b == nil {
		s.raw("null")
	} else {
		s.raw("{")
		if len(b.Installations) > 0 {
			s.member("installations", b.Installations)
		}
		if len(b.NativeTransforms) > 0 {
			s.field("nativeTransforms")
			s.raw("[")
			for i, t := range b.NativeTransforms {
				if i > 0 {
					s.raw(",")
				}
				s.value(t)
			}
			s.raw("]")
		}
		if b.Upgrade != nil {
			s.member("upgradePolicy", b.Upgrade)
		}
		// Struct-valued omitempty fields are always present in encoding/json v1.
		s.member("legacy", b.Legacy)
		s.member("schemaVersion", b.SchemaVersion)
		if b.TemplateVersion != "" {
			s.member("templateVersion", b.TemplateVersion)
		}
		if b.LegacyCLICommit != "" {
			s.member("legacyCLICommit", b.LegacyCLICommit)
		}
		if b.BundleHash != "" {
			s.member("bundleHash", b.BundleHash)
		}
		s.member("producer", b.Producer)
		s.member("sourcePolicy", b.SourcePolicy)
		s.member("profile", b.Profile)
		s.member("legacyVersion", b.LegacyVersion)
		if b.CLICommit != "" {
			s.member("cliCommit", b.CLICommit)
		}
		s.member("templateCommit", b.TemplateCommit)
		s.member("sourceState", b.SourceState)
		s.member("sourceSnapshotHash", b.SnapshotHash)
		s.member("manifestHash", b.ManifestHash)
		s.member("distribution", b.Distribution)
		s.member("manifest", b.Manifest)
		s.fileMap("initial", b.Initial)
		s.fileMap("files", b.Files)
		s.member("stageRequirements", b.StageRequirements)
		s.member("skillRequirements", b.SkillRequirements)
		s.raw("}")
	}
	if s.err != nil {
		return s.err
	}
	return buffer.Flush()
}

type bundleJSONWriter struct {
	out    io.Writer
	err    error
	fields int
}

func (s *bundleJSONWriter) raw(text string) {
	if s.err != nil {
		return
	}
	var n int
	n, s.err = io.WriteString(s.out, text)
	if s.err == nil && n != len(text) {
		s.err = io.ErrShortWrite
	}
}
func (s *bundleJSONWriter) value(value any) {
	if s.err != nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		s.err = err
		return
	}
	n, err := s.out.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	s.err = err
}
func (s *bundleJSONWriter) field(name string) {
	if s.fields > 0 {
		s.raw(",")
	}
	s.fields++
	s.raw(`"` + name + `":`)
}
func (s *bundleJSONWriter) member(name string, value any) { s.field(name); s.value(value) }
func (s *bundleJSONWriter) fileMap(name string, files map[string]File) {
	s.field(name)
	if files == nil {
		s.raw("null")
		return
	}
	s.raw("{")
	for i, ref := range sortedKeys(files) {
		if s.err != nil {
			return
		}
		if i > 0 {
			s.raw(",")
		}
		s.value(ref)
		s.raw(":")
		s.value(files[ref])
	}
	s.raw("}")
}
