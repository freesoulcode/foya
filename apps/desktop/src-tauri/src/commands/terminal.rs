use tauri::ipc::Channel;

use crate::kernel;

#[tauri::command]
pub(crate) async fn start_terminal(
    session_id: String,
    cols: u16,
    rows: u16,
) -> Result<String, String> {
    let body = serde_json::json!({ "cols": cols, "rows": rows }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/terminals"),
        Some(&body),
    )
    .await
}

#[tauri::command]
pub(crate) async fn attach_terminal(
    session_id: String,
    terminal_ref: String,
) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}"),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn write_terminal(
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

#[tauri::command]
pub(crate) async fn resize_terminal(
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

#[tauri::command]
pub(crate) async fn stop_terminal(session_id: String, terminal_ref: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/terminals/{terminal_ref}"),
        None,
    )
    .await
    .map(|_| ())
}

#[tauri::command]
pub(crate) async fn subscribe_terminal(
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
        .map_err(|_| "Terminal subscription ended before connecting".to_string())?
}
