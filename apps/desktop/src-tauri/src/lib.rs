// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use std::path::PathBuf;

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

/// ping_kernel 通过 Unix socket 连内核 /healthz,验证前端→Rust→socket→内核链路。
/// 脚手架阶段用 std::os::unix::net 手写最小 HTTP/1.1,零额外依赖。
#[cfg(unix)]
#[tauri::command]
fn ping_kernel() -> Result<String, String> {
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;
    use std::time::Duration;

    let path = kernel_socket_path().ok_or("无法解析 socket 路径")?;
    let mut stream = UnixStream::connect(&path).map_err(|e| format!("连接内核失败: {e}"))?;
    stream
        .set_read_timeout(Some(Duration::from_secs(3)))
        .map_err(|e| e.to_string())?;

    let req = "GET /healthz HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n";
    stream.write_all(req.as_bytes()).map_err(|e| e.to_string())?;

    let mut resp = String::new();
    stream.read_to_string(&mut resp).map_err(|e| e.to_string())?;

    // 取 HTTP body(最后一段)。
    let body = resp.rsplit("\r\n\r\n").next().unwrap_or("").trim().to_string();
    Ok(body)
}

/// Windows 占位:后续用 named pipe 或 AF_UNIX 实现。
#[cfg(not(unix))]
#[tauri::command]
fn ping_kernel() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            // 启动时把打包进来的 Go 内核 sidecar 拉起(connect-or-spawn 的 spawn 部分)。
            // Tauri 会自动解析当前平台对应的二进制(如 foya-aarch64-apple-darwin)。
            let sidecar = app.shell().sidecar("foya")?;
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
        .invoke_handler(tauri::generate_handler![ping_kernel])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
