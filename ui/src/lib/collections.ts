/** Keep copy-on-sort semantics when a system WebView predates toSorted. */
export function sorted<T>(values: readonly T[], compare?: (left: T, right: T) => number): T[] {
  return typeof values.toSorted === 'function' ? values.toSorted(compare) : [...values].sort(compare)
}
