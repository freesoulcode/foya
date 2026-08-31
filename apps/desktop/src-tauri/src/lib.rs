// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use std::fs;
use std::path::{Component, Path, PathBuf};
use std::sync::{Arc, Mutex};

use tauri::ipc::Channel;
use tauri::{Emitter, Manager};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

const BROWSER_VIEW_PREFIX: &str = "foya-workbar-browser";
const MAX_PROJECT_ENTRIES: usize = 10_000;
const MAX_PREVIEW_BYTES: u64 = 2 * 1024 * 1024;
const IGNORED_PROJECT_DIRS: &[&str] = &[
    ".git",
    ".idea",
    ".next",
    ".nuxt",
    ".turbo",
    "coverage",
    "dist",
    "node_modules",
    "target",
    "vendor",
];

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

#[derive(serde::Deserialize)]
struct BrowserViewport {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[derive(Clone, serde::Serialize)]
struct BrowserPageLoad {
    browser_id: String,
    url: String,
    status: &'static str,
}

#[derive(Clone, serde::Serialize)]
struct BrowserTitleChanged {
    browser_id: String,
    title: String,
}

#[derive(serde::Serialize)]
struct ProjectEntry {
    path: String,
    name: String,
    is_dir: bool,
}

fn browser_view_label(browser_id: &str) -> Result<String, String> {
    if browser_id.is_empty()
        || browser_id.len() > 64
        || !browser_id
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
    {
        return Err("浏览器实例 ID 无效".into());
    }
    Ok(format!("{BROWSER_VIEW_PREFIX}-{browser_id}"))
}

fn project_root(project_path: &str) -> Result<PathBuf, String> {
    let root = fs::canonicalize(project_path).map_err(|e| format!("无法访问项目目录: {e}"))?;
    if !root.is_dir() {
        return Err("项目路径不是目录".into());
    }
    Ok(root)
}

fn project_relative_path(relative_path: &str) -> Result<&Path, String> {
    let relative = Path::new(relative_path);
    if relative.as_os_str().is_empty()
        || relative
            .components()
            .any(|component| !matches!(component, Component::Normal(_)))
    {
        return Err("项目路径无效".into());
    }
    Ok(relative)
}

fn safe_project_entry(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let relative = project_relative_path(relative_path)?;
    let root = project_root(project_path)?;
    let candidate = root.join(relative);
    let metadata =
        fs::symlink_metadata(&candidate).map_err(|e| format!("无法访问项目条目: {e}"))?;
    if metadata.file_type().is_symlink() {
        return Err("不支持操作符号链接".into());
    }
    let entry = fs::canonicalize(candidate).map_err(|e| format!("无法访问项目条目: {e}"))?;
    if !entry.starts_with(&root) {
        return Err("项目条目不在当前项目中".into());
    }
    Ok(entry)
}

fn safe_project_file(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let file = safe_project_entry(project_path, relative_path)?;
    if !file.is_file() {
        return Err("项目条目不是文件".into());
    }
    Ok(file)
}

fn safe_project_destination(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let relative = project_relative_path(relative_path)?;
    let root = project_root(project_path)?;
    let candidate = root.join(relative);
    let file_name = candidate.file_name().ok_or("项目路径无效")?;
    let parent = candidate.parent().ok_or("项目路径无效")?;
    let parent = fs::canonicalize(parent).map_err(|e| format!("无法访问父目录: {e}"))?;
    if !parent.starts_with(&root) || !parent.is_dir() {
        return Err("父目录不在当前项目中".into());
    }
    let destination = parent.join(file_name);
    match fs::symlink_metadata(&destination) {
        Ok(_) => Err("同名文件或文件夹已存在".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(destination),
        Err(error) => Err(format!("无法检查目标路径: {error}")),
    }
}

fn safe_entry_name(name: &str) -> Result<&str, String> {
    let path = Path::new(name);
    let mut components = path.components();
    if name.is_empty()
        || !matches!(components.next(), Some(Component::Normal(_)))
        || components.next().is_some()
    {
        return Err("名称不能包含路径分隔符".into());
    }
    Ok(name)
}

fn project_relative_string(root: &Path, path: &Path) -> Result<String, String> {
    path.strip_prefix(root)
        .map(|relative| relative.to_string_lossy().replace('\\', "/"))
        .map_err(|_| "项目条目不在当前项目中".into())
}

fn collect_project_entries(
    root: &Path,
    directory: &Path,
    entries: &mut Vec<ProjectEntry>,
) -> Result<(), String> {
    if entries.len() >= MAX_PROJECT_ENTRIES {
        return Ok(());
    }
    let mut children = fs::read_dir(directory)
        .map_err(|e| format!("无法读取项目目录: {e}"))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| format!("无法读取项目目录项: {e}"))?;
    children.sort_by_key(|entry| entry.file_name().to_string_lossy().to_lowercase());

    for child in children {
        if entries.len() >= MAX_PROJECT_ENTRIES {
            break;
        }
        let file_type = child
            .file_type()
            .map_err(|e| format!("无法读取文件类型: {e}"))?;
        if file_type.is_symlink() {
            continue;
        }
        let name = child.file_name().to_string_lossy().into_owned();
        if file_type.is_dir() && IGNORED_PROJECT_DIRS.contains(&name.as_str()) {
            continue;
        }
        let path = child.path();
        let relative = path
            .strip_prefix(root)
            .map_err(|e| e.to_string())?
            .to_string_lossy()
            .replace('\\', "/");
        entries.push(ProjectEntry {
            path: relative,
            name,
            is_dir: file_type.is_dir(),
        });
        if file_type.is_dir() {
            collect_project_entries(root, &path, entries)?;
        }
    }
    Ok(())
}

/// 内核 Unix socket 路径,须与 Go 端 config.DefaultSocketPath 保持一致。
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

// ============ 以下为 Unix 平台的内核连接实现 ============
// 使用 hyper(事实标准 HTTP 实现)+ hyperlocal(Unix socket 连接器)。
// chunked、keep-alive、header 解析全部由库负责,避免手写 HTTP 客户端的边界 bug。
// Windows 传输(named pipe / AF_UNIX)后续单独实现。

#[cfg(unix)]
mod kernel {
    use super::kernel_socket_path;
    use bytes::{Buf, Bytes};
    use futures_util::StreamExt;
    use http_body_util::{BodyDataStream, BodyExt, Full};
    use hyper::{Method, Request, StatusCode};
    use hyper_util::client::legacy::Client;
    use hyper_util::rt::TokioExecutor;
    use hyperlocal::{UnixConnector, Uri as HyperlocalUri};
    use std::path::Path;
    use tauri::ipc::Channel;
    use tokio::sync::oneshot;

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
}

/// 建会话,返回会话 JSON。options 为可选的创建参数(model/project_id/approval_mode)。
#[cfg(unix)]
#[tauri::command]
async fn create_session(options: Option<serde_json::Value>) -> Result<String, String> {
    let body = options.map(|v| v.to_string());
    kernel::request("POST", "/sessions", body.as_deref()).await
}

/// 局部更新会话(模型/工作目录/审批档位),返回更新后的会话 JSON。
#[cfg(unix)]
#[tauri::command]
async fn update_session(session_id: String, patch: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/sessions/{session_id}"),
        Some(&patch.to_string()),
    )
    .await
}

