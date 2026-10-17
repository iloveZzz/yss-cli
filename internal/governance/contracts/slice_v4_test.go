package contracts

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"testing"
)

func TestSlice4SelectsWorkUnitWithoutRole(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	unit := map[string]any{"id": "work-unit.domain", "primary_skill": "yss-domain", "check_ids": []string{"check.code-reviewed"}, "project_root": "backend/app", "allowed_write_paths": []string{"src/domain"}}
	c := &NativeSlice{Raw: map[string]any{"schema_version": json.Number("4")}, Normalized: map[string]any{"common": map[string]any{"allowed_write_paths": []string{"src"}}, "work_units": []any{unit}, "backend": map[string]any{"status": "required"}}}
	selected, err := ContractSelectLocalUnit(s, c, "work-unit.domain")
	if err != nil {
		t.Fatal(err)
	}
	if !kernel.ApSetEqual(kernel.ApMap(selected.Normalized["common"])["allowed_write_paths"], []string{"src/domain"}) {
		t.Fatal("Slice4 did not narrow to its work unit")
	}
	if _, ok := kernel.ApRows(selected.Normalized["work_units"])[0]["role_id"]; ok {
		t.Fatal("role synthesized")
	}
	if _, err = ContractSelectLocalUnit(s, c, "work-unit.unknown"); err == nil {
		t.Fatal("unknown Slice4 unit accepted")
	}
	if !kernel.ApSetEqual(kernel.ApMap(c.Normalized["common"])["allowed_write_paths"], []string{"src"}) {
		t.Fatal("authoritative contract mutated")
	}
}
