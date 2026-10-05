package updater

import (
	"math/big"
	"regexp"
	"strings"
)

var semanticVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func versionParts(version string) ([]string, error) {
	m := semanticVersion.FindStringSubmatch(version)
	if m == nil {
		return nil, fail("VERSION", "程序版本不符合 SemVer: "+version)
	}
	for _, v := range strings.Split(m[4], ".") {
		if len(v) > 1 && v[0] == '0' && numeric(v) {
			return nil, fail("VERSION", "预发布数字版本不能含前导零")
		}
	}
	return m[1:5], nil
}
func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func compareNumber(a, b string) int {
	x, _ := new(big.Int).SetString(a, 10)
	y, _ := new(big.Int).SetString(b, 10)
	return x.Cmp(y)
}
func compareVersions(a, b string) (int, error) {
	x, e := versionParts(a)
	if e != nil {
		return 0, e
	}
	y, e := versionParts(b)
	if e != nil {
		return 0, e
	}
	for i := 0; i < 3; i++ {
		if n := compareNumber(x[i], y[i]); n != 0 {
			return n, nil
		}
	}
	if x[3] == y[3] {
		return 0, nil
	}
	if x[3] == "" {
		return 1, nil
	}
	if y[3] == "" {
		return -1, nil
	}
	xn, yn := strings.Split(x[3], "."), strings.Split(y[3], ".")
	for i := 0; i < len(xn) && i < len(yn); i++ {
		if xn[i] == yn[i] {
			continue
		}
		a, b := numeric(xn[i]), numeric(yn[i])
		if a && b {
			return compareNumber(xn[i], yn[i]), nil
		}
		if a {
			return -1, nil
		}
		if b {
			return 1, nil
		}
		if xn[i] < yn[i] {
			return -1, nil
		}
		return 1, nil
	}
	if len(xn) < len(yn) {
		return -1, nil
	}
	return 1, nil
}
