// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
mod browser;
mod commands;
#[cfg(unix)]
mod kernel;
mod project_files;

use browser::*;
use commands::*;
use project_files::*;
use std::sync::{Arc, Mutex};

#[cfg(not(target_os = "macos"))]
use tauri::Manager;
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

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
            let mut sidecar = app
                .shell()
                .sidecar("foya")?
                .env("FOYA_PARENT_PID", std::process::id().to_string());
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
            fork_session,
            update_session,
            submit_turn,
            upload_image,
            read_artifact,
            delete_artifact,
            list_canvases,
            create_canvas,
            get_canvas,
            update_canvas,
            delete_canvas,
            upload_canvas_asset,
            read_canvas_asset,
            generate_canvas_image,
            generate_canvas_video,
            subscribe_canvas_events,
            rewind_turn,
            load_file_review,
            resolve_file_review,
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
            load_usage_statistics,
            list_connections,
            create_connection,
            update_connection,
            delete_connection,
            list_connection_models,
            get_default_models,
            update_default_models,
            list_skills,
            list_agents,
            get_agent_limits,
            update_agent_limits,
            get_feishu_bot_settings,
            update_feishu_bot_settings,
            list_channels,
            create_channel,
            start_feishu_registration,
            get_feishu_registration,
            cancel_feishu_registration,
            update_channel,
            delete_channel,
            list_automations,
            create_automation,
            update_automation,
            delete_automation,
            run_automation,
            get_memory_settings,
            update_memory_settings,
            get_hooks,
            update_hooks,
            list_commands,
            create_command,
            update_command,
            delete_command,
            list_session_commands,
            execute_command,
            get_workflow,
            approve_workflow,
            list_projects,
            register_project,
            update_project,
            delete_project,
            list_project_skills,
            list_project_agents,
            list_context_items,
            create_context_item,
            update_context_item,
            delete_context_item,
            subscribe_context_events,
            set_skill_enabled,
            set_skill_pinned,
            get_web_search_settings,
            update_web_search_settings,
            test_web_search,
            resolve_browser_action,
            get_mcp_config,
            update_mcp_config,
            get_mcp_status,
            search_mcp_registry,
            list_plugins,
            list_plugin_marketplaces,
            add_plugin_marketplace,
            browse_plugin_marketplace,
            refresh_plugin_marketplace,
            set_plugin_marketplace_enabled,
            remove_plugin_marketplace,
            preview_marketplace_plugin,
            install_marketplace_plugin,
            install_plugin,
            set_plugin_enabled,
            remove_plugin,
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
            set_browser_element_picker,
            execute_browser_action,
            browser_action_result,
            hide_browser,
            close_browser,
            list_project_files,
            read_project_file,
            list_external_editors,
            open_project_in_external_editor,
            watch_project_files,
            unwatch_project_files,
            create_project_file,
            create_project_directory,
            rename_project_entry,
            delete_project_entry,
            resolve_project_path,
            resolve_approval,
            answer_questions,
            cancel_questions,
            cancel_turn,
            cancel_tool,
            background_tool,
            reveal_tool_command,
            list_background_commands,
            get_background_command,
            stop_background_command,
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
