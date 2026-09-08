#![cfg(unix)]

use std::fs;
use std::io::{Read, Write};
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::OnceLock;
use std::time::Duration;

use bytes::{Buf, Bytes};
use futures_util::StreamExt;
use http_body_util::{BodyDataStream, BodyExt, Full};
use hyper::{Method, Request, StatusCode};
use hyper_util::client::legacy::Client;
use hyper_util::rt::TokioExecutor;
use hyperlocal::{UnixConnector, Uri as HyperlocalUri};
use serde::{Deserialize, Serialize};
use tauri::ipc::Channel;
use tauri::{AppHandle, Manager};
use tokio::sync::oneshot;
use url::{Host, Url};

use crate::ssh_connection::{self, SshConnection, SshHostInput};

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

#[derive(Clone, Debug, Deserialize, Serialize)]
struct StoredKernelConnection {
    mode: String,
    #[serde(default)]
    url: String,
    #[serde(default)]
    token: String,
    #[serde(default)]
    ssh: Option<SshConnection>,
}

impl Default for StoredKernelConnection {
    fn default() -> Self {
        Self {
            mode: "local".into(),
            url: String::new(),
            token: String::new(),
            ssh: None,
        }
    }
}

#[derive(Deserialize)]
pub(crate) struct KernelConnectionInput {
    mode: String,
    #[serde(default)]
    url: String,
    #[serde(default)]
    token: String,
}

#[derive(Serialize)]
struct PublicKernelConnection {
    mode: String,
    url: String,
    has_token: bool,
    ssh: Option<SshConnection>,
}

static ACTIVE_CONNECTION: OnceLock<StoredKernelConnection> = OnceLock::new();
static REMOTE_CLIENT: OnceLock<Result<reqwest::Client, String>> = OnceLock::new();

pub(crate) fn initialize(app: &AppHandle) -> Result<bool, String> {
    let mut config = load_kernel_connection(app).unwrap_or_else(|error| {
        eprintln!("[foya-kernel] {error}; falling back to local mode");
        StoredKernelConnection::default()
    });
    if config.mode == "ssh" {
        if let Some(connection) = config.ssh.as_ref() {
            match ssh_connection::deploy_and_connect(app, connection, &config.token) {
                Ok((url, _)) => config.url = url,
                Err(error) => {
                    eprintln!("[foya-kernel] SSH connection failed: {error}");
                    config.url = "http://127.0.0.1:1".into();
                }
            }
        }
    }
    let use_local = config.mode == "local";
    ACTIVE_CONNECTION
        .set(config)
        .map_err(|_| "Kernel connection is already initialized".to_string())?;
    Ok(use_local)
}

fn active_connection() -> StoredKernelConnection {
    ACTIVE_CONNECTION.get().cloned().unwrap_or_default()
}

fn connection_path(app: &AppHandle) -> Result<PathBuf, String> {
    app.path()
        .app_config_dir()
        .map(|path| path.join("kernel-connection.json"))
        .map_err(|error| format!("Unable to resolve app configuration directory: {error}"))
}

fn load_kernel_connection(app: &AppHandle) -> Result<StoredKernelConnection, String> {
    let path = connection_path(app)?;
    let data = match fs::read(&path) {
        Ok(data) => data,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(StoredKernelConnection::default())
        }
        Err(error) => return Err(format!("Failed to read kernel connection: {error}")),
    };
    let config: StoredKernelConnection = serde_json::from_slice(&data)
        .map_err(|error| format!("Failed to parse kernel connection: {error}"))?;
    validate_kernel_connection(config)
}

fn resolve_kernel_connection(
    app: &AppHandle,
    input: KernelConnectionInput,
) -> Result<StoredKernelConnection, String> {
    let existing = load_kernel_connection(app).unwrap_or_default();
    let same_remote = existing.mode == "remote"
        && input.url.trim().trim_end_matches('/') == existing.url.trim_end_matches('/');
    let token = if input.token.trim().is_empty() && same_remote {
        existing.token
    } else {
        input.token
    };
    validate_kernel_connection(StoredKernelConnection {
        mode: input.mode,
        url: input.url,
        token,
        ssh: None,
    })
}

