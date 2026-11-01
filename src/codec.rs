use crate::{
    error::{ErrorCode, Result, YssError},
    model::{MAX_ASSET, MAX_MANIFEST},
};
use saphyr_parser::{Event, Parser, ScalarStyle};
use serde::{
    Serialize,
    de::{self, DeserializeOwned, DeserializeSeed, MapAccess, SeqAccess, Visitor},
};
use serde_json::{Map, Value};
use std::{collections::BTreeSet, fmt, str::FromStr};

pub type StrictValue = Value;

#[derive(Clone, Copy)]
pub struct Limits {
    pub bytes: u64,
    pub depth: usize,
    pub nodes: usize,
    pub number_bytes: usize,
}
impl Limits {
    pub fn manifest() -> Self {
        Self {
            bytes: MAX_MANIFEST,
            depth: 128,
            nodes: 1_000_000,
            number_bytes: 4096,
        }
    }
    pub fn asset() -> Self {
        Self {
            bytes: MAX_ASSET,
            ..Self::manifest()
        }
    }
    pub fn yaml() -> Self {
        Self {
            depth: 512,
            ..Self::asset()
        }
    }
}

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
fn input(bytes: &[u8], limits: Limits) -> Result<&str> {
    if bytes.len() as u64 > limits.bytes.min(MAX_ASSET) {
        return Err(invalid("输入超过大小限制"));
    }
    let text = std::str::from_utf8(bytes).map_err(|_| invalid("输入不是合法 UTF-8"))?;
    if text.trim().is_empty() {
        return Err(invalid("输入为空"));
    }
    Ok(text)
}

// Bound numeric allocation before Serde's arbitrary-precision number parser runs.
fn number_lengths(text: &str, limit: usize) -> Result<()> {
    let (mut quoted, mut escaped, mut length) = (false, false, 0usize);
    for b in text.bytes() {
        if quoted {
            if escaped {
                escaped = false;
            } else if b == b'\\' {
                escaped = true;
            } else if b == b'"' {
                quoted = false;
            }
        } else if b == b'"' {
            quoted = true;
            length = 0;
        } else if b.is_ascii_digit() || matches!(b, b'-' | b'+' | b'.' | b'e' | b'E') {
            length += 1;
            if length > limit {
                return Err(invalid("数值 token 超过长度限制"));
            }
        } else {
            length = 0;
        }
    }
    Ok(())
}
struct Check<'a> {
    limits: Limits,
    nodes: &'a mut usize,
    depth: usize,
}
impl Check<'_> {
    fn child(&mut self) -> Check<'_> {
        Check {
            limits: self.limits,
            nodes: self.nodes,
            depth: self.depth + 1,
        }
    }
}
impl<'de> DeserializeSeed<'de> for Check<'_> {
    type Value = ();
    fn deserialize<D: de::Deserializer<'de>>(self, d: D) -> std::result::Result<(), D::Error> {
        if *self.nodes >= self.limits.nodes || self.depth > self.limits.depth {
            return Err(de::Error::custom("resource limit"));
        }
        *self.nodes += 1;
        d.deserialize_any(self)
    }
}
impl<'de> Visitor<'de> for Check<'_> {
    type Value = ();
    fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("bounded JSON")
    }
    fn visit_unit<E: de::Error>(self) -> std::result::Result<(), E> {
        Ok(())
    }
    fn visit_bool<E: de::Error>(self, _: bool) -> std::result::Result<(), E> {
        Ok(())
    }
    fn visit_i64<E: de::Error>(self, _: i64) -> std::result::Result<(), E> {
        Ok(())
    }
    fn visit_u64<E: de::Error>(self, _: u64) -> std::result::Result<(), E> {
        Ok(())
    }
    fn visit_f64<E: de::Error>(self, _: f64) -> std::result::Result<(), E> {
        Ok(())
    }
    fn visit_str<E: de::Error>(self, _: &str) -> std::result::Result<(), E> {
        Ok(())
    }
    fn visit_map<A: MapAccess<'de>>(mut self, mut map: A) -> std::result::Result<(), A::Error> {
        let mut keys = BTreeSet::new();
        while let Some(key) = map.next_key::<String>()? {
            if *self.nodes >= self.limits.nodes {
                return Err(de::Error::custom("node limit"));
            }
            *self.nodes += 1;
            if !keys.insert(key) {
                return Err(de::Error::custom("duplicate key"));
            }
            map.next_value_seed(self.child())?;
        }
        Ok(())
    }
    fn visit_seq<A: SeqAccess<'de>>(mut self, mut seq: A) -> std::result::Result<(), A::Error> {
        while seq.next_element_seed(self.child())?.is_some() {}
        Ok(())
    }
}

