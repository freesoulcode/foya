use tauri::ipc::Channel;

use crate::kernel;

/// Create a chat and return its JSON. Options can specify model, project_id, and approval_mode.
#[tauri::command]
pub(crate) async fn create_session(options: Option<serde_json::Value>) -> Result<String, String> {
    let body = options.map(|v| v.to_string());
    kernel::request("POST", "/sessions", body.as_deref()).await
}

#[tauri::command]
pub(crate) async fn fork_session(
    session_id: String,
    options: Option<serde_json::Value>,
) -> Result<String, String> {
    let body = options.map(|v| v.to_string());
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/fork"),
        body.as_deref(),
    )
    .await
}

/// Partially update a chat and return its JSON.
#[tauri::command]
pub(crate) async fn update_session(
    session_id: String,
    patch: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/sessions/{session_id}"),
        Some(&patch.to_string()),
    )
    .await
}

/// Submit one chat turn.
#[tauri::command]
pub(crate) async fn submit_turn(
    session_id: String,
    message: String,
    skill_ref: Option<String>,
    attachments: Option<Vec<serde_json::Value>>,
    browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "skill_ref": skill_ref.unwrap_or_default(),
        "attachments": attachments.unwrap_or_default(),
        "browser_elements": browser_elements.unwrap_or_default(),
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns"),
        Some(&body),
    )
    .await
}

#[tauri::command]
pub(crate) async fn upload_image(
    session_id: String,
    name: String,
    data: Vec<u8>,
) -> Result<String, String> {
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
    String::from_utf8(response).map_err(|e| format!("Attachment response is not UTF-8: {e}"))
}

#[tauri::command]
pub(crate) async fn read_artifact(
    session_id: String,
    artifact_id: String,
) -> Result<Vec<u8>, String> {
    kernel::request_bytes(
        "GET",
        &format!("/sessions/{session_id}/artifacts/{artifact_id}"),
        None,
        Vec::new(),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_artifact(session_id: String, artifact_id: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/artifacts/{artifact_id}"),
        None,
    )
    .await
    .map(|_| ())
}

/// Rewind a completed user message to the composer and trim active history.
#[tauri::command]
pub(crate) async fn rewind_turn(
    session_id: String,
    message_seq: u64,
    confirm: bool,
    expected_head_seq: u64,
    expected_file_state: String,
    force_file_keys: Vec<String>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "confirm": confirm,
        "expected_head_seq": expected_head_seq,
        "expected_file_state": expected_file_state,
        "force_file_keys": force_file_keys,
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/turns/{message_seq}/rewind"),
        Some(&body),
    )
    .await
}

/// Load pending agent file changes for the current chat.
#[tauri::command]
pub(crate) async fn load_file_review(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/file-review"), None).await
}

/// Keep or undo pending agent file changes for the current chat.
#[tauri::command]
pub(crate) async fn resolve_file_review(
    session_id: String,
    action: String,
    expected_through_seq: u64,
    expected_file_state: String,
    force_file_keys: Vec<String>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "action": action,
        "expected_through_seq": expected_through_seq,
        "expected_file_state": expected_file_state,
        "force_file_keys": force_file_keys,
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/file-review"),
        Some(&body),
    )
    .await
}

/// Manually compact completed chat history.
#[tauri::command]
pub(crate) async fn compact_session(session_id: String) -> Result<String, String> {
    kernel::request("POST", &format!("/sessions/{session_id}/compact"), None).await
}

/// Load a chat queue.
#[tauri::command]
pub(crate) async fn list_queued_messages(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/queue"), None).await
}

/// Add a message to the queue explicitly.
#[tauri::command]
pub(crate) async fn enqueue_message(
    session_id: String,
    message: String,
    skill_ref: Option<String>,
    attachments: Option<Vec<serde_json::Value>>,
    browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    let body = serde_json::json!({
        "message": message,
        "skill_ref": skill_ref.unwrap_or_default(),
        "attachments": attachments.unwrap_or_default(),
        "browser_elements": browser_elements.unwrap_or_default(),
    })
    .to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/queue"),
        Some(&body),
    )
    .await
}

