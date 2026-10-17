package project

import (
	"bytes"
	"encoding/base64"
	"os/exec"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func decodeCandidate(t *testing.T, c *Candidate) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(c.Data)
	if err != nil {
		t.Fatal(err)
	}
	if c.Digest != safefs.Digest(raw) {
		t.Fatal("候选摘要与内容不一致")
	}
	return raw
}

func TestMergeCandidate(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("需要 git merge-file")
	}
	base := []byte("a\nb\nc\nd\ne\n")

	clean, err := mergeCandidate([]byte("A\nb\nc\nd\ne\n"), base, []byte("a\nb\nc\nd\nE\n"))
	if err != nil || clean == nil || !clean.Clean {
		t.Fatalf("互不重叠的改动应干净合并: %+v, %v", clean, err)
	}
	if got := decodeCandidate(t, clean); string(got) != "A\nb\nc\nd\nE\n" {
		t.Errorf("干净合并结果 = %q", got)
	}

	conflict, err := mergeCandidate([]byte("a\nLOCAL\nc\nd\ne\n"), base, []byte("a\nTEMPLATE\nc\nd\ne\n"))
	if err != nil || conflict == nil || conflict.Clean {
		t.Fatalf("同一行的不同改动应产生冲突候选: %+v, %v", conflict, err)
	}
	raw := decodeCandidate(t, conflict)
	for _, marker := range []string{"<<<<<<< local", "||||||| base", ">>>>>>> template", "LOCAL", "TEMPLATE"} {
		if !bytes.Contains(raw, []byte(marker)) {
			t.Errorf("冲突候选缺少 %q（标签需固定，结果才可复现）:\n%s", marker, raw)
		}
	}

	// 二进制内容不生成文本候选。
	if c, err := mergeCandidate([]byte("a\x00"), base, base); c != nil || err != nil {
		t.Errorf("含 NUL 的输入不应合并: %+v, %v", c, err)
	}
}
