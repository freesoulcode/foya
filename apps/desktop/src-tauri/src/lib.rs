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
// 脚手架阶段用 std::os::unix::net 手写最小 HTTP/1.1 over Unix socket,零额外依赖。
// Windows 传输(named pipe / AF_UNIX)后续单独实现。

#[cfg(unix)]
mod kernel {
    use super::kernel_socket_path;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::os::unix::net::UnixStream;
    use std::time::Duration;
    use tauri::ipc::Channel;

    /// 发一个带 body 的 HTTP 请求,读完整响应,返回 body 字符串(用于短请求)。
    pub fn request(method: &str, path: &str, body: Option<&str>) -> Result<String, String> {
        let sock = kernel_socket_path().ok_or("无法解析 socket 路径")?;
        let mut stream = UnixStream::connect(&sock).map_err(|e| format!("连接内核失败: {e}"))?;
        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
            .map_err(|e| e.to_string())?;

        let body = body.unwrap_or("");
        let req = format!(
            "{method} {path} HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
            body.len()
        );
        stream.write_all(req.as_bytes()).map_err(|e| e.to_string())?;

        let mut resp = String::new();
        stream.read_to_string(&mut resp).map_err(|e| e.to_string())?;
        let body = resp.rsplit("\r\n\r\n").next().unwrap_or("").trim().to_string();
        Ok(body)
    }

    /// 订阅某会话的 SSE 事件流,逐条经 Channel 推给前端(每个 data 行一条)。
    /// 阻塞运行,应在独立线程调用。
    pub fn subscribe(session_id: &str, channel: Channel<String>) -> Result<(), String> {
        let sock = kernel_socket_path().ok_or("无法解析 socket 路径")?;
        let stream = UnixStream::connect(&sock).map_err(|e| format!("连接内核失败: {e}"))?;

        let path = format!("/sessions/{session_id}/events");
        let req = format!(
            "GET {path} HTTP/1.1\r\nHost: localhost\r\nAccept: text/event-stream\r\nConnection: keep-alive\r\n\r\n"
        );
        {
            let mut w = stream.try_clone().map_err(|e| e.to_string())?;
            w.write_all(req.as_bytes()).map_err(|e| e.to_string())?;
        }

        let reader = BufReader::new(stream);
        for line in reader.lines() {
            let line = line.map_err(|e| e.to_string())?;
            // SSE 的 data 行:把 JSON 负载推给前端。
            if let Some(payload) = line.strip_prefix("data: ") {
                let _ = channel.send(payload.to_string());
            }
        }
        Ok(())
    }
}

/// 建会话,返回会话 JSON。
#[cfg(unix)]
#[tauri::command]
fn create_session() -> Result<String, String> {
    kernel::request("POST", "/sessions", None)
}

/// 提交一轮对话。
#[cfg(unix)]
#[tauri::command]
fn submit_turn(session_id: String, message: String) -> Result<String, String> {
    let body = serde_json::json!({ "message": message }).to_string();
    kernel::request("POST", &format!("/sessions/{session_id}/turns"), Some(&body))
}

/// 列出所有会话。
#[cfg(unix)]
#[tauri::command]
fn list_sessions() -> Result<String, String> {
    kernel::request("GET", "/sessions", None)
}

/// 加载某会话的对话历史。
#[cfg(unix)]
#[tauri::command]
fn load_history(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/history"), None)
}

/// 读取当前 provider 配置(key 脱敏)。
#[cfg(unix)]
#[tauri::command]
fn get_provider() -> Result<String, String> {
    kernel::request("GET", "/config/provider", None)
}

/// 保存 provider 配置(热替换)。
#[cfg(unix)]
#[tauri::command]
fn set_provider(config: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/config/provider", Some(&config.to_string()))
}

/// 订阅会话事件流。在后台线程持续把 SSE 事件经 Channel 推给前端。
#[cfg(unix)]
#[tauri::command]
fn subscribe_events(session_id: String, channel: Channel<String>) -> Result<(), String> {
    std::thread::spawn(move || {
        let _ = kernel::subscribe(&session_id, channel);
    });
    Ok(())
}

// Windows 占位。
#[cfg(not(unix))]
#[tauri::command]
fn create_session() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg(not(unix))]
#[tauri::command]
fn submit_turn(_session_id: String, _message: String) -> Result<String, String> {
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
fn subscribe_events(_session_id: String, _channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
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
            submit_turn,
            list_sessions,
            load_history,
            get_provider,
            set_provider,
            subscribe_events
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
