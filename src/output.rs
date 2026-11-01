use crate::error::{Result, YssError};
use serde::Serialize;
use std::io::Write;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OutputEnvelope<'a, T: Serialize> {
    pub format: &'static str,
    pub format_version: u8,
    pub command: &'a str,
    pub status: &'static str,
    pub result: Option<T>,
    pub error: Option<&'a YssError>,
}
impl<'a, T: Serialize> OutputEnvelope<'a, T> {
    pub fn ok(command: &'a str, result: T) -> Self {
        Self {
            format: "yss.output",
            format_version: 1,
            command,
            status: "ok",
            result: Some(result),
            error: None,
        }
    }
    pub fn error(command: &'a str, error: &'a YssError) -> Self {
        Self {
            format: "yss.output",
            format_version: 1,
            command,
            status: "error",
            result: None,
            error: Some(error),
        }
    }
}
pub fn write<T: Serialize>(out: &mut dyn Write, envelope: &OutputEnvelope<'_, T>) -> Result<()> {
    let mut bytes = serde_json::to_vec(envelope)
        .map_err(|_| YssError::new(crate::error::ErrorCode::InvalidInput, "输出编码失败"))?;
    bytes.push(b'\n');
    out.write_all(&bytes)?;
    Ok(())
}