/// Edit queued message content or position.
#[tauri::command]
pub(crate) async fn update_queued_message(
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

/// Delete a queued message.
#[tauri::command]
pub(crate) async fn delete_queued_message(
    session_id: String,
    message_id: String,
) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!("/sessions/{session_id}/queue/{message_id}"),
        None,
    )
    .await
    .map(|_| ())
}

/// Cancel the current turn and dispatch the selected queued message.
#[tauri::command]
pub(crate) async fn dispatch_queued_message(
    session_id: String,
    message_id: String,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/queue/{message_id}/dispatch"),
        None,
    )
    .await
}

/// List all chats.
#[tauri::command]
pub(crate) async fn list_sessions() -> Result<String, String> {
    kernel::request("GET", "/sessions", None).await
}

/// List direct sub-agent chats for a parent chat.
#[tauri::command]
pub(crate) async fn list_child_sessions(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/children"), None).await
}

/// List asynchronous agent runs created by a parent chat.
#[tauri::command]
pub(crate) async fn list_agent_runs(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agents"), None).await
}

/// Start an asynchronous agent run.
#[tauri::command]
pub(crate) async fn start_agent(
    session_id: String,
    request: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/agents"),
        Some(&request.to_string()),
    )
    .await
}

/// Cancel an asynchronous agent run.
#[tauri::command]
pub(crate) async fn cancel_agent(session_id: String, run_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/agents/{run_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

/// Load the parent task tree token budget.
#[tauri::command]
pub(crate) async fn load_agent_budget(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agent-budget"), None).await
}

/// Load chat history.
#[tauri::command]
pub(crate) async fn load_history(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/history"), None).await
}

/// Load token usage for the latest model request.
#[tauri::command]
pub(crate) async fn load_usage(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/usage"), None).await
}

/// Load global application usage statistics.
#[tauri::command]
pub(crate) async fn load_usage_statistics(days: u16) -> Result<String, String> {
    kernel::request("GET", &format!("/usage?days={days}"), None).await
}

/// Subscribe to chat events and forward SSE events through a Channel.
#[tauri::command]
pub(crate) async fn subscribe_events(
    session_id: String,
    channel: Channel<String>,
) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe(&session_id, channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "Event subscription ended before connecting".to_string())?
}

/// Resolve an approval decision.
#[tauri::command]
pub(crate) async fn resolve_approval(
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

#[tauri::command]
pub(crate) async fn answer_questions(
    session_id: String,
    batch_id: String,
    answers: serde_json::Value,
) -> Result<(), String> {
    let body = serde_json::json!({ "answers": answers }).to_string();
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/questions/{batch_id}/answer"),
        Some(&body),
    )
    .await
    .map(|_| ())
}

#[tauri::command]
pub(crate) async fn cancel_questions(session_id: String, batch_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/questions/{batch_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

/// Stop the active turn for the current chat.
#[tauri::command]
pub(crate) async fn cancel_turn(session_id: String) -> Result<(), String> {
    kernel::request("POST", &format!("/sessions/{session_id}/cancel"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn cancel_tool(session_id: String, tool_call_id: String) -> Result<(), String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/tools/{tool_call_id}/cancel"),
        None,
    )
    .await
    .map(|_| ())
}

#[tauri::command]
pub(crate) async fn background_tool(
    session_id: String,
    tool_call_id: String,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/tools/{tool_call_id}/background"),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn reveal_tool_command(
    session_id: String,
    tool_call_id: String,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/tools/{tool_call_id}/reveal"),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn list_background_commands(session_id: String) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/background-commands"),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn get_background_command(
    session_id: String,
    command_id: String,
) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!("/sessions/{session_id}/background-commands/{command_id}"),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn stop_background_command(
    session_id: String,
    command_id: String,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/background-commands/{command_id}/cancel"),
        None,
    )
    .await
}

/// Delete a chat, cancel its turn, clear its data, and broadcast removal.
#[tauri::command]
pub(crate) async fn delete_session(session_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/sessions/{session_id}"), None)
        .await
        .map(|_| ())
}