/// 提交一轮对话。
#[cfg(unix)]
#[tauri::command]
async fn submit_turn(
    session_id: String,
    message: String,
    attachments: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "attachments": attachments.unwrap_or_default(),
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns"),
        Some(&body),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn upload_image(session_id: String, name: String, data: Vec<u8>) -> Result<String, String> {
    let boundary = "foya-image-upload-boundary";
    let safe_name = name.replace(['"', '\r', '\n'], "_");
    let mut body = format!(
        "--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{safe_name}\"\r\nContent-Type: application/octet-stream\r\n\r\n"
    )
    .into_bytes();
    body.extend_from_slice(&data);
    body.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());
    let response = kernel::request_bytes(
        "POST",
        &format!("/sessions/{session_id}/artifacts"),
        Some(&format!("multipart/form-data; boundary={boundary}")),
        body,
    )
    .await?;
    String::from_utf8(response).map_err(|e| format!("附件响应不是 UTF-8: {e}"))
}

#[cfg(unix)]
#[tauri::command]
async fn read_artifact(session_id: String, artifact_id: String) -> Result<Vec<u8>, String> {
    kernel::request_bytes(
        "GET",
        &format!("/sessions/{session_id}/artifacts/{artifact_id}"),
        None,
        Vec::new(),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_artifact(session_id: String, artifact_id: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/artifacts/{artifact_id}"),
        None,
    )
    .await
    .map(|_| ())
}

/// 编辑一条已完成的用户消息并从该位置创建新分支。
#[cfg(unix)]
#[tauri::command]
async fn edit_turn(
    session_id: String,
    message_seq: u64,
    message: String,
    confirm_effects: bool,
    expected_head_seq: u64,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "confirm_effects": confirm_effects,
        "expected_head_seq": expected_head_seq,
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns/{message_seq}/edit"),
        Some(&body),
    )
    .await
}

