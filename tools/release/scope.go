package release

import (
	"reflect"
	"sort"
)

const LocalQualificationScope = "local-platform-impacted-consumers"
const FullQualificationScope = "complete-template-release"

// ProgramInstallQualificationScope 只要求本机平台真实的 version、全部现役 Profile 的 bundle inspect
// 与离线程序安装四项；不含 native-smoke、native-recovery、plugin-native-smoke
// 和模板消费者检查，这些未覆盖项须在发行说明中如实列出。
const ProgramInstallQualificationScope = "local-platform-program-install"

func nativeChecksFor(scope string) []string {
	if scope == ProgramInstallQualificationScope {
		return []string{}
	}
	return nativeChecks
}

func scopedChecks(scope string) ([]string, error) {
	switch scope {
	case LocalQualificationScope:
		return []string{"program-installation", "scoped-template-consumers"}, nil
	case ProgramInstallQualificationScope:
		return []string{"program-installation"}, nil
	case FullQualificationScope:
		return []string{"full-template-integration", "cli-integration"}, nil
	default:
		return nil, reject("EVIDENCE", "independent qualification scope is required")
	}
}

func requiredPlatforms(input, expected []string) ([]string, error) {
	if len(input) == 0 || len(expected) == 0 {
		return nil, reject("PLATFORMS", "independently declared release platforms are required")
	}
	valid := func(values []string) ([]string, error) {
		seen := map[string]bool{}
		out := append([]string(nil), values...)
		for _, value := range out {
			found := false
			for _, supported := range Platforms {
				found = found || value == supported
			}
			if !found || seen[value] {
				return nil, reject("PLATFORMS", "unknown or duplicate release platform %s", value)
			}
			seen[value] = true
		}
		sort.Strings(out)
		return out, nil
	}
	a, e := valid(input)
	if e != nil {
		return nil, e
	}
	b, e := valid(expected)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(a, b) {
		return nil, reject("PLATFORMS", "artifact input cannot change the caller's approved platform scope")
	}
	return a, nil
}