fn validate_kernel_connection(
    mut config: StoredKernelConnection,
) -> Result<StoredKernelConnection, String> {
    config.mode = config.mode.trim().to_lowercase();
    if config.mode == "local" {
        return Ok(StoredKernelConnection::default());
    }
    if config.mode == "ssh" {
        if config.token.trim().len() < 32 {
            return Err("SSH kernel token must contain at least 32 characters".into());
        }
        let connection = config
            .ssh
            .take()
            .ok_or_else(|| "SSH kernel configuration is missing".to_string())?;
        let normalized = ssh_connection::normalize_connection(SshHostInput {
            name: connection.name,
            target: connection.target,
            port: connection.port,
        })?;
        config.token = config.token.trim().to_string();
        config.url.clear();
        config.ssh = Some(SshConnection {
            remote_port: if connection.remote_port == 0 {
                normalized.remote_port
            } else {
                connection.remote_port
            },
            ..normalized
        });
        return Ok(config);
    }
    if config.mode != "remote" {
        return Err("Kernel connection mode must be local, SSH, or remote".into());
    }
    if config.token.trim().len() < 32 {
        return Err("Remote kernel token must contain at least 32 characters".into());
    }
    config.token = config.token.trim().to_string();

    let mut url = Url::parse(config.url.trim())
        .map_err(|error| format!("Invalid remote kernel URL: {error}"))?;
    if url.username() != ""
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(
            "Remote kernel URL must not contain credentials, a query, or a fragment".into(),
        );
    }
    let secure = url.scheme() == "https";
    let loopback_http = url.scheme() == "http"
        && match url.host() {
            Some(Host::Domain(host)) => host.eq_ignore_ascii_case("localhost"),
            Some(Host::Ipv4(address)) => address.is_loopback(),
            Some(Host::Ipv6(address)) => address.is_loopback(),
            None => false,
        };
    if !secure && !loopback_http {
        return Err(
            "Remote kernel URL must use HTTPS; HTTP is allowed only for loopback addresses".into(),
        );
    }
    if !url.path().ends_with('/') {
        let path = format!("{}/", url.path());
        url.set_path(&path);
    }
    config.url = url.to_string().trim_end_matches('/').to_string();
    config.ssh = None;
    Ok(config)
}

fn save_kernel_connection(app: &AppHandle, config: &StoredKernelConnection) -> Result<(), String> {
    let path = connection_path(app)?;
    let parent = path
        .parent()
        .ok_or_else(|| "Kernel connection path has no parent directory".to_string())?;
    fs::create_dir_all(parent)
        .map_err(|error| format!("Failed to create app configuration directory: {error}"))?;
    let temporary = path.with_extension("json.tmp");
    let data = serde_json::to_vec_pretty(config)
        .map_err(|error| format!("Failed to encode kernel connection: {error}"))?;
    let mut file = fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|error| format!("Failed to write kernel connection: {error}"))?;
    file.write_all(&data)
        .and_then(|_| file.sync_all())
        .map_err(|error| format!("Failed to persist kernel connection: {error}"))?;
    fs::rename(&temporary, &path)
        .map_err(|error| format!("Failed to activate kernel connection: {error}"))?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
        .map_err(|error| format!("Failed to protect kernel connection: {error}"))
}

#[tauri::command]
pub(crate) fn get_kernel_connection(app: AppHandle) -> Result<String, String> {
    let config = load_kernel_connection(&app)?;
    serde_json::to_string(&PublicKernelConnection {
        mode: config.mode,
        url: config.url,
        has_token: !config.token.is_empty(),
        ssh: config.ssh,
    })
    .map_err(|error| format!("Failed to encode kernel connection: {error}"))
}

#[tauri::command]
pub(crate) fn list_ssh_hosts() -> Result<String, String> {
    serde_json::to_string(&ssh_connection::list_config_hosts())
        .map_err(|error| format!("Failed to encode SSH hosts: {error}"))
}

#[tauri::command]
pub(crate) async fn test_ssh_host(input: SshHostInput) -> Result<String, String> {
    let connection = ssh_connection::normalize_connection(input)?;
    let probe = tokio::task::spawn_blocking(move || ssh_connection::probe(&connection))
        .await
        .map_err(|error| format!("SSH test task failed: {error}"))??;
    serde_json::to_string(&probe).map_err(|error| format!("Failed to encode SSH result: {error}"))
}

#[tauri::command]
pub(crate) async fn deploy_ssh_kernel(app: AppHandle, input: SshHostInput) -> Result<(), String> {
    let connection = ssh_connection::normalize_connection(input)?;
    let existing = load_kernel_connection(&app).unwrap_or_default();
    let token = if existing.mode == "ssh"
        && existing.ssh.as_ref().is_some_and(|configured| {
            configured.target == connection.target && configured.port == connection.port
        })
        && existing.token.len() >= 32
    {
        existing.token
    } else {
        generate_token()?
    };
    let worker_app = app.clone();
    let worker_connection = connection.clone();
    let worker_token = token.clone();
    tokio::task::spawn_blocking(move || {
        ssh_connection::deploy_and_connect(&worker_app, &worker_connection, &worker_token)
    })
    .await
    .map_err(|error| format!("SSH deployment task failed: {error}"))??;

    let config = validate_kernel_connection(StoredKernelConnection {
        mode: "ssh".into(),
        url: String::new(),
        token,
        ssh: Some(connection),
    })?;
    save_kernel_connection(&app, &config)?;
    app.request_restart();
    Ok(())
}

