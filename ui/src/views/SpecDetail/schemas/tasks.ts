export function parseTaskTitles(text: string): string[] {
  return text.split(/\r?\n/).map(title => title.trim()).filter(Boolean)
}
