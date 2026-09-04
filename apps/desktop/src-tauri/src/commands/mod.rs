#[cfg(unix)]
mod canvas;
#[cfg(unix)]
mod sessions;
#[cfg(unix)]
mod settings;
#[cfg(unix)]
mod terminal;
#[cfg(not(unix))]
mod unsupported;

#[cfg(unix)]
pub(crate) use canvas::*;
#[cfg(unix)]
pub(crate) use sessions::*;
#[cfg(unix)]
pub(crate) use settings::*;
#[cfg(unix)]
pub(crate) use terminal::*;
#[cfg(not(unix))]
pub(crate) use unsupported::*;

#[cfg(unix)]
fn encode_query_component(value: &str) -> String {
    let mut encoded = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.' | b'~') {
            encoded.push(byte as char);
        } else {
            encoded.push_str(&format!("%{byte:02X}"));
        }
    }
    encoded
}
