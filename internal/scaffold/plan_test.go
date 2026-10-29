//go:build !yss_nobundle

package scaffold

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestBoundScaffoldPlanRoundTrip(t *testing.T) {
	ctx := context.Background()
	catalog, err := DescribeCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defaults, choices := 0, map[string]bool{}
	for _, choice := range catalog.Installations {
		choices[choice.ID] = true
		if choice.Default {
			defaults++
			if choice.ID != "governed" {
				t.Fatalf("unexpected installation default: %s", choice.ID)
			}
		}
	}
	if defaults != 1 || !choices["skills"] || !choices["governed"] {
		t.Fatalf("installation choices: %+v", catalog.Installations)
	}
	var platform *CatalogPlatform
	for i := range catalog.Platforms {
		if catalog.Platforms[i].StandaloneAllowed {
			platform = &catalog.Platforms[i]
			break
		}
	}
	if platform == nil || catalog.Kind != "scaffold-catalog" || catalog.SchemaVersion != 1 || len(catalog.Fields) == 0 {
		t.Fatalf("fixed catalog unavailable: %+v", catalog)
	}
	for _, architecture := range []string{"ddd", "mvc"} {
		t.Run(architecture, func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "中文 & parent", "not-created")
			withoutExample := false
			o := Options{Architecture: architecture, ProjectName: "demo", BasePackage: "com.example.demo", OutputDir: parent,
				GroupID: "com.example", ProjectVersion: "0.1.0-SNAPSHOT", PlatformProfile: platform.PlatformProfile,
				SpringBootVersion: platform.SpringBootVersion, JavaVersion: platform.JavaVersion, ParentGroupID: platform.ParentGroupID,
				ParentArtifactID: platform.ParentArtifactID, ParentVersion: platform.ParentVersion, YssComponentsVersion: platform.YssComponentsVersion, WithExample: &withoutExample}
			plan, err := BuildPlan(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(parent); !os.IsNotExist(err) {
				t.Fatalf("preview created parent: %v", err)
			}
			file := filepath.Join(root, "confirmed.json")
			if err := SavePlan(plan, file); err != nil {
				t.Fatal(err)
			}
			plan, err = ReadPlan(file)
			if err != nil {
				t.Fatal(err)
			}
			before, err := VerifyPlanOutput(ctx, plan)
			if err != nil || before.Outcome != "not-applied" {
				t.Fatalf("preview output: %v %v", before, err)
			}
			_, err = ApplyPlan(ctx, plan, "different")
			var rejection *domain.Error
			if !errors.As(err, &rejection) || rejection.Code != "INPUT_DRIFT" {
				t.Fatalf("unconfirmed plan accepted: %v", err)
			}
			if _, err := os.Stat(parent); !os.IsNotExist(err) {
				t.Fatalf("rejected apply wrote target: %v", err)
			}
			if _, err := ApplyPlan(ctx, plan, plan.Digest); err != nil {
				t.Fatal(err)
			}
			actual, err := VerifyPlanOutput(ctx, plan)
			if err != nil || actual.Outcome != "complete" {
				t.Fatalf("applied output: %v %v", actual, err)
			}
			marker := filepath.Join(plan.ProjectRoot, ".idea", "user.txt")
			if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte("user"), 0600); err != nil {
				t.Fatal(err)
			}
			actual, err = VerifyPlanOutput(ctx, plan)
			if err != nil || actual.Outcome != "complete" {
				t.Fatalf("user file invalidated output: %v %v", actual, err)
			}
			if err := os.WriteFile(filepath.Join(plan.ProjectRoot, manifestRef), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			actual, err = VerifyPlanOutput(ctx, plan)
			if err != nil || actual.Outcome != "incomplete" {
				t.Fatalf("manifest drift missed: %v %v", actual, err)
			}
			if _, err := ApplyPlan(ctx, plan, plan.Digest); err == nil {
				t.Fatal("existing target overwritten")
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "user" {
				t.Fatalf("user file lost: %q %v", data, err)
			}
		})
	}
}