/// 手动压缩会话的已完成历史。
#[cfg(unix)]
#[tauri::command]
async fn compact_session(session_id: String) -> Result<String, String> {
    kernel::request("POST", &format!("/sessions/{session_id}/compact"), None).await
}

/// 读取会话的待发送队列。
#[cfg(unix)]
#[tauri::command]
async fn list_queued_messages(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/queue"), None).await
}

/// 显式追加一条待发送消息。
#[cfg(unix)]
#[tauri::command]
async fn enqueue_message(
    session_id: String,
    message: String,
    attachments: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "attachments": attachments.unwrap_or_default(),
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/queue"),
        Some(&body),
    )
    .await
}

/// 编辑待发送消息正文或顺序。
#[cfg(unix)]
#[tauri::command]
async fn update_queued_message(
    session_id: String,
    message_id: String,
    patch: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/sessions/{session_id}/queue/{message_id}"),
        Some(&patch.to_string()),
    )
    .await
}

/// 删除一条待发送消息。
#[cfg(unix)]
#[tauri::command]
async fn delete_queued_message(session_id: String, message_id: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/queue/{message_id}"),
        None,
    )
    .await
    .map(|_| ())
}

/// 取消当前回合并优先发送选中的队列消息。
#[cfg(unix)]
#[tauri::command]
async fn dispatch_queued_message(session_id: String, message_id: String) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/queue/{message_id}/dispatch"),
        None,
    )
    .await
}

/// 列出所有会话。
#[cfg(unix)]
#[tauri::command]
async fn list_sessions() -> Result<String, String> {
    kernel::request("GET", "/sessions", None).await
}

/// 列出一个父会话的直接子 Agent 会话。
#[cfg(unix)]
#[tauri::command]
async fn list_child_sessions(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/children"), None).await
}

/// 列出父会话创建的异步 Agent runs。
#[cfg(unix)]
#[tauri::command]
async fn list_agent_runs(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agents"), None).await
}

/// 启动一个异步 Agent run。
#[cfg(unix)]
#[tauri::command]
async fn start_agent(session_id: String, request: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/agents"),
        Some(&request.to_string()),
    )
    .await
}