#[tauri::command]
pub(crate) async fn test_kernel_connection(
    app: AppHandle,
    input: KernelConnectionInput,
) -> Result<(), String> {
    let config = resolve_kernel_connection(&app, input)?;
    request_with_connection(&config, "GET", "/readyz", None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) fn update_kernel_connection(
    app: AppHandle,
    input: KernelConnectionInput,
) -> Result<(), String> {
    let config = resolve_kernel_connection(&app, input)?;
    save_kernel_connection(&app, &config)?;
    app.request_restart();
    Ok(())
}

fn generate_token() -> Result<String, String> {
    let mut bytes = [0u8; 32];
    fs::File::open("/dev/urandom")
        .and_then(|mut file| file.read_exact(&mut bytes))
        .map_err(|error| format!("Failed to generate SSH kernel token: {error}"))?;
    Ok(bytes.iter().map(|byte| format!("{byte:02x}")).collect())
}

/// Find the next newline in a byte buffer.
fn find_line_end(buf: &[u8]) -> Option<usize> {
    buf.iter().position(|&b| b == b'\n')
}

type LocalHttpClient = Client<UnixConnector, Full<Bytes>>;

fn local_client() -> LocalHttpClient {
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
    request_with_connection(&active_connection(), method, path, body).await
}

async fn request_with_connection(
    connection: &StoredKernelConnection,
    method: &str,
    path: &str,
    body: Option<&str>,
) -> Result<String, String> {
    if connection.mode == "remote" || connection.mode == "ssh" {
        if connection.mode == "ssh" {
            ssh_connection::ensure_tunnel()?;
        }
        let bytes = remote_request_bytes(
            connection,
            method,
            path,
            Some("application/json"),
            body.unwrap_or_default().as_bytes().to_vec(),
        )
        .await?;
        return String::from_utf8(bytes).map_err(|_| "Kernel response is not UTF-8".into());
    }
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

    let resp = local_client()
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
    let connection = active_connection();
    if connection.mode == "remote" || connection.mode == "ssh" {
        if connection.mode == "ssh" {
            ssh_connection::ensure_tunnel()?;
        }
        return remote_request_bytes(&connection, method, path, content_type, body).await;
    }
    let uri = socket_uri(path)?;
    let mut builder = Request::builder().method(method_from_str(method)).uri(uri);
    if let Some(content_type) = content_type {
        builder = builder.header("content-type", content_type);
    }
    let req = builder
        .body(Full::new(Bytes::from(body)))
        .map_err(|e| format!("Failed to build request: {e}"))?;
    let resp = local_client()
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

fn remote_url(connection: &StoredKernelConnection, path: &str) -> Result<Url, String> {
    let base = Url::parse(&(connection.url.clone() + "/"))
        .map_err(|error| format!("Invalid remote kernel URL: {error}"))?;
    base.join(path.trim_start_matches('/'))
        .map_err(|error| format!("Invalid kernel request path: {error}"))
}

fn remote_client() -> Result<&'static reqwest::Client, String> {
    match REMOTE_CLIENT.get_or_init(|| {
        reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .build()
            .map_err(|error| format!("Failed to build remote kernel client: {error}"))
    }) {
        Ok(client) => Ok(client),
        Err(error) => Err(error.clone()),
    }
}

async fn remote_request_bytes(
    connection: &StoredKernelConnection,
    method: &str,
    path: &str,
    content_type: Option<&str>,
    body: Vec<u8>,
) -> Result<Vec<u8>, String> {
    let method = reqwest::Method::from_bytes(method.as_bytes())
        .map_err(|error| format!("Invalid request method: {error}"))?;
    let mut request = remote_client()?
        .request(method, remote_url(connection, path)?)
        .bearer_auth(&connection.token);
    if let Some(content_type) = content_type {
        request = request.header("content-type", content_type);
    }
    let response = request
        .body(body)
        .send()
        .await
        .map_err(|error| format!("Failed to connect to remote kernel: {error}"))?;
    let status = response.status();
    let bytes = response
        .bytes()
        .await
        .map_err(|error| format!("Failed to read remote kernel response: {error}"))?;
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
    let connection = active_connection();
    if connection.mode == "remote" || connection.mode == "ssh" {
        if connection.mode == "ssh" {
            if let Err(error) = ssh_connection::ensure_tunnel() {
                let _ = ready.send(Err(error.clone()));
                return Err(error);
            }
        }
        return subscribe_remote(&connection, path, channel, ready).await;
    }
    let setup = async {
        let uri = socket_uri(path)?;
        let req = Request::builder()
            .method(Method::GET)
            .uri(uri)
            .header("accept", "text/event-stream")
            .body(Full::new(Bytes::new()))
            .map_err(|e| format!("Failed to build request: {e}"))?;

        let resp = local_client()
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
                forward_sse_chunk(
                    &mut buf,
                    data.copy_to_bytes(data.remaining()).as_ref(),
                    &channel,
                );
            }
            Err(e) => return Err(format!("Failed to read event stream: {e}")),
        }
    }
    Ok(())
}

