// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use std::path::PathBuf;

use tauri::ipc::Channel;
use tauri_plugin_shell::process::CommandEvent;
use tauri_plugin_shell::ShellExt;

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

    /// 订阅某会话的 SSE 事件流,逐条经 Channel 推给前端(每个 data 行一条)。
    pub async fn subscribe(
        session_id: &str,
        channel: Channel<String>,
        ready: oneshot::Sender<Result<(), String>>,
    ) -> Result<(), String> {
        let setup = async {
            let path = format!("/sessions/{session_id}/events");
            let uri = socket_uri(&path)?;
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

/// 建会话,返回会话 JSON。options 为可选的创建参数(model/workspace/approval_mode)。
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
async fn submit_turn(session_id: String, message: String) -> Result<String, String> {
    let body = serde_json::json!({ "message": message }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns"),
        Some(&body),
    )
    .await
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
async fn enqueue_message(session_id: String, message: String) -> Result<String, String> {
    let body = serde_json::json!({ "message": message }).to_string();
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

/// 读取当前 provider 配置(key 脱敏)。
#[cfg(unix)]
#[tauri::command]
async fn get_provider() -> Result<String, String> {
    kernel::request("GET", "/config/provider", None).await
}

/// 保存 provider 配置(热替换)。
#[cfg(unix)]
#[tauri::command]
async fn set_provider(config: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/config/provider", Some(&config.to_string())).await
}

/// 拉取当前 provider 可用模型列表(内核用已配置的 base_url + api_key 代求 /models)。
#[cfg(unix)]
#[tauri::command]
async fn list_models() -> Result<String, String> {
    kernel::request("GET", "/config/models", None).await
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
fn submit_turn(_session_id: String, _message: String) -> Result<String, String> {
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
fn enqueue_message(_session_id: String, _message: String) -> Result<String, String> {
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
fn get_provider() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn set_provider(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn list_models() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn subscribe_events(_session_id: String, _channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
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

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_dialog::init())
        .setup(|app| {
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
            let (mut rx, _child) = sidecar.spawn()?;
            tauri::async_runtime::spawn(async move {
                while let Some(event) = rx.recv().await {
                    if let CommandEvent::Stdout(line) = event {
                        println!("[foya-kernel] {}", String::from_utf8_lossy(&line));
                    }
                }
            });
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            create_session,
            update_session,
            submit_turn,
            edit_turn,
            compact_session,
            list_queued_messages,
            enqueue_message,
            update_queued_message,
            delete_queued_message,
            dispatch_queued_message,
            list_sessions,
            load_history,
            load_usage,
            get_provider,
            set_provider,
            list_models,
            subscribe_events,
            resolve_approval,
            cancel_turn,
            delete_session
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