/// 取消一个异步 Agent run。
#[cfg(unix)]
#[tauri::command]
async fn cancel_agent(session_id: String, run_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/agents/{run_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

/// 读取父任务树的 token 预算。
#[cfg(unix)]
#[tauri::command]
async fn load_agent_budget(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agent-budget"), None).await
}

/// 加载某会话的对话历史。
#[cfg(unix)]
#[tauri::command]
async fn load_history(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/history"), None).await
}

/// 读取会话最近一次模型请求的 token 使用情况。
#[cfg(unix)]
#[tauri::command]
async fn load_usage(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/usage"), None).await
}

/// 列出已配置的模型连接(key 脱敏)。
#[cfg(unix)]
#[tauri::command]
async fn list_connections() -> Result<String, String> {
    kernel::request("GET", "/connections", None).await
}

/// 新建一个 API Key 模型连接。
#[cfg(unix)]
#[tauri::command]
async fn create_connection(config: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/connections", Some(&config.to_string())).await
}

/// 局部更新一个模型连接。
#[cfg(unix)]
#[tauri::command]
async fn update_connection(
    connection_id: String,
    config: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/connections/{connection_id}"),
        Some(&config.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_connection(connection_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/connections/{connection_id}"), None)
        .await
        .map(|_| ())
}

/// 拉取指定连接可用的模型目录。
#[cfg(unix)]
#[tauri::command]
async fn list_connection_models(connection_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/connections/{connection_id}/models"), None).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_skills() -> Result<String, String> {
    kernel::request("GET", "/skills?all=true", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_agents() -> Result<String, String> {
    kernel::request("GET", "/agents", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_agent_limits() -> Result<String, String> {
    kernel::request("GET", "/settings/agent-limits", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_agent_limits(limits: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/settings/agent-limits", Some(&limits.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_projects() -> Result<String, String> {
    kernel::request("GET", "/projects", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn register_project(path: String, name: String) -> Result<String, String> {
    let body = serde_json::json!({ "path": path, "name": name }).to_string();
    kernel::request("POST", "/projects", Some(&body)).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_project(project_id: String, patch: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/projects/{project_id}"),
        Some(&patch.to_string()),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn delete_project(project_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/projects/{project_id}"), None)
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn list_project_skills(project_id: String) -> Result<String, String> {
    let path = format!("/projects/{}/skills", encode_query_component(&project_id));
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn list_project_agents(project_id: String) -> Result<String, String> {
    let path = format!("/projects/{}/agents", encode_query_component(&project_id));
    kernel::request("GET", &path, None).await
}

#[cfg(unix)]
#[tauri::command]
async fn set_skill_enabled(skill_ref: String, enabled: bool) -> Result<(), String> {
    let body = serde_json::json!({ "enabled": enabled }).to_string();
    kernel::request("PATCH", &format!("/skills/{skill_ref}"), Some(&body))
        .await
        .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn get_web_search_settings() -> Result<String, String> {
    kernel::request("GET", "/web-search", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_web_search_settings(settings: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/web-search", Some(&settings.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn test_web_search(provider_id: String, query: String) -> Result<String, String> {
    let body = serde_json::json!({ "provider_id": provider_id, "query": query }).to_string();
    kernel::request("POST", "/web-search/test", Some(&body)).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_mcp_config() -> Result<String, String> {
    kernel::request("GET", "/mcp", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn update_mcp_config(config: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/mcp", Some(&config.to_string())).await
}

#[cfg(unix)]
#[tauri::command]
async fn get_mcp_status() -> Result<String, String> {
    kernel::request("GET", "/mcp/status", None).await
}

#[cfg(unix)]
#[tauri::command]
async fn search_mcp_registry(query: String) -> Result<String, String> {
    let path = format!("/mcp/registry?search={}", encode_query_component(&query));
    kernel::request("GET", &path, None).await
}

/// 订阅会话事件流。在后台异步任务持续把 SSE 事件经 Channel 推给前端。
#[cfg(unix)]
#[tauri::command]
async fn subscribe_events(session_id: String, channel: Channel<String>) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe(&session_id, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "事件订阅在连接前意外结束".to_string())?
}

#[cfg(unix)]
#[tauri::command]
async fn start_terminal(session_id: String, cols: u16, rows: u16) -> Result<String, String> {
    let body = serde_json::json!({ "cols": cols, "rows": rows }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals"),
        Some(&body),
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn attach_terminal(session_id: String, terminal_ref: String) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}"),
        None,
    )
    .await
}

#[cfg(unix)]
#[tauri::command]
async fn write_terminal(
    session_id: String,
    terminal_ref: String,
    input: String,
) -> Result<(), String> {
    let body = serde_json::json!({ "input": input }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}/input"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn resize_terminal(
    session_id: String,
    terminal_ref: String,
    cols: u16,
    rows: u16,
) -> Result<(), String> {
    let body = serde_json::json!({ "cols": cols, "rows": rows }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}/resize"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn stop_terminal(session_id: String, terminal_ref: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}"),
        None,
    )
    .await
    .map(|_| ())
}

#[cfg(unix)]
#[tauri::command]
async fn subscribe_terminal(
    session_id: String,
    terminal_ref: String,
    after: u64,
    channel: Channel<String>,
) -> Result<(), String> {
    let path = format!("/sessions/{session_id}/terminals/{terminal_ref}/events?after={after}");
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe_path(&path, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "终端订阅在连接前意外结束".to_string())?
}

/// 回执审批决策(批准/拒绝)。
#[cfg(unix)]
#[tauri::command]
async fn resolve_approval(
    session_id: String,
    request_id: String,
    decision: String,
) -> Result<(), String> {
    let body = serde_json::json!({ "request_id": request_id, "decision": decision }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/approvals/{request_id}"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

/// 中断当前会话正在运行的回合(用户点停止)。
#[cfg(unix)]
#[tauri::command]
async fn cancel_turn(session_id: String) -> Result<(), String> {
    kernel::request("POST", &format!("/sessions/{session_id}/cancel"), None)
        .await
        .map(|_| ())
}

/// 删除会话(中断回合、清除元数据与历史、广播移除)。
#[cfg(unix)]
#[tauri::command]
async fn delete_session(session_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/sessions/{session_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
async fn list_project_files(project_path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let mut entries = Vec::new();
        collect_project_entries(&root, &root, &mut entries)?;
        serde_json::to_string(&entries).map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn read_project_file(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let file = safe_project_file(&project_path, &path)?;
        let metadata = fs::metadata(&file).map_err(|e| format!("无法读取文件信息: {e}"))?;
        if metadata.len() > MAX_PREVIEW_BYTES {
            return Err("文件超过 2 MiB，无法预览".into());
        }
        let bytes = fs::read(file).map_err(|e| format!("无法读取文件: {e}"))?;
        String::from_utf8(bytes).map_err(|_| "二进制文件暂不支持预览".into())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn create_project_file(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let file = safe_project_destination(&project_path, &path)?;
        fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&file)
            .map_err(|e| format!("无法创建文件: {e}"))?;
        project_relative_string(&root, &file)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn create_project_directory(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let directory = safe_project_destination(&project_path, &path)?;
        fs::create_dir(&directory).map_err(|e| format!("无法创建文件夹: {e}"))?;
        project_relative_string(&root, &directory)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn rename_project_entry(
    project_path: String,
    path: String,
    new_name: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let entry = safe_project_entry(&project_path, &path)?;
        let new_name = safe_entry_name(&new_name)?;
        let destination = entry.with_file_name(new_name);

        if destination != entry {
            if let Ok(existing) = fs::canonicalize(&destination) {
                if existing != entry {
                    return Err("同名文件或文件夹已存在".into());
                }
            }
            fs::rename(&entry, &destination).map_err(|e| format!("无法重命名: {e}"))?;
        }

        project_relative_string(&root, &destination)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn delete_project_entry(project_path: String, path: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let entry = safe_project_entry(&project_path, &path)?;
        if entry.is_dir() {
            fs::remove_dir_all(entry).map_err(|e| format!("无法删除文件夹: {e}"))
        } else {
            fs::remove_file(entry).map_err(|e| format!("无法删除文件: {e}"))
        }
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn resolve_project_path(project_path: String, path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let entry = if path.is_empty() {
            project_root(&project_path)?
        } else {
            safe_project_entry(&project_path, &path)?
        };
        Ok(entry.to_string_lossy().into_owned())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
async fn navigate_browser(
    app: tauri::AppHandle,
    browser_id: String,
    url: String,
    viewport: BrowserViewport,
) -> Result<(), String> {
    let parsed = url.parse::<tauri::Url>().map_err(|e| e.to_string())?;
    if !matches!(parsed.scheme(), "http" | "https") {
        return Err("只允许打开 HTTP 或 HTTPS 地址".into());
    }
    if viewport.width <= 0.0 || viewport.height <= 0.0 {
        return Err("浏览器预览区域尺寸无效".into());
    }
    let position = tauri::LogicalPosition::new(viewport.x, viewport.y);
    let size = tauri::LogicalSize::new(viewport.width, viewport.height);
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.navigate(parsed).map_err(|e| e.to_string())?;
        webview
            .set_bounds(tauri::Rect {
                position: tauri::Position::Logical(position),
                size: tauri::Size::Logical(size),
            })
            .map_err(|e| e.to_string())?;
        webview.show().map_err(|e| e.to_string())?;
        webview.set_focus().map_err(|e| e.to_string())?;
        return Ok(());
    }

    let window = app.get_window("main").ok_or("主窗口不可用")?;
    let load_browser_id = browser_id.clone();
    let title_browser_id = browser_id.clone();
    let builder = tauri::webview::WebviewBuilder::new(&label, tauri::WebviewUrl::External(parsed))
        .on_navigation(|target| matches!(target.scheme(), "http" | "https" | "about"))
        .on_page_load(move |webview, payload| {
            let status = match payload.event() {
                tauri::webview::PageLoadEvent::Started => "started",
                tauri::webview::PageLoadEvent::Finished => "finished",
            };
            let _ = webview.app_handle().emit_to(
                "main",
                "browser-page-load",
                BrowserPageLoad {
                    browser_id: load_browser_id.clone(),
                    url: payload.url().to_string(),
                    status,
                },
            );
        })
        .on_document_title_changed(move |webview, title| {
            let _ = webview.app_handle().emit_to(
                "main",
                "browser-title-changed",
                BrowserTitleChanged {
                    browser_id: title_browser_id.clone(),
                    title,
                },
            );
        });
    let webview = window
        .add_child(builder, position, size)
        .map_err(|e| e.to_string())?;
    webview.show().map_err(|e| e.to_string())?;
    webview.set_focus().map_err(|e| e.to_string())?;
    Ok(())
}

#[tauri::command]
fn set_browser_viewport(
    app: tauri::AppHandle,
    browser_id: String,
    viewport: Option<BrowserViewport>,
) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    let Some(webview) = app.get_webview(&label) else {
        return Ok(());
    };
    let Some(viewport) = viewport else {
        return webview.hide().map_err(|e| e.to_string());
    };
    if viewport.width <= 0.0 || viewport.height <= 0.0 {
        return webview.hide().map_err(|e| e.to_string());
    }
    webview
        .set_bounds(tauri::Rect {
            position: tauri::Position::Logical(tauri::LogicalPosition::new(viewport.x, viewport.y)),
            size: tauri::Size::Logical(tauri::LogicalSize::new(viewport.width, viewport.height)),
        })
        .map_err(|e| e.to_string())?;
    webview.show().map_err(|e| e.to_string())
}

#[tauri::command]
fn browser_back(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.eval("history.back()").map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn browser_forward(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview
            .eval("history.forward()")
            .map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn browser_reload(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.reload().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn hide_browser(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.hide().map_err(|e| e.to_string())?;
    }
    Ok(())
}

#[tauri::command]
fn close_browser(app: tauri::AppHandle, browser_id: String) -> Result<(), String> {
    let label = browser_view_label(&browser_id)?;
    if let Some(webview) = app.get_webview(&label) {
        webview.close().map_err(|e| e.to_string())?;
    }
    Ok(())
}

// Windows 占位。
#[cfg(not(unix))]
#[tauri::command]
fn create_session(_options: Option<serde_json::Value>) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_session(_session_id: String, _patch: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn submit_turn(
    _session_id: String,
    _message: String,
    _attachments: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn upload_image(_session_id: String, _name: String, _data: Vec<u8>) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn read_artifact(_session_id: String, _artifact_id: String) -> Result<Vec<u8>, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn delete_artifact(_session_id: String, _artifact_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn edit_turn(
    _session_id: String,
    _message_seq: u64,
    _message: String,
    _confirm_effects: bool,
    _expected_head_seq: u64,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn compact_session(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_queued_messages(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn enqueue_message(
    _session_id: String,
    _message: String,
    _attachments: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_queued_message(
    _session_id: String,
    _message_id: String,
    _patch: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn delete_queued_message(_session_id: String, _message_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn dispatch_queued_message(_session_id: String, _message_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_sessions() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_child_sessions(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn load_history(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn load_usage(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_connections() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn create_connection(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn update_connection(_connection_id: String, _config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn delete_connection(_connection_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_connection_models(_connection_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn subscribe_events(_session_id: String, _channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn start_terminal(_session_id: String, _cols: u16, _rows: u16) -> Result<String, String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn attach_terminal(_session_id: String, _terminal_ref: String) -> Result<String, String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn write_terminal(
    _session_id: String,
    _terminal_ref: String,
    _input: String,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn resize_terminal(
    _session_id: String,
    _terminal_ref: String,
    _cols: u16,
    _rows: u16,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn stop_terminal(_session_id: String, _terminal_ref: String) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn subscribe_terminal(
    _session_id: String,
    _terminal_ref: String,
    _after: u64,
    _channel: Channel<String>,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn resolve_approval(
    _session_id: String,
    _request_id: String,
    _decision: String,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn cancel_turn(_session_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_session(_session_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_skills() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_agents() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_agent_limits() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_agent_limits(_limits: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_projects() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn register_project(_path: String, _name: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_project(_project_id: String, _patch: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn delete_project(_project_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_project_skills(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn list_project_agents(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn set_skill_enabled(_skill_ref: String, _enabled: bool) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_web_search_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_web_search_settings(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn test_web_search(_provider_id: String, _query: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_mcp_config() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn update_mcp_config(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn get_mcp_status() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg(not(unix))]
#[tauri::command]
async fn search_mcp_registry(_query: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let sidecar_child = Arc::new(Mutex::new(None::<CommandChild>));
    let setup_child = Arc::clone(&sidecar_child);
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .setup(move |app| {
            // macOS 用 titleBarStyle=Overlay(在 tauri.conf.json)保留红绿灯;
            // 其他平台关闭原生装饰,改用前端自绘标题栏(WindowControls)。
            #[cfg(not(target_os = "macos"))]
            {
                if let Some(window) = app.get_webview_window("main") {
                    window.set_decorations(false)?;
                }
            }

            // 启动时把打包进来的 Go 内核 sidecar 拉起(connect-or-spawn 的 spawn 部分)。
            // Tauri 会自动解析当前平台对应的二进制(如 foya-aarch64-apple-darwin)。
            let mut sidecar = app.shell().sidecar("foya")?;
            // BYOK:把 provider 配置从当前进程环境透传给内核 sidecar。
            // 脚手架阶段靠环境变量注入(启动 app 前 export FOYA_PROVIDER_*);
            // 后续改为从设置界面写入、key 存 OS keychain。
            for key in [
                "FOYA_PROVIDER_BASE_URL",
                "FOYA_PROVIDER_API_KEY",
                "FOYA_PROVIDER_MODEL",
            ] {
                if let Ok(val) = std::env::var(key) {
                    sidecar = sidecar.env(key, val);
                }
            }
            let (mut rx, child) = sidecar.spawn()?;
            *setup_child.lock().expect("sidecar child lock poisoned") = Some(child);
            tauri::async_runtime::spawn(async move {
                while let Some(event) = rx.recv().await {
                    match event {
                        CommandEvent::Stdout(line) => {
                            println!("[foya-kernel] {}", String::from_utf8_lossy(&line));
                        }
                        CommandEvent::Stderr(line) => {
                            eprintln!("[foya-kernel] {}", String::from_utf8_lossy(&line));
                        }
                        CommandEvent::Error(error) => {
                            eprintln!("[foya-kernel] process error: {error}");
                        }
                        CommandEvent::Terminated(status) => {
                            eprintln!(
                                "[foya-kernel] exited: code={:?} signal={:?}",
                                status.code, status.signal
                            );
                        }
                        _ => {}
                    }
                }
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            create_session,
            update_session,
            submit_turn,
            upload_image,
            read_artifact,
            delete_artifact,
            edit_turn,
            compact_session,
            list_queued_messages,
            enqueue_message,
            update_queued_message,
            delete_queued_message,
            dispatch_queued_message,
            list_sessions,
            list_child_sessions,
            list_agent_runs,
            start_agent,
            cancel_agent,
            load_agent_budget,
            load_history,
            load_usage,
            list_connections,
            create_connection,
            update_connection,
            delete_connection,
            list_connection_models,
            list_skills,
            list_agents,
            get_agent_limits,
            update_agent_limits,
            list_projects,
            register_project,
            update_project,
            delete_project,
            list_project_skills,
            list_project_agents,
            set_skill_enabled,
            get_web_search_settings,
            update_web_search_settings,
            test_web_search,
            get_mcp_config,
            update_mcp_config,
            get_mcp_status,
            search_mcp_registry,
            subscribe_events,
            start_terminal,
            attach_terminal,
            write_terminal,
            resize_terminal,
            stop_terminal,
            subscribe_terminal,
            navigate_browser,
            set_browser_viewport,
            browser_back,
            browser_forward,
            browser_reload,
            hide_browser,
            close_browser,
            list_project_files,
            read_project_file,
            create_project_file,
            create_project_directory,
            rename_project_entry,
            delete_project_entry,
            resolve_project_path,
            resolve_approval,
            cancel_turn,
            delete_session
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application");

    app.run(move |_app_handle, event| {
        if matches!(event, tauri::RunEvent::Exit) {
            if let Some(child) = sidecar_child
                .lock()
                .expect("sidecar child lock poisoned")
                .take()
            {
                let _ = child.kill();
            }
        }
    });
}