async fn subscribe_remote(
    connection: &StoredKernelConnection,
    path: &str,
    channel: Channel<String>,
    ready: oneshot::Sender<Result<(), String>>,
) -> Result<(), String> {
    let setup = async {
        let response = remote_client()?
            .get(remote_url(connection, path)?)
            .bearer_auth(&connection.token)
            .header("accept", "text/event-stream")
            .send()
            .await
            .map_err(|error| format!("Failed to connect to remote kernel: {error}"))?;
        if !response.status().is_success() {
            return Err(format!(
                "Subscription failed: HTTP {}",
                response.status().as_u16()
            ));
        }
        Ok(response)
    }
    .await;

    let response = match setup {
        Ok(response) => {
            let _ = ready.send(Ok(()));
            response
        }
        Err(error) => {
            let _ = ready.send(Err(error.clone()));
            return Err(error);
        }
    };

    let mut stream = response.bytes_stream();
    let mut buf = Vec::new();
    while let Some(chunk) = stream.next().await {
        match chunk {
            Ok(data) => forward_sse_chunk(&mut buf, &data, &channel),
            Err(error) => return Err(format!("Failed to read remote event stream: {error}")),
        }
    }
    Ok(())
}

fn forward_sse_chunk(buf: &mut Vec<u8>, data: &[u8], channel: &Channel<String>) {
    buf.extend_from_slice(data);
    while let Some(pos) = find_line_end(buf) {
        let line: Vec<u8> = buf.drain(..=pos).collect();
        let end = line
            .iter()
            .rposition(|&byte| byte != b'\n' && byte != b'\r')
            .map(|position| position + 1)
            .unwrap_or(0);
        if let Ok(text) = std::str::from_utf8(&line[..end]) {
            if let Some(payload) = text.strip_prefix("data: ") {
                let _ = channel.send(payload.to_string());
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{request_with_connection, validate_kernel_connection, StoredKernelConnection};
    use crate::ssh_connection::SshConnection;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpListener;

    fn remote(url: &str) -> StoredKernelConnection {
        StoredKernelConnection {
            mode: "remote".into(),
            url: url.into(),
            token: "0123456789abcdef0123456789abcdef".into(),
            ssh: None,
        }
    }

    #[test]
    fn remote_connection_requires_https_except_on_loopback() {
        assert!(validate_kernel_connection(remote("https://foya.example.com")).is_ok());
        assert!(validate_kernel_connection(remote("http://127.0.0.1:8787")).is_ok());
        assert!(validate_kernel_connection(remote("http://foya.example.com")).is_err());
    }

    #[test]
    fn remote_connection_rejects_embedded_credentials() {
        assert!(validate_kernel_connection(remote("https://user:pass@foya.example.com")).is_err());
    }

    #[test]
    fn ssh_connection_restores_default_remote_port() {
        let config = validate_kernel_connection(StoredKernelConnection {
            mode: "ssh".into(),
            url: String::new(),
            token: "0123456789abcdef0123456789abcdef".into(),
            ssh: Some(SshConnection {
                name: "home".into(),
                target: "home".into(),
                port: 22,
                remote_port: 0,
            }),
        })
        .unwrap();
        assert_eq!(config.ssh.unwrap().remote_port, 8787);
    }

    #[tokio::test]
    async fn remote_request_sends_bearer_token() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = vec![0; 4096];
            let length = stream.read(&mut request).await.unwrap();
            let request = String::from_utf8_lossy(&request[..length]);
            assert!(request.starts_with("GET /readyz HTTP/1.1\r\n"));
            assert!(request
                .to_ascii_lowercase()
                .contains("authorization: bearer 0123456789abcdef0123456789abcdef\r\n"));
            stream
                .write_all(
                    b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
                )
                .await
                .unwrap();
        });

        let config = validate_kernel_connection(remote(&format!("http://{address}"))).unwrap();
        let response = request_with_connection(&config, "GET", "/readyz", None)
            .await
            .unwrap();
        assert_eq!(response, "ok");
        server.await.unwrap();
    }
}
