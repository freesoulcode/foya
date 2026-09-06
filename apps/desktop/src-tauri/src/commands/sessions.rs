use tauri::ipc::Channel;

use crate::kernel;

/// 建会话,返回会话 JSON。options 为可选的创建参数(model/project_id/approval_mode)。
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

/// 局部更新会话(模型/工作目录/审批档位),返回更新后的会话 JSON。
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

/// 提交一轮对话。
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
    String::from_utf8(response).map_err(|e| format!("附件响应不是 UTF-8: {e}"))
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

/// 回退一条已完成的用户消息到输入框,并裁剪当前 active history。
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

/// 读取当前会话尚未处理的 Agent 文件变更。
#[tauri::command]
pub(crate) async fn load_file_review(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/file-review"), None).await
}

/// 保留或撤销当前会话尚未处理的 Agent 文件变更。
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

/// 手动压缩会话的已完成历史。
#[tauri::command]
pub(crate) async fn compact_session(session_id: String) -> Result<String, String> {
    kernel::request("POST", &format!("/sessions/{session_id}/compact"), None).await
}

/// 读取会话的待发送队列。
#[tauri::command]
pub(crate) async fn list_queued_messages(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/queue"), None).await
}

/// 显式追加一条待发送消息。
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

/// 编辑待发送消息正文或顺序。
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

/// 删除一条待发送消息。
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

/// 取消当前回合并优先发送选中的队列消息。
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

/// 列出所有会话。
#[tauri::command]
pub(crate) async fn list_sessions() -> Result<String, String> {
    kernel::request("GET", "/sessions", None).await
}

/// 列出一个父会话的直接子 Agent 会话。
#[tauri::command]
pub(crate) async fn list_child_sessions(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/children"), None).await
}

/// 列出父会话创建的异步 Agent runs。
#[tauri::command]
pub(crate) async fn list_agent_runs(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agents"), None).await
}

/// 启动一个异步 Agent run。
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

/// 取消一个异步 Agent run。
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

/// 读取父任务树的 token 预算。
#[tauri::command]
pub(crate) async fn load_agent_budget(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/agent-budget"), None).await
}

/// 加载某会话的对话历史。
#[tauri::command]
pub(crate) async fn load_history(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/history"), None).await
}

/// 读取会话最近一次模型请求的 token 使用情况。
#[tauri::command]
pub(crate) async fn load_usage(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/usage"), None).await
}

/// 读取全局应用使用统计。
#[tauri::command]
pub(crate) async fn load_usage_statistics(days: u16) -> Result<String, String> {
    kernel::request("GET", &format!("/usage?days={days}"), None).await
}

/// 订阅会话事件流。在后台异步任务持续把 SSE 事件经 Channel 推给前端。
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
        .map_err(|_| "事件订阅在连接前意外结束".to_string())?
}

/// 回执审批决策(批准/拒绝)。
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

/// 中断当前会话正在运行的回合(用户点停止)。
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

/// 删除会话(中断回合、清除元数据与历史、广播移除)。
#[tauri::command]
pub(crate) async fn delete_session(session_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/sessions/{session_id}"), None)
        .await
        .map(|_| ())
}
