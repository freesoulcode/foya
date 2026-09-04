use tauri::ipc::Channel;

use super::encode_query_component;
use crate::kernel;

#[tauri::command]
pub(crate) async fn list_canvases(session_id: Option<String>) -> Result<String, String> {
    let path = session_id
        .filter(|value| !value.is_empty())
        .map(|value| format!("/canvases?session_id={}", encode_query_component(&value)))
        .unwrap_or_else(|| "/canvases".to_string());
    kernel::request("GET", &path, None).await
}

#[tauri::command]
pub(crate) async fn create_canvas(
    session_id: Option<String>,
    project_id: Option<String>,
    title: Option<String>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "session_id": session_id.unwrap_or_default(),
        "project_id": project_id.unwrap_or_default(),
        "title": title.unwrap_or_default(),
    })
    .to_string();
    kernel::request("POST", "/canvases", Some(&body)).await
}

#[tauri::command]
pub(crate) async fn get_canvas(canvas_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/canvases/{canvas_id}"), None).await
}

#[tauri::command]
pub(crate) async fn update_canvas(
    canvas_id: String,
    patch: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/canvases/{canvas_id}"),
        Some(&patch.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_canvas(canvas_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/canvases/{canvas_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn upload_canvas_asset(
    canvas_id: String,
    name: String,
    media_type: String,
    data: Vec<u8>,
) -> Result<String, String> {
    let boundary = "foya-canvas-asset-boundary";
    let safe_name = name.replace(['"', '\r', '\n'], "_");
    let safe_type = media_type.replace(['\r', '\n'], "");
    let mut body = format!(
        "--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{safe_name}\"\r\nContent-Type: {safe_type}\r\n\r\n"
    ).into_bytes();
    body.extend_from_slice(&data);
    body.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());
    let response = kernel::request_bytes(
        "POST",
        &format!("/canvases/{canvas_id}/assets"),
        Some(&format!("multipart/form-data; boundary={boundary}")),
        body,
    )
    .await?;
    String::from_utf8(response).map_err(|e| format!("画布资产响应不是 UTF-8: {e}"))
}

#[tauri::command]
pub(crate) async fn read_canvas_asset(
    canvas_id: String,
    asset_id: String,
) -> Result<Vec<u8>, String> {
    kernel::request_bytes(
        "GET",
        &format!("/canvases/{canvas_id}/assets/{asset_id}"),
        None,
        Vec::new(),
    )
    .await
}

#[tauri::command]
pub(crate) async fn generate_canvas_image(
    canvas_id: String,
    request: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/canvases/{canvas_id}/generate-image"),
        Some(&request.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn generate_canvas_video(
    canvas_id: String,
    request: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/canvases/{canvas_id}/generate-video"),
        Some(&request.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn subscribe_canvas_events(
    canvas_id: String,
    channel: Channel<String>,
) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let path = format!("/canvases/{canvas_id}/events");
        let _ = kernel::subscribe_path(&path, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "画布事件订阅在连接前意外结束".to_string())?
}
