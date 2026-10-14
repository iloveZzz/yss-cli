package kernel

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"path"
	"strings"
)

func (s *SemanticSession) MaintenanceCandidate(manifest, record map[string]any) error {
	for _, k := range []string{"review_mode", "review_base_ref", "merge_base", "implementation_candidate_ref", "candidate_snapshot_ref", "candidate_digest", "tracked_diff_command", "commit_list_command", "untracked_inventory_command", "untracked_diff_command", "snapshot_stream_ref", "tracked_diff_ref"} {
		if strings.TrimSpace(Text(manifest[k])) == "" {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选 Manifest 缺少 "+k)
		}
	}
	version, ok := Integer(manifest["schema_version"])
	if !ok || version < 1 || version > 2 {
		return s.Unavailable("CAPABILITY", "未知维护候选 Manifest 版本")
	}
	if manifest["review_mode"] != "worktree" || manifest["candidate_snapshot_ref"] != record["candidate_snapshot_ref"] || manifest["candidate_digest"] != record["candidate_digest"] {
		return s.Reject("MAINTENANCE_CANDIDATE", "审查候选主体或摘要不一致")
	}
	for _, k := range []string{"untracked_files", "untracked_path_bytes"} {
		if _, ok := manifest[k].([]any); !ok {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选缺少 untracked inventory")
		}
	}
	packed := manifest["storage"] == "packed-stream"
	if !packed {
		if manifest["storage"] != nil && manifest["storage"] != "" {
			return s.Unavailable("CAPABILITY", "未知候选流存储格式")
		}
		if _, ok := manifest["untracked_content_refs"].([]any); !ok {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选缺少独立 untracked snapshots")
		}
	} else if manifest["untracked_content_refs"] != nil {
		return s.Reject("MAINTENANCE_CANDIDATE", "packed-stream 不允许重复保存 untracked snapshots")
	}
	streamRef, diffRef := Text(manifest["snapshot_stream_ref"]), Text(manifest["tracked_diff_ref"])
	if version == 2 {
		excluded, ok := manifest["excluded_paths"].([]any)
		if !ok || len(excluded) != 0 || manifest["reference_base"] != "bundle" || manifest["workspace_id"] != safefs.Digest([]byte(s.Root)) {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选 Bundle 不属于当前工作区或包含排除范围")
		}
		if streamRef != "candidate.bin" || diffRef != "tracked.diff" {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选成员须为同目录规范文件")
		}
		base := path.Dir(Text(record["candidate_snapshot_ref"]))
		streamRef, diffRef = base+"/"+streamRef, base+"/"+diffRef
	}
	stream, err := s.Bytes(streamRef)
	if err != nil {
		return err
	}
	if safefs.Digest(stream) != strings.TrimPrefix(Text(record["candidate_digest"]), "sha256:") {
		return s.Reject("MAINTENANCE_CANDIDATE", "候选流字节摘要过期")
	}
	diff, err := s.Bytes(diffRef)
	if err != nil {
		return err
	}
	const magic = "YSS-WORKTREE-CANDIDATE-V1\x00"
	if !bytes.HasPrefix(stream, []byte(magic)) {
		return s.Reject("MAINTENANCE_CANDIDATE", "候选流格式标记无效")
	}
	position := len(magic)
	take := func(n uint64) ([]byte, error) {
		if n > 9007199254740991 || n > uint64(len(stream)-position) {
			return nil, s.Reject("MAINTENANCE_CANDIDATE", "候选流截断或长度无效")
		}
		out := stream[position : position+int(n)]
		position += int(n)
		return out, nil
	}
	length := func(width uint64) (uint64, error) {
		raw, e := take(width)
		if e != nil {
			return 0, e
		}
		if width == 8 {
			return binary.BigEndian.Uint64(raw), nil
		}
		return uint64(binary.BigEndian.Uint32(raw)), nil
	}
	marker, err := take(1)
	if err != nil {
		return err
	}
	if marker[0] != 0x54 {
		return s.Reject("MAINTENANCE_CANDIDATE", "缺少 tracked record")
	}
	n, err := length(8)
	if err != nil {
		return err
	}
	tracked, err := take(n)
	if err != nil {
		return err
	}
	if !bytes.Equal(tracked, diff) {
		return s.Reject("MAINTENANCE_CANDIDATE", "tracked diff 与候选流不一致")
	}
	paths := []any{}
	var previous []byte
	for position < len(stream) {
		if err = s.Guard(); err != nil {
			return err
		}
		marker, err = take(1)
		if err != nil {
			return err
		}
		if marker[0] != 0x55 {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选流 record kind 无效")
		}
		n, err = length(8)
		if err != nil {
			return err
		}
		rawPath, err := take(n)
		if err != nil {
			return err
		}
		if len(rawPath) == 0 || bytes.ContainsRune(rawPath, 0) || previous != nil && bytes.Compare(previous, rawPath) >= 0 {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选路径为空、重复或排序非法")
		}
		previous = rawPath
		mode, err := length(4)
		if err != nil {
			return err
		}
		kind, err := take(1)
		if err != nil {
			return err
		}
		if kind[0] != 0x52 && kind[0] != 0x4c || kind[0] == 0x52 && mode&0170000 != 0100000 || kind[0] == 0x4c && mode&0170000 != 0120000 {
			return s.Reject("MAINTENANCE_CANDIDATE", "候选 kind 与 mode 无效")
		}
		n, err = length(8)
		if err != nil {
			return err
		}
		content, err := take(n)
		if err != nil {
			return err
		}
		if !packed {
			refs := SemStrings(manifest["untracked_content_refs"])
			if len(refs) <= len(paths) {
				return s.Reject("MAINTENANCE_CANDIDATE", "候选 untracked snapshot 数量不足")
			}
			stored, err := s.Bytes(refs[len(paths)])
			if err != nil {
				return err
			}
			if !bytes.Equal(content, stored) {
				return s.Reject("MAINTENANCE_CANDIDATE", "untracked snapshot 字节不一致")
			}
		}
		paths = append(paths, base64.StdEncoding.EncodeToString(rawPath))
	}
	if !ApEqual(paths, manifest["untracked_path_bytes"]) || len(SemList(manifest["untracked_files"])) != len(paths) || !packed && len(SemList(manifest["untracked_content_refs"])) != len(paths) {
		return s.Reject("MAINTENANCE_CANDIDATE", "候选 untracked inventory 与流不一致")
	}
	return nil
}
