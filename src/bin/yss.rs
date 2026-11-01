use std::{ffi::OsString, io};
use yss::{
    cancel::{self, Cancellation},
    model::BuildInfo,
    output::{self, OutputEnvelope},
};
const BUNDLE: &[u8] = include_bytes!("../../assets/bundle.tar.gz");
#[used]
#[cfg_attr(target_os = "macos", unsafe(link_section = "__DATA,__yssbuild"))]
#[cfg_attr(target_os = "linux", unsafe(link_section = ".yssbuild"))]
#[cfg_attr(target_os = "windows", unsafe(link_section = ".yssmeta"))]
static BUILD_RECORD: [u8; include_bytes!(concat!(env!("OUT_DIR"), "/build-record.bin")).len()] =
    *include_bytes!(concat!(env!("OUT_DIR"), "/build-record.bin"));
fn main() {
    std::hint::black_box(&BUILD_RECORD);
    let args: Vec<OsString> = std::env::args_os().skip(1).collect();
    let cancel = Cancellation::new();
    if let Err(error) = cancel::install_signal_handler(cancel.clone()) {
        let json = args
            .iter()
            .take_while(|s| *s != "--")
            .fold(false, |value, arg| {
                if arg == "--json" || arg == "--json=true" {
                    true
                } else if arg == "--json=false" {
                    false
                } else {
                    value
                }
            });
        if json {
            let _ = output::write(
                &mut io::stdout(),
                &OutputEnvelope::<()>::error("startup", &error),
            );
        } else {
            eprintln!("{error}");
        }
        std::process::exit(error.exit_code());
    }
    std::process::exit(yss::cli::run(
        args,
        BUNDLE,
        &BuildInfo::current(),
        &cancel,
        &mut io::stdout(),
        &mut io::stderr(),
    ));
}
