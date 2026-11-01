use crate::error::{ErrorCode, Result, YssError};

#[derive(Clone, Default)]
pub struct Cancellation(tokio_util::sync::CancellationToken);

impl Cancellation {
    pub fn new() -> Self {
        Self::default()
    }
    pub fn cancel(&self) {
        self.0.cancel();
    }
    pub fn is_cancelled(&self) -> bool {
        self.0.is_cancelled()
    }
    pub fn check(&self) -> Result<()> {
        if self.is_cancelled() {
            Err(YssError::new(ErrorCode::Cancelled, "操作已取消"))
        } else {
            Ok(())
        }
    }
    pub async fn cancelled(&self) {
        self.0.cancelled().await;
    }
}

pub fn install_signal_handler(cancel: Cancellation) -> Result<()> {
    ctrlc::set_handler(move || cancel.cancel())
        .map_err(|_| YssError::new(ErrorCode::Io, "无法注册取消信号处理器"))
}
