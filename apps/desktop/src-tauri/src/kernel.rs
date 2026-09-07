#![cfg(unix)]

use std::path::{Path, PathBuf};

use bytes::{Buf, Bytes};
use futures_util::StreamExt;
use http_body_util::{BodyDataStream, BodyExt, Full};
use hyper::{Method, Request, StatusCode};
use hyper_util::client::legacy::Client;
use hyper_util::rt::TokioExecutor;
use hyperlocal::{UnixConnector, Uri as HyperlocalUri};
use tauri::ipc::Channel;
use tokio::sync::oneshot;

fn kernel_socket_path() -> Option<PathBuf> {
    // Keep this aligned with Go's os.UserConfigDir()/foya/kernel.sock.
    dirs_config_dir().map(|d| d.join("foya").join("kernel.sock"))
}

/// Return the platform configuration directory used by Go's os.UserConfigDir.
#[cfg(target_os = "macos")]
fn dirs_config_dir() -> Option<PathBuf> {
    std::env::var_os("HOME").map(|h| PathBuf::from(h).join("Library").join("Application Support"))
}

#[cfg(target_os = "linux")]
fn dirs_config_dir() -> Option<PathBuf> {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")))
}

#[cfg(target_os = "windows")]
fn dirs_config_dir() -> Option<PathBuf> {
    std::env::var_os("APPDATA").map(PathBuf::from)
}

/// Find the next newline in a byte buffer.
fn find_line_end(buf: &[u8]) -> Option<usize> {
    buf.iter().position(|&b| b == b'\n')
}

type HttpClient = Client<UnixConnector, Full<Bytes>>;

fn client() -> HttpClient {
    Client::builder(TokioExecutor::new()).build(UnixConnector)
}

fn socket_uri(path: &str) -> Result<hyperlocal::Uri, String> {
    let sock = kernel_socket_path().ok_or("Unable to resolve kernel socket path")?;
    Ok(HyperlocalUri::new(Path::new(&sock), path))
}

fn method_from_str(m: &str) -> Method {
    match m {
        "POST" => Method::POST,
        "PUT" => Method::PUT,
        "PATCH" => Method::PATCH,
        "DELETE" => Method::DELETE,
        _ => Method::GET,
    }
}

/// Send an HTTP request and return its complete response body.
pub async fn request(method: &str, path: &str, body: Option<&str>) -> Result<String, String> {
    let uri = socket_uri(path)?;
    let mut builder = Request::builder().method(method_from_str(method)).uri(uri);
    let body_bytes: Bytes = match body {
        Some(b) if !b.is_empty() => {
            builder = builder.header("content-type", "application/json");
            Bytes::copy_from_slice(b.as_bytes())
        }
        _ => Bytes::new(),
    };
    let req = builder
        .body(Full::new(body_bytes))
        .map_err(|e| format!("Failed to build request: {e}"))?;

    let resp = client()
        .request(req)
        .await
        .map_err(|e| format!("Failed to connect to kernel: {e}"))?;

    let status = resp.status();
    let bytes = resp
        .into_body()
        .collect()
        .await
        .map_err(|e| format!("Failed to read response: {e}"))?
        .to_bytes();
    let text = String::from_utf8_lossy(&bytes).to_string();

    if status.is_success() {
        Ok(text)
    } else {
        Err(text)
    }
}

pub async fn request_bytes(
    method: &str,
    path: &str,
    content_type: Option<&str>,
    body: Vec<u8>,
) -> Result<Vec<u8>, String> {
    let uri = socket_uri(path)?;
    let mut builder = Request::builder().method(method_from_str(method)).uri(uri);
    if let Some(content_type) = content_type {
        builder = builder.header("content-type", content_type);
    }
    let req = builder
        .body(Full::new(Bytes::from(body)))
        .map_err(|e| format!("Failed to build request: {e}"))?;
    let resp = client()
        .request(req)
        .await
        .map_err(|e| format!("Failed to connect to kernel: {e}"))?;
    let status = resp.status();
    let bytes = resp
        .into_body()
        .collect()
        .await
        .map_err(|e| format!("Failed to read response: {e}"))?
        .to_bytes();
    if status.is_success() {
        Ok(bytes.to_vec())
    } else {
        Err(String::from_utf8_lossy(&bytes).to_string())
    }
}

/// Subscribe to a chat SSE stream and forward each data frame to the renderer.
pub async fn subscribe(
    session_id: &str,
    channel: Channel<String>,
    ready: oneshot::Sender<Result<(), String>>,
) -> Result<(), String> {
    subscribe_path(&format!("/sessions/{session_id}/events"), channel, ready).await
}

/// Subscribe to an SSE endpoint and forward each data frame to the renderer.
pub async fn subscribe_path(
    path: &str,
    channel: Channel<String>,
    ready: oneshot::Sender<Result<(), String>>,
) -> Result<(), String> {
    let setup = async {
        let uri = socket_uri(path)?;
        let req = Request::builder()
            .method(Method::GET)
            .uri(uri)
            .header("accept", "text/event-stream")
            .body(Full::new(Bytes::new()))
            .map_err(|e| format!("Failed to build request: {e}"))?;

        let resp = client()
            .request(req)
            .await
            .map_err(|e| format!("Failed to connect to kernel: {e}"))?;

        if resp.status() != StatusCode::OK {
            return Err(format!(
                "Subscription failed: HTTP {}",
                resp.status().as_u16()
            ));
        }
        Ok(resp)
    }
    .await;

    let resp = match setup {
        Ok(resp) => {
            let _ = ready.send(Ok(()));
            resp
        }
        Err(error) => {
            let _ = ready.send(Err(error.clone()));
            return Err(error);
        }
    };

    // Read the response body directly and split buffered SSE frames by line.
    let mut stream = BodyDataStream::new(resp.into_body());
    let mut buf = Vec::new();
    while let Some(chunk) = stream.next().await {
        match chunk {
            Ok(mut data) => {
                buf.extend_from_slice(data.copy_to_bytes(data.remaining()).as_ref());
                while let Some(pos) = find_line_end(&buf) {
                    let line: Vec<u8> = buf.drain(..=pos).collect();
                    // Remove trailing newline characters.
                    let end = line
                        .iter()
                        .rposition(|&b| b != b'\n' && b != b'\r')
                        .map(|p| p + 1)
                        .unwrap_or(0);
                    if let Ok(text) = std::str::from_utf8(&line[..end]) {
                        if let Some(payload) = text.strip_prefix("data: ") {
                            let _ = channel.send(payload.to_string());
                        }
                    }
                }
            }
            Err(e) => return Err(format!("Failed to read event stream: {e}")),
        }
    }
    Ok(())
}