pub fn decode_json<T: DeserializeOwned>(bytes: &[u8], limits: Limits) -> Result<T> {
    let text = input(bytes, limits)?;
    number_lengths(text, limits.number_bytes)?;
    let mut decoder = serde_json::Deserializer::from_slice(bytes);
    Check {
        limits: Limits {
            depth: limits.depth.min(128),
            ..limits
        },
        nodes: &mut 0,
        depth: 0,
    }
    .deserialize(&mut decoder)
    .map_err(|e| {
        invalid("JSON 语法、重复键或资源限制不合法")
            .context("line", e.line())
            .context("column", e.column())
    })?;
    decoder.end().map_err(|_| invalid("JSON 包含尾随内容"))?;
    let mut decoder = serde_json::Deserializer::from_slice(bytes);
    serde_path_to_error::deserialize(&mut decoder)
        .map_err(|e| invalid("字段类型、范围或名称不符合格式").at(e.path()))
}

pub fn encode_json<T: Serialize>(value: &T) -> Result<Vec<u8>> {
    let value = serde_json::to_value(value).map_err(|_| invalid("无法编码 JSON"))?;
    let mut out = serde_json::to_vec(&value).map_err(|_| invalid("无法编码 JSON"))?;
    out.push(b'\n');
    Ok(out)
}

pub fn ensure_format(format: &str, version: u32, expected: &str) -> Result<()> {
    if format != expected || version != 1 {
        Err(YssError::new(
            ErrorCode::UnsupportedFormat,
            "不支持的格式身份或版本",
        ))
    } else {
        Ok(())
    }
}

enum Frame {
    Sequence(Vec<Value>),
    Mapping(Map<String, Value>, Option<String>),
}
fn append(stack: &mut [Frame], root: &mut Option<Value>, value: Value) -> Result<()> {
    match stack.last_mut() {
        Some(Frame::Sequence(values)) => values.push(value),
        Some(Frame::Mapping(values, key)) => {
            if let Some(key) = key.take() {
                values.insert(key, value);
            } else if let Value::String(name) = value {
                if name == "<<" || values.contains_key(&name) {
                    return Err(invalid("YAML 包含重复键或 merge key"));
                }
                *key = Some(name);
            } else {
                return Err(invalid("YAML 映射键必须是字符串"));
            }
        }
        None => {
            if root.replace(value).is_some() {
                return Err(invalid("YAML 包含多个根值"));
            }
        }
    }
    Ok(())
}

pub fn decode_yaml(bytes: &[u8], limits: Limits) -> Result<StrictValue> {
    let text = input(bytes, limits)?;
    let mut stack: Vec<Frame> = Vec::new();
    let (mut root, mut documents, mut nodes) = (None, 0, 0usize);
    for event in Parser::new_from_str(text) {
        let (event, _) = event.map_err(|_| invalid("YAML 语法不合法"))?;
        match event {
            Event::DocumentStart(_) => {
                documents += 1;
                if documents > 1 {
                    return Err(invalid("YAML 只允许单个文档"));
                }
            }
            Event::Alias(_) => return Err(invalid("YAML 不允许别名")),
            Event::Scalar(value, style, anchor, tag) => {
                if anchor != 0 || tag.is_some() {
                    return Err(invalid("YAML 不允许锚点或显式标签"));
                }
                nodes += 1;
                if nodes > limits.nodes {
                    return Err(invalid("YAML 节点过多"));
                }
                let value = if style == ScalarStyle::Plain {
                    scalar(&value, limits.number_bytes)?
                } else {
                    Value::String(value.into_owned())
                };
                append(&mut stack, &mut root, value)?;
            }
            Event::SequenceStart(anchor, ref tag) | Event::MappingStart(anchor, ref tag) => {
                if anchor != 0 || tag.is_some() {
                    return Err(invalid("YAML 不允许锚点或显式标签"));
                }
                if matches!(stack.last(), Some(Frame::Mapping(_, None))) {
                    return Err(invalid("YAML 映射键必须是字符串"));
                }
                nodes += 1;
                if nodes > limits.nodes || stack.len() >= limits.depth.min(512) {
                    return Err(invalid("YAML 节点或深度超过限制"));
                }
                // The consumed event's variant still determines the container kind.
                let sequence = matches!(event, Event::SequenceStart(..));
                stack.push(if sequence {
                    Frame::Sequence(Vec::new())
                } else {
                    Frame::Mapping(Map::new(), None)
                });
            }
            Event::SequenceEnd | Event::MappingEnd => {
                let value = match stack.pop() {
                    Some(Frame::Sequence(values)) => Value::Array(values),
                    Some(Frame::Mapping(values, None)) => Value::Object(values),
                    _ => return Err(invalid("YAML 容器不完整")),
                };
                append(&mut stack, &mut root, value)?;
            }
            _ => {}
        }
    }
    if documents != 1 || !stack.is_empty() {
        return Err(invalid("YAML 文档不完整"));
    }
    root.ok_or_else(|| invalid("YAML 文档为空"))
}

