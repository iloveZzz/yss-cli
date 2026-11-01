use clap::{Parser, Subcommand};
use std::{io::Write, path::PathBuf};
use yss::{
    error::{ErrorCode, Result, YssError},
    updater::release,
};
#[derive(Parser)]
#[command(
    about = "构建真实来源 Bundle 或 Rust 发行包",
    subcommand_negates_reqs = true,
    args_conflicts_with_subcommands = true
)]
struct Args {
    #[arg(long, required = true)]
    source_root: Option<PathBuf>,
    #[arg(long, required = true)]
    out: Option<PathBuf>,
    #[command(subcommand)]
    command: Option<Command>,
}
#[derive(Subcommand)]
enum Command {
    Package {
        #[arg(long)]
        binary: PathBuf,
        #[arg(long)]
        version: String,
        #[arg(long)]
        target: String,
        #[arg(long)]
        out: PathBuf,
    },
}
fn run(args: Args) -> Result<Vec<u8>> {
    match args.command {
        Some(Command::Package {
            binary,
            version,
            target,
            out,
        }) => {
            let info = release::read_binary_info(&binary)?;
            if release::version(&version, false)?.to_string() != info.version
                || target != info.target
            {
                return Err(YssError::new(
                    ErrorCode::InvalidInput,
                    "--version/--target 与实际程序不一致",
                ));
            }
            yss::codec::encode_json(&release::package(&binary, &info, &out)?)
        }
        None => yss::codec::encode_json(&yss::bundle::build(
            &args
                .source_root
                .ok_or_else(|| YssError::new(ErrorCode::Argument, "缺少 --source-root"))?,
            &args
                .out
                .ok_or_else(|| YssError::new(ErrorCode::Argument, "缺少 --out"))?,
        )?),
    }
}
fn main() {
    let result = run(Args::parse())
        .and_then(|bytes| std::io::stdout().write_all(&bytes).map_err(Into::into));
    if let Err(error) = result {
        eprintln!(
            "{}",
            serde_json::to_string(&error).unwrap_or_else(|_| "构建错误".into())
        );
        std::process::exit(error.exit_code());
    }
}
