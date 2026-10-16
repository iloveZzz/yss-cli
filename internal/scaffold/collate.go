package scaffold

import "sort"

// The Node generators order names with String.prototype.localeCompare under the
// ICU en-US collation. Names are ASCII, so the root collation is reproduced from
// the primary order of the printable ASCII characters (punctuation, symbols,
// digits, then letters ignoring case) and a tertiary tie-break that puts a
// lower-case letter before its upper-case form.
const collationOrder = "\t\n\v\f\r _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789abcdefghijklmnopqrstuvwxyz"

var primaryWeight = func() map[rune]int {
	weights := map[rune]int{}
	for i, r := range collationOrder {
		weights[r] = i + 1
	}
	for r := 'A'; r <= 'Z'; r++ {
		weights[r] = weights[r+('a'-'A')]
	}
	return weights
}()

// localeCompare returns -1, 0 or 1 like "a".localeCompare("b") in en-US.
func localeCompare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		wa, wb := charWeight(ra[i]), charWeight(rb[i])
		if wa != wb {
			if wa < wb {
				return -1
			}
			return 1
		}
	}
	if len(ra) != len(rb) {
		if len(ra) < len(rb) {
			return -1
		}
		return 1
	}
	for i := range ra {
		if ra[i] == rb[i] {
			continue
		}
		// Same primary weight: lower case sorts before upper case.
		if ra[i] >= 'a' && ra[i] <= 'z' {
			return -1
		}
		if rb[i] >= 'a' && rb[i] <= 'z' {
			return 1
		}
		if ra[i] < rb[i] {
			return -1
		}
		return 1
	}
	return 0
}

func charWeight(r rune) int {
	if w, ok := primaryWeight[r]; ok {
		return w
	}
	return 1000 + int(r)
}

// sortLocale sorts names in place with localeCompare.
func sortLocale(names []string) {
	sort.SliceStable(names, func(i, j int) bool { return localeCompare(names[i], names[j]) < 0 })
}
