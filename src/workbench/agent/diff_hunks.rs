use super::{DiffFile, WorkspaceMode, invalid};
use crate::error::Result;
use serde::Serialize;
use serde_json::{Value, json};

const MAX_COMPARE_CELLS: usize = 2_000_000;
const MAX_SIDE_HUNKS: usize = 16;
const MAX_SIDE_LINES: usize = 120;
const MAX_SIDE_BYTES: usize = 24 * 1024;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Hunk<'a> {
    side: &'static str,
    old_start: usize,
    new_start: usize,
    old_lines: &'a [&'a str],
    new_lines: &'a [&'a str],
}

pub(super) fn file_preview(
    mut file: DiffFile,
    workspace_mode: WorkspaceMode,
    historical: bool,
    full: bool,
) -> Result<Value> {
    let result = if file.result.is_none() {
        Some("")
    } else {
        file.result_text.as_deref()
    };
    let mut hunks = Vec::new();
    let mut truncated = false;
    if !file.binary {
        let sides = [
            (
                "initial",
                file.initial.is_none(),
                file.initial_text.as_deref(),
            ),
            (
                "current",
                file.current.is_none(),
                file.current_text.as_deref(),
            ),
        ];
        let count = if workspace_mode == WorkspaceMode::Isolated && !historical {
            2
        } else {
            1
        };
        for (side, missing, old) in sides.into_iter().take(count) {
            let old = if missing { Some("") } else { old };
            if let (Some(old), Some(new)) = (old, result) {
                truncated |= append_hunks(side, old, new, &mut hunks);
            } else {
                truncated = true;
            }
        }
    }
    if !full {
        file.initial_text = None;
        file.result_text = None;
        file.current_text = None;
        file.reviewed_before = None;
    }
    let mut value = serde_json::to_value(file).map_err(|_| invalid("文件差异编码失败"))?;
    if !full {
        remove_text(&mut value);
    }
    value["hunks"] = json!(hunks);
    value["hunksTruncated"] = json!(truncated);
    Ok(value)
}

pub(super) fn remove_text(file: &mut Value) {
    let fields = file.as_object_mut().expect("DiffFile is an object");
    for field in ["initialText", "resultText", "currentText", "reviewedBefore"] {
        fields.remove(field);
    }
}

fn append_hunks(side: &'static str, old: &str, new: &str, hunks: &mut Vec<Value>) -> bool {
    let old: Vec<_> = old.lines().collect();
    let new: Vec<_> = new.lines().collect();
    let prefix = old.iter().zip(&new).take_while(|(a, b)| a == b).count();
    let old = &old[prefix..];
    let new = &new[prefix..];
    let suffix = old
        .iter()
        .rev()
        .zip(new.iter().rev())
        .take_while(|(a, b)| a == b)
        .count();
    let old = &old[..old.len() - suffix];
    let new = &new[..new.len() - suffix];
    if old.is_empty() && new.is_empty() {
        return false;
    }
    let columns = new.len() + 1;
    let cells = (old.len() + 1).saturating_mul(columns);
    if cells > MAX_COMPARE_CELLS {
        return true;
    }
    // Longest common suffix lengths identify exact line edit spans within the bounded matrix.
    let mut lengths = vec![0_u32; cells];
    for (i, left) in old.iter().enumerate().rev() {
        for (j, right) in new.iter().enumerate().rev() {
            let cell = i * columns + j;
            lengths[cell] = if left == right {
                lengths[cell + columns + 1] + 1
            } else {
                lengths[cell + columns].max(lengths[cell + 1])
            };
        }
    }
    let start_count = hunks.len();
    let (mut lines_left, mut bytes_left) = (MAX_SIDE_LINES, MAX_SIDE_BYTES);
    let (mut i, mut j) = (0, 0);
    while i < old.len() || j < new.len() {
        if old.get(i).zip(new.get(j)).is_some_and(|(a, b)| a == b) {
            i += 1;
            j += 1;
            continue;
        }
        if hunks.len() - start_count == MAX_SIDE_HUNKS {
            return true;
        }
        let (old_start, new_start) = (i, j);
        loop {
            if i < old.len()
                && (j == new.len()
                    || lengths[(i + 1) * columns + j] >= lengths[i * columns + j + 1])
            {
                i += 1;
            } else {
                j += 1;
            }
            if (i == old.len() && j == new.len())
                || old.get(i).zip(new.get(j)).is_some_and(|(a, b)| a == b)
            {
                break;
            }
        }
        let old_lines = &old[old_start..i];
        let new_lines = &new[new_start..j];
        let lines = old_lines.len() + new_lines.len();
        let bytes: usize = old_lines
            .iter()
            .chain(new_lines)
            .map(|line| line.len())
            .sum();
        if lines > lines_left || bytes > bytes_left {
            return true;
        }
        lines_left -= lines;
        bytes_left -= bytes;
        hunks.push(json!(Hunk {
            side,
            old_start: prefix + old_start + 1,
            new_start: prefix + new_start + 1,
            old_lines,
            new_lines,
        }));
    }
    false
}
