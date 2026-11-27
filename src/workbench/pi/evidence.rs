use pulldown_cmark::{Event, Options, Parser, Tag, TagEnd};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    ops::Range,
    sync::{Arc, Mutex},
};

const MAX_SOURCES: usize = 64;
const MAX_OBSERVED_BYTES: usize = 512 * 1024;
const MAX_CITATIONS: usize = 12;
const MAX_LINES: usize = 20;
const MAX_SNIPPET: usize = 4 * 1024;
const MODEL_MARKER: &str = "{{cite:";
const DISPLAY_MARKER: &str = "{{yss-citation:";

#[derive(Clone, Default)]
pub(super) struct Evidence(Arc<Mutex<Registry>>);

#[derive(Default)]
struct Registry {
    sources: HashMap<String, Source>,
    bytes: usize,
}

enum Source {
    Fixed(Value),
    File {
        project_id: String,
        path: String,
        digest: String,
        first_line: usize,
        complete_lines: String,
    },
}

impl Evidence {
    fn register(&self, source: Source, bytes: usize) -> Option<String> {
        let id = crate::fs::unique_id().ok()?;
        let mut registry = self.0.lock().ok()?;
        if registry.sources.len() >= MAX_SOURCES || bytes > MAX_OBSERVED_BYTES - registry.bytes {
            return None;
        }
        registry.sources.insert(id.clone(), source);
        registry.bytes += bytes;
        Some(id)
    }

    pub(super) fn register_excerpt(&self, value: &Value) -> Option<String> {
        let snippet = value["snippet"].as_str()?;
        let project = value["projectId"].as_str()?;
        let path = value["path"].as_str()?;
        let start = value["startLine"].as_u64()?;
        let end = value["endLine"].as_u64()?;
        let run = value["runId"].as_str()?;
        let job = value["jobId"].as_str()?;
        let side = value["side"].as_str()?;
        let digest = value["reviewToken"].as_str()?;
        if project.is_empty()
            || path.is_empty()
            || run.is_empty()
            || job.is_empty()
            || digest.is_empty()
            || start == 0
            || end < start
            || !matches!(side, "initial" | "result")
        {
            return None;
        }
        let line_bytes = snippet
            .split_inclusive('\n')
            .take(MAX_LINES)
            .map(str::len)
            .sum::<usize>();
        let preview = &snippet[..snippet.floor_char_boundary(line_bytes.min(MAX_SNIPPET))];
        let entry = json!({
            "kind": "excerpt", "projectId": project, "path": path,
            "startLine": start, "endLine": end, "snippet": preview, "digest": digest,
            "sourceRunId": run, "sourceJobId": job, "side": side,
            "snippetTruncated": preview.len() != snippet.len(),
        });
        self.register(
            Source::Fixed(entry),
            preview.len() + path.len() + project.len(),
        )
    }

    pub(super) fn register_file(
        &self,
        project_id: &str,
        path: &str,
        digest: &str,
        first_line: usize,
        complete_lines: String,
    ) -> Option<String> {
        if complete_lines.is_empty() || first_line == 0 || project_id.is_empty() || path.is_empty()
        {
            return None;
        }
        let bytes = complete_lines.len() + path.len() + project_id.len();
        self.register(
            Source::File {
                project_id: project_id.into(),
                path: path.into(),
                digest: digest.into(),
                first_line,
                complete_lines,
            },
            bytes,
        )
    }

    pub(super) fn register_command(
        &self,
        activity_id: u64,
        command: &str,
        exit_code: Option<i64>,
    ) -> Option<String> {
        if activity_id == 0 {
            return None;
        }
        let command: String = command.chars().take(240).collect();
        let bytes = command.len();
        self.register(
            Source::Fixed(json!({
                "kind": "command", "activityId": activity_id, "command": command, "exitCode": exit_code,
            })),
            bytes,
        )
    }

