use tauri::ipc::Channel;

use super::encode_query_component;
use crate::kernel;

/// 列出已配置的模型连接(key 脱敏)。
#[tauri::command]
pub(crate) async fn list_connections() -> Result<String, String> {
    kernel::request("GET", "/connections", None).await
}

/// 新建一个 API Key 模型连接。
#[tauri::command]
pub(crate) async fn create_connection(config: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/connections", Some(&config.to_string())).await
}

/// 局部更新一个模型连接。
#[tauri::command]
pub(crate) async fn update_connection(
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

#[tauri::command]
pub(crate) async fn delete_connection(connection_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/connections/{connection_id}"), None)
        .await
        .map(|_| ())
}

/// 拉取指定连接可用的模型目录。
#[tauri::command]
pub(crate) async fn list_connection_models(
    connection_id: String,
    refresh: bool,
) -> Result<String, String> {
    let path = if refresh {
        format!("/connections/{connection_id}/models?refresh=true")
    } else {
        format!("/connections/{connection_id}/models")
    };
    kernel::request("GET", &path, None).await
}

#[tauri::command]
pub(crate) async fn get_default_models() -> Result<String, String> {
    kernel::request("GET", "/settings/default-models", None).await
}

#[tauri::command]
pub(crate) async fn update_default_models(defaults: serde_json::Value) -> Result<String, String> {
    kernel::request(
        "PUT",
        "/settings/default-models",
        Some(&defaults.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn list_skills() -> Result<String, String> {
    kernel::request("GET", "/skills?all=true", None).await
}

#[tauri::command]
pub(crate) async fn list_agents() -> Result<String, String> {
    kernel::request("GET", "/agents", None).await
}

#[tauri::command]
pub(crate) async fn get_agent_limits() -> Result<String, String> {
    kernel::request("GET", "/settings/agent-limits", None).await
}

#[tauri::command]
pub(crate) async fn update_agent_limits(limits: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/settings/agent-limits", Some(&limits.to_string())).await
}

#[tauri::command]
pub(crate) async fn get_feishu_bot_settings() -> Result<String, String> {
    kernel::request("GET", "/settings/feishu-bot", None).await
}

#[tauri::command]
pub(crate) async fn update_feishu_bot_settings(
    settings: serde_json::Value,
) -> Result<String, String> {
    kernel::request("PUT", "/settings/feishu-bot", Some(&settings.to_string())).await
}

#[tauri::command]
pub(crate) async fn list_channels() -> Result<String, String> {
    kernel::request("GET", "/channels", None).await
}

#[tauri::command]
pub(crate) async fn create_channel(settings: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/channels", Some(&settings.to_string())).await
}

#[tauri::command]
pub(crate) async fn start_feishu_registration(
    settings: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "POST",
        "/channels/feishu/registrations",
        Some(&settings.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn get_feishu_registration(registration_id: String) -> Result<String, String> {
    kernel::request(
        "GET",
        &format!(
            "/channels/feishu/registrations/{}",
            encode_query_component(&registration_id)
        ),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn cancel_feishu_registration(registration_id: String) -> Result<(), String> {
    kernel::request(
        "DELETE",
        &format!(
            "/channels/feishu/registrations/{}",
            encode_query_component(&registration_id)
        ),
        None,
    )
    .await
    .map(|_| ())
}

#[tauri::command]
pub(crate) async fn update_channel(
    channel_id: String,
    settings: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PUT",
        &format!("/channels/{channel_id}"),
        Some(&settings.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_channel(channel_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/channels/{channel_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn list_automations() -> Result<String, String> {
    kernel::request("GET", "/automations", None).await
}

#[tauri::command]
pub(crate) async fn create_automation(input: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/automations", Some(&input.to_string())).await
}

#[tauri::command]
pub(crate) async fn update_automation(
    automation_id: String,
    input: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PUT",
        &format!("/automations/{automation_id}"),
        Some(&input.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_automation(automation_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/automations/{automation_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn run_automation(automation_id: String) -> Result<String, String> {
    kernel::request("POST", &format!("/automations/{automation_id}/run"), None).await
}

#[tauri::command]
pub(crate) async fn get_memory_settings() -> Result<String, String> {
    kernel::request("GET", "/settings/memory", None).await
}

#[tauri::command]
pub(crate) async fn update_memory_settings(settings: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/settings/memory", Some(&settings.to_string())).await
}

#[tauri::command]
pub(crate) async fn get_hooks(scope: String, project_id: String) -> Result<String, String> {
    let mut path = format!("/hooks?scope={}", encode_query_component(&scope));
    if !project_id.is_empty() {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("GET", &path, None).await
}

#[tauri::command]
pub(crate) async fn update_hooks(request: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/hooks", Some(&request.to_string())).await
}

#[tauri::command]
pub(crate) async fn list_commands(scope: String, project_id: String) -> Result<String, String> {
    let mut path = format!("/commands?scope={}", encode_query_component(&scope));
    if !project_id.is_empty() {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("GET", &path, None).await
}

#[tauri::command]
pub(crate) async fn create_command(request: serde_json::Value) -> Result<String, String> {
    kernel::request("POST", "/commands", Some(&request.to_string())).await
}

#[tauri::command]
pub(crate) async fn update_command(
    command_ref: String,
    request: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/commands/{}", encode_query_component(&command_ref)),
        Some(&request.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_command(
    command_ref: String,
    scope: String,
    project_id: String,
) -> Result<(), String> {
    let mut path = format!(
        "/commands/{}?scope={}",
        encode_query_component(&command_ref),
        encode_query_component(&scope),
    );
    if !project_id.is_empty() {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("DELETE", &path, None).await.map(|_| ())
}

#[tauri::command]
pub(crate) async fn list_session_commands(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/commands"), None).await
}

#[tauri::command]
pub(crate) async fn execute_command(
    session_id: String,
    name: String,
    args: String,
) -> Result<String, String> {
    let body = serde_json::json!({ "args": args }).to_string();
    kernel::request(
        "POST",
        &format!(
            "/sessions/{session_id}/commands/{}",
            encode_query_component(&name)
        ),
        Some(&body),
    )
    .await
}

#[tauri::command]
pub(crate) async fn get_workflow(session_id: String) -> Result<String, String> {
    kernel::request("GET", &format!("/sessions/{session_id}/workflow"), None).await
}

#[tauri::command]
pub(crate) async fn approve_workflow(
    session_id: String,
    workflow_id: String,
) -> Result<String, String> {
    kernel::request(
        "POST",
        &format!("/sessions/{session_id}/workflow/{workflow_id}/approve"),
        None,
    )
    .await
}

#[tauri::command]
pub(crate) async fn list_projects() -> Result<String, String> {
    kernel::request("GET", "/projects", None).await
}

#[tauri::command]
pub(crate) async fn register_project(path: String, name: String) -> Result<String, String> {
    let body = serde_json::json!({ "path": path, "name": name }).to_string();
    kernel::request("POST", "/projects", Some(&body)).await
}

#[tauri::command]
pub(crate) async fn update_project(
    project_id: String,
    patch: serde_json::Value,
) -> Result<String, String> {
    kernel::request(
        "PATCH",
        &format!("/projects/{project_id}"),
        Some(&patch.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_project(project_id: String) -> Result<(), String> {
    kernel::request("DELETE", &format!("/projects/{project_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn list_project_skills(project_id: String) -> Result<String, String> {
    let path = format!("/projects/{}/skills", encode_query_component(&project_id));
    kernel::request("GET", &path, None).await
}

#[tauri::command]
pub(crate) async fn list_project_agents(project_id: String) -> Result<String, String> {
    let path = format!("/projects/{}/agents", encode_query_component(&project_id));
    kernel::request("GET", &path, None).await
}

fn context_resource(kind: &str) -> Result<&'static str, String> {
    match kind {
        "rule" => Ok("rules"),
        "memory" => Ok("memories"),
        _ => Err("未知上下文类型".into()),
    }
}

#[tauri::command]
pub(crate) async fn list_context_items(
    kind: String,
    scope: String,
    project_id: Option<String>,
) -> Result<String, String> {
    let resource = context_resource(&kind)?;
    let mut path = format!("/{resource}?scope={}", encode_query_component(&scope));
    if let Some(project_id) = project_id.filter(|value| !value.is_empty()) {
        path.push_str("&project_id=");
        path.push_str(&encode_query_component(&project_id));
    }
    kernel::request("GET", &path, None).await
}

#[tauri::command]
pub(crate) async fn create_context_item(
    kind: String,
    item: serde_json::Value,
) -> Result<String, String> {
    let resource = context_resource(&kind)?;
    kernel::request("POST", &format!("/{resource}"), Some(&item.to_string())).await
}

#[tauri::command]
pub(crate) async fn update_context_item(
    kind: String,
    item_id: String,
    item: serde_json::Value,
) -> Result<String, String> {
    let resource = context_resource(&kind)?;
    kernel::request(
        "PATCH",
        &format!("/{resource}/{item_id}"),
        Some(&item.to_string()),
    )
    .await
}

#[tauri::command]
pub(crate) async fn delete_context_item(kind: String, item_id: String) -> Result<(), String> {
    let resource = context_resource(&kind)?;
    kernel::request("DELETE", &format!("/{resource}/{item_id}"), None)
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn subscribe_context_events(channel: Channel<String>) -> Result<(), String> {
    let (ready_tx, ready_rx) = tokio::sync::oneshot::channel();
    tauri::async_runtime::spawn(async move {
        let _ = kernel::subscribe_path("/context/events", channel, ready_tx).await;
    });
    ready_rx
        .await
        .map_err(|_| "规则与记忆事件订阅在连接前意外结束".to_string())?
}

#[tauri::command]
pub(crate) async fn set_skill_enabled(skill_ref: String, enabled: bool) -> Result<(), String> {
    let body = serde_json::json!({ "enabled": enabled }).to_string();
    let path = format!("/skills/{}", encode_query_component(&skill_ref));
    kernel::request("PATCH", &path, Some(&body))
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn set_skill_pinned(skill_ref: String, pinned: bool) -> Result<(), String> {
    let body = serde_json::json!({ "pinned": pinned }).to_string();
    let path = format!("/skills/{}/pinned", encode_query_component(&skill_ref));
    kernel::request("PATCH", &path, Some(&body))
        .await
        .map(|_| ())
}

#[tauri::command]
pub(crate) async fn get_web_search_settings() -> Result<String, String> {
    kernel::request("GET", "/web-search", None).await
}

#[tauri::command]
pub(crate) async fn update_web_search_settings(
    settings: serde_json::Value,
) -> Result<String, String> {
    kernel::request("PUT", "/web-search", Some(&settings.to_string())).await
}

#[tauri::command]
pub(crate) async fn test_web_search(provider_id: String, query: String) -> Result<String, String> {
    let body = serde_json::json!({ "provider_id": provider_id, "query": query }).to_string();
    kernel::request("POST", "/web-search/test", Some(&body)).await
}

#[tauri::command]
pub(crate) async fn get_mcp_config() -> Result<String, String> {
    kernel::request("GET", "/mcp", None).await
}

#[tauri::command]
pub(crate) async fn update_mcp_config(config: serde_json::Value) -> Result<String, String> {
    kernel::request("PUT", "/mcp", Some(&config.to_string())).await
}

#[tauri::command]
pub(crate) async fn get_mcp_status() -> Result<String, String> {
    kernel::request("GET", "/mcp/status", None).await
}

#[tauri::command]
pub(crate) async fn search_mcp_registry(query: String) -> Result<String, String> {
    let path = format!("/mcp/registry?search={}", encode_query_component(&query));
    kernel::request("GET", &path, None).await
}