fn scalar(text: &str, limit: usize) -> Result<Value> {
    match text {
        "" | "~" | "null" | "Null" | "NULL" => return Ok(Value::Null),
        "true" | "True" | "TRUE" => return Ok(Value::Bool(true)),
        "false" | "False" | "FALSE" => return Ok(Value::Bool(false)),
        _ => {}
    }
    if matches!(
        text.to_ascii_lowercase().as_str(),
        ".nan" | ".inf" | "+.inf" | "-.inf"
    ) {
        return Err(invalid("YAML 不允许非有限数值"));
    }
    for (prefix, base) in [("0x", 16), ("0o", 8)] {
        if let Some(digits) = text.strip_prefix(prefix)
            && !digits.is_empty()
            && digits.chars().all(|c| c.is_digit(base))
        {
            if text.len() > limit {
                return Err(invalid("YAML 数值超过长度限制"));
            }
            let mut decimal = vec![0u32];
            for digit in digits.chars() {
                let mut carry = digit.to_digit(base).unwrap();
                for value in &mut decimal {
                    carry += *value * base;
                    *value = carry % 10;
                    carry /= 10;
                }
                while carry != 0 {
                    decimal.push(carry % 10);
                    carry /= 10;
                }
                if decimal.len() > limit {
                    return Err(invalid("YAML 数值超过长度限制"));
                }
            }
            let number: String = decimal
                .iter()
                .rev()
                .map(|&n| (b'0' + n as u8) as char)
                .collect();
            return serde_json::Number::from_str(&number)
                .map(Value::Number)
                .map_err(|_| invalid("YAML 数值不合法"));
        }
    }
    let signed = text.strip_prefix('+').unwrap_or(text);
    let (sign, unsigned) = signed.strip_prefix('-').map_or(("", signed), |s| ("-", s));
    let (mantissa, exponent) = unsigned
        .find(['e', 'E'])
        .map_or((unsigned, ""), |i| (&unsigned[..i], &unsigned[i..]));
    let (whole, fraction) = mantissa
        .split_once('.')
        .map_or((mantissa, None), |(a, b)| (a, Some(b)));
    let exp_digits = exponent
        .get(1..)
        .unwrap_or("")
        .trim_start_matches(['+', '-']);
    let numeric = (!whole.is_empty() || fraction.is_some_and(|s| !s.is_empty()))
        && whole.bytes().all(|c| c.is_ascii_digit())
        && fraction.is_none_or(|s| s.bytes().all(|c| c.is_ascii_digit()))
        && (exponent.is_empty()
            || (!exp_digits.is_empty()
                && exp_digits.bytes().all(|c| c.is_ascii_digit())
                && !exponent[1..].contains("++")
                && !exponent[1..].contains("--")
                && !exponent[1..].contains("+-")
                && !exponent[1..].contains("-+")));
    if !numeric {
        return Ok(Value::String(text.into()));
    }
    if text.len() > limit {
        return Err(invalid("YAML 数值超过长度限制"));
    }
    let whole = whole.trim_start_matches('0');
    let mut number = format!("{sign}{}", if whole.is_empty() { "0" } else { whole });
    if let Some(fraction) = fraction {
        number.push('.');
        number.push_str(if fraction.is_empty() { "0" } else { fraction });
    }
    number.push_str(exponent);
    serde_json::Number::from_str(&number)
        .map(Value::Number)
        .map_err(|_| invalid("YAML 数值不合法"))
}
