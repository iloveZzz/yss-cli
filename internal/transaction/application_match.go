package transaction

import "slices"

// CommittedApplicationMatches is a readonly proof for resuming an exact saved
// initialization. Matching current bytes alone cannot prove who applied them.
func CommittedApplicationMatches(root, kind string, ops []Operation) (bool, error) {
	root, err := safeRoot(root, false)
	if err != nil {
		return false, err
	}
	expected, err := normalize(root, ops)
	if err != nil {
		return false, err
	}
	all, _, err := scan(root)
	if err != nil {
		return false, err
	}
	for _, archive := range all {
		if archive.plan.Kind == kind && archive.journal.Phase == "committed" && slices.Equal(archive.plan.Operations, expected) {
			return true, nil
		}
	}
	return false, nil
}
