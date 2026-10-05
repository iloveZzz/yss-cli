package governance

import (
	"encoding/binary"
	"path"
	"sort"
)

type nativeWorktreeFile struct {
	RawPath []byte
	Mode    uint32
	Kind    byte
	Content []byte
}
type nativeWorktreeSnapshot struct {
	Manifest    map[string]any
	Stream      []byte
	TrackedDiff []byte
	Files       []nativeWorktreeFile
}

// This only parses and validates a saved candidate. The backend consumer must
// independently compare it with its current registered Git/worktree inputs.
func (s *semanticSession) worktreeCandidate(ref string) (*nativeWorktreeSnapshot, error) {
	manifest, err := s.doc(ref)
	if err != nil {
		return nil, err
	}
	if manifest["storage"] != "packed-stream" {
		return nil, s.reject("MAINTENANCE_CANDIDATE", "只支持 packed-stream candidate")
	}
	v, local, err := s.referenceView(ref)
	if err != nil {
		return nil, err
	}
	inspector := s
	if v != s.v {
		inspector = newSemanticSession(s.ctx, v.root, s.args)
		s.children = append(s.children, inspector)
	}
	members, err := inspector.list(path.Dir(local))
	if err != nil {
		return nil, err
	}
	sort.Strings(members)
	if !equalStrings(members, []string{"candidate-manifest.yaml", "candidate.bin", "tracked.diff"}) {
		return nil, s.reject("MAINTENANCE_CANDIDATE", "packed candidate 目录须恰好含三个规范成员")
	}
	if path.Base(local) != "candidate-manifest.yaml" {
		return nil, s.reject("MAINTENANCE_CANDIDATE", "候选 Manifest 文件名不规范")
	}
	record := map[string]any{"candidate_snapshot_ref": ref, "candidate_digest": manifest["candidate_digest"]}
	if err = s.maintenanceCandidate(manifest, record); err != nil {
		return nil, err
	}
	streamRef, diffRef := text(manifest["snapshot_stream_ref"]), text(manifest["tracked_diff_ref"])
	if version, _ := integer(manifest["schema_version"]); version == 2 {
		base := path.Dir(ref)
		streamRef, diffRef = base+"/"+streamRef, base+"/"+diffRef
	} else if streamRef != path.Dir(ref)+"/candidate.bin" || diffRef != path.Dir(ref)+"/tracked.diff" {
		return nil, s.reject("MAINTENANCE_CANDIDATE", "packed candidate 引用须绑定同目录规范文件")
	}
	stream, err := s.bytes(streamRef)
	if err != nil {
		return nil, err
	}
	diff, err := s.bytes(diffRef)
	if err != nil {
		return nil, err
	}
	out := &nativeWorktreeSnapshot{Manifest: manifest, Stream: stream, TrackedDiff: diff, Files: []nativeWorktreeFile{}}
	// maintenanceCandidate has validated every framing boundary and inventory;
	// bytes reads also reject changes between that check and this decode.
	position := len("YSS-WORKTREE-CANDIDATE-V1\x00") + 1
	trackedLength := int(binary.BigEndian.Uint64(stream[position : position+8]))
	position += 8 + trackedLength
	for position < len(stream) {
		if err = s.guard(); err != nil {
			return nil, err
		}
		position++
		length := int(binary.BigEndian.Uint64(stream[position : position+8]))
		position += 8
		rawPath := stream[position : position+length]
		position += length
		mode := binary.BigEndian.Uint32(stream[position : position+4])
		position += 4
		kind := stream[position]
		position++
		length = int(binary.BigEndian.Uint64(stream[position : position+8]))
		position += 8
		content := stream[position : position+length]
		position += length
		out.Files = append(out.Files, nativeWorktreeFile{RawPath: rawPath, Mode: mode, Kind: kind, Content: content})
	}
	return out, nil
}