    pub(super) fn finalize(&self, text: &str, allow_refs: bool) -> (String, Vec<Value>) {
        if !text.contains(MODEL_MARKER) && !text.contains(DISPLAY_MARKER) {
            return (text.to_owned(), Vec::new());
        }
        let nonce = allow_refs.then(|| crate::fs::unique_id().ok()).flatten();
        let registry = nonce.as_ref().and_then(|_| self.0.lock().ok());
        let mut citations: Vec<Value> = Vec::new();
        let mut rewritten = String::with_capacity(text.len());
        let mut copied = 0;
        for span in prose(text) {
            for (range, reference) in markers(&text[span.clone()]) {
                let start = span.start + range.start;
                let end = span.start + range.end;
                if start < copied || is_escaped(text, start) {
                    continue;
                }
                rewritten.push_str(&text[copied..start]);
                copied = end;
                let Some(value) =
                    reference.and_then(|reference| registry.as_ref()?.resolve(reference))
                else {
                    continue;
                };
                let position = if let Some(position) =
                    citations.iter().position(|entry| entry["source"] == value)
                {
                    position
                } else if citations.len() < MAX_CITATIONS {
                    let number = citations.len() + 1;
                    let marker = format!(
                        "{DISPLAY_MARKER}{}:{number}}}}}",
                        nonce.as_deref().unwrap_or_default()
                    );
                    citations.push(json!({"source": value, "number": number, "marker": marker}));
                    number - 1
                } else {
                    continue;
                };
                rewritten.push_str(citations[position]["marker"].as_str().unwrap_or_default());
            }
        }
        rewritten.push_str(&text[copied..]);
        for citation in &mut citations {
            if let Some(entry) = citation.as_object_mut()
                && let Some(Value::Object(source)) = entry.remove("source")
            {
                entry.extend(source);
            }
        }
        (rewritten, citations)
    }
}

impl Registry {
    fn resolve(&self, reference: &str) -> Option<Value> {
        let (id, range) = reference
            .split_once(':')
            .map_or((reference, None), |(id, range)| (id, Some(range)));
        match (self.sources.get(id)?, range) {
            (Source::Fixed(source), None) => Some(source.clone()),
            (
                Source::File {
                    project_id,
                    path,
                    digest,
                    first_line,
                    complete_lines,
                },
                Some(range),
            ) => {
                let (start, end) = range.split_once('-')?;
                let start: usize = start.parse().ok()?;
                let end: usize = end.parse().ok()?;
                let offset = start.checked_sub(*first_line)?;
                let count = end.checked_sub(start)?.checked_add(1)?;
                if count > MAX_LINES {
                    return None;
                }
                let mut lines = complete_lines.split_inclusive('\n').skip(offset);
                let mut snippet = String::new();
                for _ in 0..count {
                    let line = lines.next()?;
                    if snippet.len() + line.len() > MAX_SNIPPET {
                        return None;
                    }
                    snippet.push_str(line);
                }
                Some(json!({
                    "kind": "file", "projectId": project_id, "path": path, "digest": digest,
                    "startLine": start, "endLine": end, "snippet": snippet,
                }))
            }
            _ => None,
        }
    }
}

fn prose(text: &str) -> impl Iterator<Item = Range<usize>> {
    let mut protected = 0usize;
    Parser::new_ext(text, Options::ENABLE_TABLES)
        .into_offset_iter()
        .filter_map(move |(event, range)| {
            match event {
                Event::Start(Tag::CodeBlock(_) | Tag::Link { .. } | Tag::Image { .. }) => {
                    protected += 1
                }
                Event::End(TagEnd::CodeBlock | TagEnd::Link | TagEnd::Image) => {
                    protected = protected.saturating_sub(1)
                }
                Event::Text(_) if protected == 0 => return Some(range),
                _ => {}
            }
            None
        })
}

fn markers(text: &str) -> impl Iterator<Item = (Range<usize>, Option<&str>)> {
    text.match_indices("{{").filter_map(|(start, _)| {
        let tail = &text[start..];
        let trusted = tail.starts_with(DISPLAY_MARKER);
        let prefix = if trusted {
            DISPLAY_MARKER
        } else {
            MODEL_MARKER
        };
        let value = tail.strip_prefix(prefix)?;
        let close = value
            .as_bytes()
            .windows(2)
            .take(65)
            .position(|pair| pair == b"}}")?;
        let reference = value.get(..close)?;
        if !reference
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b':' | b'-' | b'_'))
        {
            return None;
        }
        let end = start + prefix.len() + reference.len() + 2;
        // Only a marker minted here can become a live citation in the UI.
        Some((start..end, (!trusted).then_some(reference)))
    })
}

fn is_escaped(text: &str, start: usize) -> bool {
    text.as_bytes()[..start]
        .iter()
        .rev()
        .take_while(|byte| **byte == b'\\')
        .count()
        % 2
        != 0
}
