use tauri::ipc::Channel;

#[tauri::command]
pub(crate) fn create_session(_options: Option<serde_json::Value>) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn fork_session(
    _session_id: String,
    _options: Option<serde_json::Value>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn update_session(
    _session_id: String,
    _patch: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn submit_turn(
    _session_id: String,
    _message: String,
    _skill_ref: Option<String>,
    _attachments: Option<Vec<serde_json::Value>>,
    _browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn upload_image(
    _session_id: String,
    _name: String,
    _data: Vec<u8>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn read_artifact(_session_id: String, _artifact_id: String) -> Result<Vec<u8>, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn delete_artifact(_session_id: String, _artifact_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_canvases(_session_id: Option<String>) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn create_canvas(
    _session_id: Option<String>,
    _project_id: Option<String>,
    _title: Option<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn get_canvas(_canvas_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn update_canvas(
    _canvas_id: String,
    _patch: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn delete_canvas(_canvas_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn upload_canvas_asset(
    _canvas_id: String,
    _name: String,
    _media_type: String,
    _data: Vec<u8>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn read_canvas_asset(_canvas_id: String, _asset_id: String) -> Result<Vec<u8>, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn generate_canvas_image(
    _canvas_id: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn generate_canvas_video(
    _canvas_id: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}
#[tauri::command]
pub(crate) fn subscribe_canvas_events(
    _canvas_id: String,
    _channel: Channel<String>,
) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) fn rewind_turn(
    _session_id: String,
    _message_seq: u64,
    _confirm: bool,
    _expected_head_seq: u64,
    _expected_file_state: String,
    _force_file_keys: Vec<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn load_file_review(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn resolve_file_review(
    _session_id: String,
    _action: String,
    _expected_through_seq: u64,
    _expected_file_state: String,
    _force_file_keys: Vec<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn compact_session(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_queued_messages(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn enqueue_message(
    _session_id: String,
    _message: String,
    _skill_ref: Option<String>,
    _attachments: Option<Vec<serde_json::Value>>,
    _browser_elements: Option<Vec<serde_json::Value>>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn update_queued_message(
    _session_id: String,
    _message_id: String,
    _patch: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn delete_queued_message(
    _session_id: String,
    _message_id: String,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn dispatch_queued_message(
    _session_id: String,
    _message_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_sessions() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_child_sessions(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_agent_runs(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) fn start_agent(
    _session_id: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) fn cancel_agent(_session_id: String, _run_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) fn load_agent_budget(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) fn load_history(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn load_usage(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn load_usage_statistics(_days: u16) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_connections() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn create_connection(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn update_connection(
    _connection_id: String,
    _config: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn delete_connection(_connection_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn list_connection_models(
    _connection_id: String,
    _refresh: bool,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn get_default_models() -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn update_default_models(_defaults: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn subscribe_events(
    _session_id: String,
    _channel: Channel<String>,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) fn start_terminal(
    _session_id: String,
    _cols: u16,
    _rows: u16,
) -> Result<String, String> {
    Err("Windows 终端尚未实现".into())
}

#[tauri::command]
pub(crate) fn attach_terminal(
    _session_id: String,
    _terminal_ref: String,
) -> Result<String, String> {
    Err("Windows 终端尚未实现".into())
}

#[tauri::command]
pub(crate) fn write_terminal(
    _session_id: String,
    _terminal_ref: String,
    _input: String,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[tauri::command]
pub(crate) fn resize_terminal(
    _session_id: String,
    _terminal_ref: String,
    _cols: u16,
    _rows: u16,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[tauri::command]
pub(crate) fn stop_terminal(_session_id: String, _terminal_ref: String) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[tauri::command]
pub(crate) fn subscribe_terminal(
    _session_id: String,
    _terminal_ref: String,
    _after: u64,
    _channel: Channel<String>,
) -> Result<(), String> {
    Err("Windows 终端尚未实现".into())
}

#[tauri::command]
pub(crate) async fn resolve_approval(
    _session_id: String,
    _request_id: String,
    _decision: String,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn answer_questions(
    _session_id: String,
    _batch_id: String,
    _answers: serde_json::Value,
) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn cancel_questions(_session_id: String, _batch_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn cancel_turn(_session_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn cancel_tool(_session_id: String, _tool_call_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn background_tool(
    _session_id: String,
    _tool_call_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn reveal_tool_command(
    _session_id: String,
    _tool_call_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn list_background_commands(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn get_background_command(
    _session_id: String,
    _command_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn stop_background_command(
    _session_id: String,
    _command_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn delete_session(_session_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现 (脚手架阶段)".into())
}

#[tauri::command]
pub(crate) async fn list_skills() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_available_skills(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_agents() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_agent_limits() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_agent_limits(_limits: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_feishu_bot_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_feishu_bot_settings(
    _settings: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_channels() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn create_channel(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn start_feishu_registration(
    _settings: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_feishu_registration(_registration_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn cancel_feishu_registration(_registration_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_channel(
    _channel_id: String,
    _settings: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn delete_channel(_channel_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_automations() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn create_automation(_input: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_automation(
    _automation_id: String,
    _input: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn delete_automation(_automation_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn run_automation(_automation_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_memory_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_memory_settings(_settings: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_hooks(_scope: String, _project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_hooks(_request: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_commands(_scope: String, _project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn create_command(_request: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_command(
    _command_ref: String,
    _request: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn delete_command(
    _command_ref: String,
    _scope: String,
    _project_id: String,
) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_session_commands(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn execute_command(
    _session_id: String,
    _name: String,
    _args: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_workflow(_session_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn approve_workflow(
    _session_id: String,
    _workflow_id: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_projects() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn register_project(_path: String, _name: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_project(
    _project_id: String,
    _patch: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn delete_project(_project_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_project_skills(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_project_agents(_project_id: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_context_items(
    _kind: String,
    _scope: String,
    _project_id: Option<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn create_context_item(
    _kind: String,
    _item: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_context_item(
    _kind: String,
    _item_id: String,
    _item: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn delete_context_item(_kind: String, _item_id: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) fn subscribe_context_events(_channel: Channel<String>) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn set_skill_enabled(_skill_ref: String, _enabled: bool) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn set_skill_pinned(_skill_ref: String, _pinned: bool) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_web_search_settings() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_web_search_settings(
    _settings: serde_json::Value,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn test_web_search(
    _provider_id: String,
    _query: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_mcp_config() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn update_mcp_config(_config: serde_json::Value) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn get_mcp_status() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn search_mcp_registry(_query: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_plugins() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn list_plugin_marketplaces() -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn add_plugin_marketplace(
    _source: String,
    _git_ref: String,
    _sparse_paths: Vec<String>,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn browse_plugin_marketplace(_name: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn refresh_plugin_marketplace(_name: String) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn set_plugin_marketplace_enabled(
    _name: String,
    _enabled: bool,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn remove_plugin_marketplace(_name: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn preview_marketplace_plugin(
    _marketplace: String,
    _plugin_name: String,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn install_marketplace_plugin(
    _marketplace: String,
    _plugin_name: String,
    _replace: bool,
) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn install_plugin(_source: String, _replace: bool) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn set_plugin_enabled(_name: String, _enabled: bool) -> Result<String, String> {
    Err("Windows 传输尚未实现".into())
}

#[tauri::command]
pub(crate) async fn remove_plugin(_name: String) -> Result<(), String> {
    Err("Windows 传输尚未实现".into())
}
