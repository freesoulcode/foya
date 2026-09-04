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
    // 与 Go 的 os.UserConfigDir()/foya/kernel.sock 对齐。
    dirs_config_dir().map(|d| d.join("foya").join("kernel.sock"))
}

/// 跨平台用户配置目录(对齐 Go os.UserConfigDir)。
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

/// 在缓冲区中查找下一个换行符(\n)的位置。
fn find_line_end(buf: &[u8]) -> Option<usize> {
    buf.iter().position(|&b| b == b'\n')
}

type HttpClient = Client<UnixConnector, Full<Bytes>>;

fn client() -> HttpClient {
    Client::builder(TokioExecutor::new()).build(UnixConnector)
}

fn socket_uri(path: &str) -> Result<hyperlocal::Uri, String> {
    let sock = kernel_socket_path().ok_or("无法解析 socket 路径")?;
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

/// 发一个带 body 的 HTTP 请求,读完整响应,返回 body 字符串(用于短请求)。
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
        .map_err(|e| format!("构造请求失败: {e}"))?;

    let resp = client()
        .request(req)
        .await
        .map_err(|e| format!("连接内核失败: {e}"))?;

    let status = resp.status();
    let bytes = resp
        .into_body()
        .collect()
        .await
        .map_err(|e| format!("读取响应失败: {e}"))?
        .to_bytes();
    let text = String::from_utf8_lossy(&bytes).to_string();

    if status.is_success() {
        Ok(text)
    } else {
        Err(format!("内核返回 {}: {}", status.as_u16(), text))
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
        .map_err(|e| format!("构造请求失败: {e}"))?;
    let resp = client()
        .request(req)
        .await
        .map_err(|e| format!("连接内核失败: {e}"))?;
    let status = resp.status();
    let bytes = resp
        .into_body()
        .collect()
        .await
        .map_err(|e| format!("读取响应失败: {e}"))?
        .to_bytes();
    if status.is_success() {
        Ok(bytes.to_vec())
    } else {
        Err(format!(
            "内核返回 {}: {}",
            status.as_u16(),
            String::from_utf8_lossy(&bytes)
        ))
    }
}

/// 订阅某会话的 SSE 事件流,逐条经 Channel 推给前端(每个 data 行一条)。
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
            .map_err(|e| format!("构造请求失败: {e}"))?;

        let resp = client()
            .request(req)
            .await
            .map_err(|e| format!("连接内核失败: {e}"))?;

        if resp.status() != StatusCode::OK {
            return Err(format!("订阅失败: HTTP {}", resp.status().as_u16()));
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

    // 直接从 body 流读取,累积到缓冲区后按行切分 SSE。
    // 每个 SSE data: 行是一条 JSON 事件,经 Channel 推给前端。
    let mut stream = BodyDataStream::new(resp.into_body());
    let mut buf = Vec::new();
    while let Some(chunk) = stream.next().await {
        match chunk {
            Ok(mut data) => {
                buf.extend_from_slice(data.copy_to_bytes(data.remaining()).as_ref());
                while let Some(pos) = find_line_end(&buf) {
                    let line: Vec<u8> = buf.drain(..=pos).collect();
                    // 去掉行尾 \n 及可能的 \r。
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
            Err(e) => return Err(format!("读取事件流失败: {e}")),
        }
    }
    Ok(())
}
