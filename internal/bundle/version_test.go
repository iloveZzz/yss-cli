package bundle

import "testing"

func TestParseSemver(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"0.0.0", true},
		{"4.4.1", true},
		{"1.2.3-rc.1", true},
		{"1.2.3-alpha.beta.0", true},
		{"01.2.3", false},
		{"1.2", false},
		{"1.2.3+build", false},
		{"1.2.3-01", false},
		{"v1.2.3", false},
		{"", false},
	} {
		if _, ok := parseSemver(tc.in); ok != tc.ok {
			t.Errorf("parseSemver(%q) ok=%v, want %v", tc.in, ok, tc.ok)
		}
		if IsSemver(tc.in) != tc.ok {
			t.Errorf("IsSemver(%q) != %v", tc.in, tc.ok)
		}
	}
}

func TestSemverCompareFollowsPrecedence(t *testing.T) {
	t.Parallel()
	// SemVer 2.0 §11 的示例顺序，逐对严格递增。
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0"}
	for i := 0; i+1 < len(ordered); i++ {
		a, _ := parseSemver(ordered[i])
		b, _ := parseSemver(ordered[i+1])
		if a.compare(b) != -1 || b.compare(a) != 1 {
			t.Errorf("%s 应低于 %s", ordered[i], ordered[i+1])
		}
	}
	v, _ := parseSemver("3.2.1-rc.1")
	if v.compare(v) != 0 {
		t.Error("同一版本比较应为 0")
	}
}

func TestVersionRangeValidateAndContains(t *testing.T) {
	t.Parallel()
	for _, r := range []VersionRange{{}, {Min: "2.0.0", Max: "1.0.0"}, {Min: "1.0.0", Max: "1.0.0"}, {Min: "x"}, {Max: "1.0"}} {
		if r.Validate() == nil {
			t.Errorf("区间 %+v 应非法", r)
		}
		if r.Contains("1.0.0") {
			t.Errorf("非法区间 %+v 不应包含任何版本", r)
		}
	}
	r := VersionRange{Min: "2.3.1", Max: "3.0.0"}
	if err := r.Validate(); err != nil {
		t.Fatalf("合法区间被拒绝: %v", err)
	}
	for version, want := range map[string]bool{"2.3.1": true, "2.9.9": true, "3.0.0": false, "2.3.0": false, "3.0.0-rc.1": true, "2.3.1-rc.1": false, "not-a-version": false} {
		if got := r.Contains(version); got != want {
			t.Errorf("[2.3.1,3.0.0) Contains(%s)=%v, want %v", version, got, want)
		}
	}
	if !(VersionRange{Max: "1.0.0"}).Contains("0.1.0") || (VersionRange{Min: "1.0.0"}).Contains("0.9.9") {
		t.Error("单端区间判定错误")
	}
}

func TestVersionRangeOverlaps(t *testing.T) {
	t.Parallel()
	a := VersionRange{Min: "1.0.0", Max: "2.0.0"}
	for _, tc := range []struct {
		b    VersionRange
		want bool
	}{
		{VersionRange{Min: "2.0.0", Max: "3.0.0"}, false}, // 左闭右开：相邻不相交
		{VersionRange{Min: "0.1.0", Max: "1.0.0"}, false},
		{VersionRange{Min: "1.5.0", Max: "3.0.0"}, true},
		{VersionRange{Min: "0.1.0"}, true},
		{VersionRange{Max: "1.0.1"}, true},
		{VersionRange{Min: "bad"}, true}, // 非法区间按相交处理，交给 Validate 拒绝
	} {
		if got := a.overlaps(tc.b); got != tc.want {
			t.Errorf("overlaps(%+v)=%v, want %v", tc.b, got, tc.want)
		}
		if got := tc.b.overlaps(a); got != tc.want {
			t.Errorf("overlaps 不对称: %+v", tc.b)
		}
	}
}

func TestSourcesOverlapExactAgainstRange(t *testing.T) {
	t.Parallel()
	commit := "7af9715c6136d1b48e2c731fe92603806d7bf22a"
	upTo405 := SourceMatch{Profile: "spec", Versions: &VersionRange{Max: "4.0.5"}}
	exact := func(version string) SourceMatch {
		return SourceMatch{Profile: "spec", TemplateCommit: commit, SnapshotHash: "a", TemplateVersion: version}
	}
	for _, tc := range []struct {
		name    string
		exact   SourceMatch
		overlap bool
	}{
		{"未记录版本按重叠处理", exact(""), true},
		{"git 版本不会被版本区间匹配", exact("git:" + commit), false},
		{"语义化版本落在区间内", exact("4.0.1"), true},
		{"语义化版本在区间外", exact("4.1.0"), false},
	} {
		if got := sourcesOverlap(upTo405, tc.exact); got != tc.overlap {
			t.Errorf("%s: range-exact overlap=%v, want %v", tc.name, got, tc.overlap)
		}
		if got := sourcesOverlap(tc.exact, upTo405); got != tc.overlap {
			t.Errorf("%s: exact-range overlap=%v, want %v", tc.name, got, tc.overlap)
		}
	}
	if sourcesOverlap(exact("git:"+commit), SourceMatch{Profile: "design", Versions: &VersionRange{Max: "4.0.5"}}) {
		t.Error("不同 Profile 不重叠")
	}
}
