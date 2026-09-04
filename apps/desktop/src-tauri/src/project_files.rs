use std::collections::{HashMap, HashSet};
use std::env;
use std::fs;
use std::path::{Component, Path, PathBuf};
use std::process::Command as ProcessCommand;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc::{self, RecvTimeoutError};
use std::sync::{Arc, LazyLock, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use base64::Engine;
use notify::{Event as NotifyEvent, EventKind, RecommendedWatcher, RecursiveMode, Watcher};
use tauri::ipc::Channel;

static PROJECT_FILE_WATCHERS: LazyLock<Mutex<HashMap<String, Arc<AtomicBool>>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));
static NEXT_PROJECT_FILE_WATCHER_ID: AtomicU64 = AtomicU64::new(1);
static NEXT_EDITOR_ICON_ID: AtomicU64 = AtomicU64::new(1);
const MAX_PROJECT_ENTRIES: usize = 10_000;
const MAX_PREVIEW_BYTES: u64 = 2 * 1024 * 1024;
const PROJECT_FILE_EVENT_BATCH: Duration = Duration::from_millis(150);
const IGNORED_PROJECT_DIRS: &[&str] = &[
    ".git",
    ".idea",
    ".next",
    ".nuxt",
    ".turbo",
    "coverage",
    "dist",
    "node_modules",
    "target",
    "vendor",
];

#[derive(serde::Serialize)]
struct ProjectFilesChanged {
    paths: Vec<String>,
    tree_changed: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

#[derive(Clone, serde::Serialize)]
struct ExternalEditor {
    id: &'static str,
    name: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    icon_data_url: Option<String>,
}

#[derive(Clone, Copy)]
struct ExternalEditorDefinition {
    id: &'static str,
    name: &'static str,
    launcher: &'static str,
    bundle_id: &'static str,
}

#[cfg(target_os = "macos")]
const EXTERNAL_EDITORS: &[ExternalEditorDefinition] = &[
    ExternalEditorDefinition {
        id: "vscode",
        name: "Visual Studio Code",
        launcher: "Visual Studio Code",
        bundle_id: "com.microsoft.VSCode",
    },
    ExternalEditorDefinition {
        id: "cursor",
        name: "Cursor",
        launcher: "Cursor",
        bundle_id: "com.todesktop.230313mzl4w4u92",
    },
    ExternalEditorDefinition {
        id: "windsurf",
        name: "Windsurf",
        launcher: "Windsurf",
        bundle_id: "com.exafunction.windsurf",
    },
    ExternalEditorDefinition {
        id: "zed",
        name: "Zed",
        launcher: "Zed",
        bundle_id: "dev.zed.Zed",
    },
    ExternalEditorDefinition {
        id: "sublime-text",
        name: "Sublime Text",
        launcher: "Sublime Text",
        bundle_id: "com.sublimetext.4",
    },
    ExternalEditorDefinition {
        id: "goland",
        name: "GoLand",
        launcher: "GoLand",
        bundle_id: "com.jetbrains.goland",
    },
    ExternalEditorDefinition {
        id: "intellij-idea",
        name: "IntelliJ IDEA",
        launcher: "IntelliJ IDEA",
        bundle_id: "com.jetbrains.intellij",
    },
    ExternalEditorDefinition {
        id: "webstorm",
        name: "WebStorm",
        launcher: "WebStorm",
        bundle_id: "com.jetbrains.WebStorm",
    },
    ExternalEditorDefinition {
        id: "pycharm",
        name: "PyCharm",
        launcher: "PyCharm",
        bundle_id: "com.jetbrains.pycharm",
    },
    ExternalEditorDefinition {
        id: "rustrover",
        name: "RustRover",
        launcher: "RustRover",
        bundle_id: "com.jetbrains.rustrover",
    },
];

#[cfg(not(target_os = "macos"))]
const EXTERNAL_EDITORS: &[ExternalEditorDefinition] = &[
    ExternalEditorDefinition {
        id: "vscode",
        name: "Visual Studio Code",
        launcher: "code",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "cursor",
        name: "Cursor",
        launcher: "cursor",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "windsurf",
        name: "Windsurf",
        launcher: "windsurf",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "zed",
        name: "Zed",
        launcher: "zed",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "sublime-text",
        name: "Sublime Text",
        launcher: "subl",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "goland",
        name: "GoLand",
        launcher: "goland",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "intellij-idea",
        name: "IntelliJ IDEA",
        launcher: "idea",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "webstorm",
        name: "WebStorm",
        launcher: "webstorm",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "pycharm",
        name: "PyCharm",
        launcher: "pycharm",
        bundle_id: "",
    },
    ExternalEditorDefinition {
        id: "rustrover",
        name: "RustRover",
        launcher: "rustrover",
        bundle_id: "",
    },
];

#[derive(serde::Serialize)]
struct ProjectEntry {
    path: String,
    name: String,
    is_dir: bool,
}

fn project_root(project_path: &str) -> Result<PathBuf, String> {
    let root = fs::canonicalize(project_path).map_err(|e| format!("无法访问项目目录: {e}"))?;
    if !root.is_dir() {
        return Err("项目路径不是目录".into());
    }
    Ok(root)
}

fn project_relative_path(relative_path: &str) -> Result<&Path, String> {
    let relative = Path::new(relative_path);
    if relative.as_os_str().is_empty()
        || relative
            .components()
            .any(|component| !matches!(component, Component::Normal(_)))
    {
        return Err("项目路径无效".into());
    }
    Ok(relative)
}

fn safe_project_entry(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let relative = project_relative_path(relative_path)?;
    let root = project_root(project_path)?;
    let candidate = root.join(relative);
    let metadata =
        fs::symlink_metadata(&candidate).map_err(|e| format!("无法访问项目条目: {e}"))?;
    if metadata.file_type().is_symlink() {
        return Err("不支持操作符号链接".into());
    }
    let entry = fs::canonicalize(candidate).map_err(|e| format!("无法访问项目条目: {e}"))?;
    if !entry.starts_with(&root) {
        return Err("项目条目不在当前项目中".into());
    }
    Ok(entry)
}

fn safe_project_file(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let file = safe_project_entry(project_path, relative_path)?;
    if !file.is_file() {
        return Err("项目条目不是文件".into());
    }
    Ok(file)
}

fn safe_project_destination(project_path: &str, relative_path: &str) -> Result<PathBuf, String> {
    let relative = project_relative_path(relative_path)?;
    let root = project_root(project_path)?;
    let candidate = root.join(relative);
    let file_name = candidate.file_name().ok_or("项目路径无效")?;
    let parent = candidate.parent().ok_or("项目路径无效")?;
    let parent = fs::canonicalize(parent).map_err(|e| format!("无法访问父目录: {e}"))?;
    if !parent.starts_with(&root) || !parent.is_dir() {
        return Err("父目录不在当前项目中".into());
    }
    let destination = parent.join(file_name);
    match fs::symlink_metadata(&destination) {
        Ok(_) => Err("同名文件或文件夹已存在".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(destination),
        Err(error) => Err(format!("无法检查目标路径: {error}")),
    }
}

fn safe_entry_name(name: &str) -> Result<&str, String> {
    let path = Path::new(name);
    let mut components = path.components();
    if name.is_empty()
        || !matches!(components.next(), Some(Component::Normal(_)))
        || components.next().is_some()
    {
        return Err("名称不能包含路径分隔符".into());
    }
    Ok(name)
}

fn project_relative_string(root: &Path, path: &Path) -> Result<String, String> {
    path.strip_prefix(root)
        .map(|relative| relative.to_string_lossy().replace('\\', "/"))
        .map_err(|_| "项目条目不在当前项目中".into())
}

fn external_editor_definition(id: &str) -> Option<ExternalEditorDefinition> {
    EXTERNAL_EDITORS
        .iter()
        .copied()
        .find(|editor| editor.id == id)
}

#[cfg(target_os = "macos")]
fn external_editor_path(editor: ExternalEditorDefinition) -> Option<PathBuf> {
    let application = format!("{}.app", editor.launcher);
    if let Some(path) = [
        Some(PathBuf::from("/Applications")),
        env::var_os("HOME")
            .map(PathBuf::from)
            .map(|home| home.join("Applications")),
    ]
    .into_iter()
    .flatten()
    .map(|directory| directory.join(&application))
    .find(|path| path.is_dir())
    {
        return Some(path);
    }

    let output = ProcessCommand::new("/usr/bin/mdfind")
        .arg(format!(
            "kMDItemCFBundleIdentifier == '{}'",
            editor.bundle_id
        ))
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    String::from_utf8_lossy(&output.stdout)
        .lines()
        .map(PathBuf::from)
        .find(|path| path.is_dir())
}

#[cfg(target_os = "macos")]
fn external_editor_icon_data_url(application: &Path) -> Option<String> {
    let info = application.join("Contents").join("Info.plist");
    let output = ProcessCommand::new("/usr/bin/defaults")
        .arg("read")
        .arg(info)
        .arg("CFBundleIconFile")
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    let raw_name = String::from_utf8(output.stdout).ok()?;
    let icon_name = Path::new(raw_name.trim()).file_name()?.to_owned();
    let mut icon = application
        .join("Contents")
        .join("Resources")
        .join(icon_name);
    if icon.extension().is_none() {
        icon.set_extension("icns");
    }
    if !icon.is_file() {
        return None;
    }

    let output_path = env::temp_dir().join(format!(
        "foya-editor-icon-{}-{}.png",
        std::process::id(),
        NEXT_EDITOR_ICON_ID.fetch_add(1, Ordering::Relaxed)
    ));
    let converted = ProcessCommand::new("/usr/bin/sips")
        .args(["-z", "32", "32", "-s", "format", "png"])
        .arg(&icon)
        .arg("--out")
        .arg(&output_path)
        .output()
        .ok()?;
    if !converted.status.success() {
        let _ = fs::remove_file(output_path);
        return None;
    }
    let png = fs::read(&output_path).ok();
    let _ = fs::remove_file(output_path);
    png.map(|bytes| {
        format!(
            "data:image/png;base64,{}",
            base64::engine::general_purpose::STANDARD.encode(bytes)
        )
    })
}

#[cfg(target_os = "macos")]
fn external_editor_available(editor: ExternalEditorDefinition) -> bool {
    external_editor_path(editor).is_some()
}

#[cfg(not(target_os = "macos"))]
fn external_editor_available(editor: ExternalEditorDefinition) -> bool {
    let Some(path) = env::var_os("PATH") else {
        return false;
    };
    env::split_paths(&path).any(|directory| {
        let candidate = directory.join(editor.launcher);
        if candidate.is_file() {
            return true;
        }
        #[cfg(target_os = "windows")]
        {
            return ["exe", "cmd", "bat"]
                .iter()
                .any(|extension| candidate.with_extension(extension).is_file());
        }
        #[cfg(not(target_os = "windows"))]
        false
    })
}

#[cfg(target_os = "macos")]
fn installed_external_editors() -> Vec<ExternalEditor> {
    EXTERNAL_EDITORS
        .iter()
        .copied()
        .filter_map(|editor| {
            let application = external_editor_path(editor)?;
            Some(ExternalEditor {
                id: editor.id,
                name: editor.name,
                icon_data_url: external_editor_icon_data_url(&application),
            })
        })
        .collect()
}

#[cfg(not(target_os = "macos"))]
fn installed_external_editors() -> Vec<ExternalEditor> {
    EXTERNAL_EDITORS
        .iter()
        .copied()
        .filter(|editor| external_editor_available(*editor))
        .map(|editor| ExternalEditor {
            id: editor.id,
            name: editor.name,
            icon_data_url: None,
        })
        .collect()
}

fn project_watch_path(root: &Path, path: &Path) -> Option<String> {
    let relative = path.strip_prefix(root).ok()?;
    let ignored = relative.components().any(|component| {
        let Component::Normal(name) = component else {
            return false;
        };
        IGNORED_PROJECT_DIRS
            .iter()
            .any(|ignored| name == std::ffi::OsStr::new(ignored))
    });
    if ignored {
        return None;
    }
    Some(relative.to_string_lossy().replace('\\', "/"))
}

fn project_event_changes_tree(kind: &EventKind) -> bool {
    matches!(
        kind,
        EventKind::Any
            | EventKind::Other
            | EventKind::Create(_)
            | EventKind::Remove(_)
            | EventKind::Modify(notify::event::ModifyKind::Name(_))
    )
}

fn merge_project_watch_event(
    root: &Path,
    event: Result<NotifyEvent, notify::Error>,
    paths: &mut HashSet<String>,
    tree_changed: &mut bool,
    error: &mut Option<String>,
) {
    match event {
        Ok(event) => {
            if matches!(event.kind, EventKind::Access(_)) {
                return;
            }
            *tree_changed |= project_event_changes_tree(&event.kind);
            for path in event.paths {
                if let Some(relative) = project_watch_path(root, &path) {
                    paths.insert(relative);
                }
            }
        }
        Err(cause) => {
            *tree_changed = true;
            *error = Some(cause.to_string());
        }
    }
}

fn collect_project_entries(
    root: &Path,
    directory: &Path,
    entries: &mut Vec<ProjectEntry>,
) -> Result<(), String> {
    if entries.len() >= MAX_PROJECT_ENTRIES {
        return Ok(());
    }
    let mut children = fs::read_dir(directory)
        .map_err(|e| format!("无法读取项目目录: {e}"))?
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| format!("无法读取项目目录项: {e}"))?;
    children.sort_by_key(|entry| entry.file_name().to_string_lossy().to_lowercase());

    for child in children {
        if entries.len() >= MAX_PROJECT_ENTRIES {
            break;
        }
        let file_type = child
            .file_type()
            .map_err(|e| format!("无法读取文件类型: {e}"))?;
        if file_type.is_symlink() {
            continue;
        }
        let name = child.file_name().to_string_lossy().into_owned();
        if file_type.is_dir() && IGNORED_PROJECT_DIRS.contains(&name.as_str()) {
            continue;
        }
        let path = child.path();
        let relative = path
            .strip_prefix(root)
            .map_err(|e| e.to_string())?
            .to_string_lossy()
            .replace('\\', "/");
        entries.push(ProjectEntry {
            path: relative,
            name,
            is_dir: file_type.is_dir(),
        });
        if file_type.is_dir() {
            collect_project_entries(root, &path, entries)?;
        }
    }
    Ok(())
}

#[tauri::command]
pub(crate) async fn list_external_editors() -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(|| {
        serde_json::to_string(&installed_external_editors()).map_err(|error| error.to_string())
    })
    .await
    .map_err(|error| error.to_string())?
}

#[tauri::command]
pub(crate) async fn open_project_in_external_editor(
    project_path: String,
    editor_id: String,
) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let editor = external_editor_definition(&editor_id)
            .filter(|editor| external_editor_available(*editor))
            .ok_or_else(|| "外部编辑器未安装或不可用".to_string())?;
        tauri_plugin_opener::open_path(root, Some(editor.launcher))
            .map_err(|error| format!("无法打开外部编辑器: {error}"))
    })
    .await
    .map_err(|error| error.to_string())?
}

#[tauri::command]
pub(crate) fn watch_project_files(
    project_path: String,
    channel: Channel<String>,
) -> Result<String, String> {
    let root = project_root(&project_path)?;
    let (event_tx, event_rx) = mpsc::channel();
    let mut watcher = RecommendedWatcher::new(
        move |event| {
            let _ = event_tx.send(event);
        },
        notify::Config::default(),
    )
    .map_err(|error| format!("无法创建文件监听器: {error}"))?;
    watcher
        .watch(&root, RecursiveMode::Recursive)
        .map_err(|error| format!("无法监听项目目录: {error}"))?;

    let watch_id = format!(
        "project-watch-{}",
        NEXT_PROJECT_FILE_WATCHER_ID.fetch_add(1, Ordering::Relaxed)
    );
    let stop = Arc::new(AtomicBool::new(false));
    PROJECT_FILE_WATCHERS
        .lock()
        .map_err(|_| "文件监听器状态已损坏".to_string())?
        .insert(watch_id.clone(), Arc::clone(&stop));

    let thread_watch_id = watch_id.clone();
    thread::Builder::new()
        .name(thread_watch_id.clone())
        .spawn(move || {
            let _watcher = watcher;
            while !stop.load(Ordering::Acquire) {
                let first = match event_rx.recv_timeout(PROJECT_FILE_EVENT_BATCH) {
                    Ok(event) => event,
                    Err(RecvTimeoutError::Timeout) => continue,
                    Err(RecvTimeoutError::Disconnected) => break,
                };

                let mut paths = HashSet::new();
                let mut tree_changed = false;
                let mut error = None;
                merge_project_watch_event(&root, first, &mut paths, &mut tree_changed, &mut error);

                let deadline = Instant::now() + PROJECT_FILE_EVENT_BATCH;
                loop {
                    let remaining = deadline.saturating_duration_since(Instant::now());
                    if remaining.is_zero() {
                        break;
                    }
                    match event_rx.recv_timeout(remaining) {
                        Ok(event) => merge_project_watch_event(
                            &root,
                            event,
                            &mut paths,
                            &mut tree_changed,
                            &mut error,
                        ),
                        Err(RecvTimeoutError::Timeout) => break,
                        Err(RecvTimeoutError::Disconnected) => {
                            stop.store(true, Ordering::Release);
                            break;
                        }
                    }
                }

                if paths.is_empty() && error.is_none() {
                    continue;
                }
                let mut paths = paths.into_iter().collect::<Vec<_>>();
                paths.sort();
                let payload = ProjectFilesChanged {
                    paths,
                    tree_changed,
                    error,
                };
                let Ok(payload) = serde_json::to_string(&payload) else {
                    continue;
                };
                if channel.send(payload).is_err() {
                    break;
                }
            }
            if let Ok(mut watchers) = PROJECT_FILE_WATCHERS.lock() {
                watchers.remove(&thread_watch_id);
            }
        })
        .map_err(|error| {
            if let Ok(mut watchers) = PROJECT_FILE_WATCHERS.lock() {
                watchers.remove(&watch_id);
            }
            format!("无法启动文件监听器: {error}")
        })?;

    Ok(watch_id)
}

#[tauri::command]
pub(crate) fn unwatch_project_files(watch_id: String) -> Result<(), String> {
    let stop = PROJECT_FILE_WATCHERS
        .lock()
        .map_err(|_| "文件监听器状态已损坏".to_string())?
        .remove(&watch_id);
    if let Some(stop) = stop {
        stop.store(true, Ordering::Release);
    }
    Ok(())
}

/// 内核 Unix socket 路径,须与 Go 端 config.DefaultSocketPath 保持一致。

#[tauri::command]
pub(crate) async fn list_project_files(project_path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let mut entries = Vec::new();
        collect_project_entries(&root, &root, &mut entries)?;
        serde_json::to_string(&entries).map_err(|e| e.to_string())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub(crate) async fn read_project_file(
    project_path: String,
    path: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let file = safe_project_file(&project_path, &path)?;
        let metadata = fs::metadata(&file).map_err(|e| format!("无法读取文件信息: {e}"))?;
        if metadata.len() > MAX_PREVIEW_BYTES {
            return Err("文件超过 2 MiB，无法预览".into());
        }
        let bytes = fs::read(file).map_err(|e| format!("无法读取文件: {e}"))?;
        String::from_utf8(bytes).map_err(|_| "二进制文件暂不支持预览".into())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub(crate) async fn create_project_file(
    project_path: String,
    path: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let file = safe_project_destination(&project_path, &path)?;
        fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&file)
            .map_err(|e| format!("无法创建文件: {e}"))?;
        project_relative_string(&root, &file)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub(crate) async fn create_project_directory(
    project_path: String,
    path: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let directory = safe_project_destination(&project_path, &path)?;
        fs::create_dir(&directory).map_err(|e| format!("无法创建文件夹: {e}"))?;
        project_relative_string(&root, &directory)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub(crate) async fn rename_project_entry(
    project_path: String,
    path: String,
    new_name: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let root = project_root(&project_path)?;
        let entry = safe_project_entry(&project_path, &path)?;
        let new_name = safe_entry_name(&new_name)?;
        let destination = entry.with_file_name(new_name);

        if destination != entry {
            if let Ok(existing) = fs::canonicalize(&destination) {
                if existing != entry {
                    return Err("同名文件或文件夹已存在".into());
                }
            }
            fs::rename(&entry, &destination).map_err(|e| format!("无法重命名: {e}"))?;
        }

        project_relative_string(&root, &destination)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub(crate) async fn delete_project_entry(project_path: String, path: String) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let entry = safe_project_entry(&project_path, &path)?;
        if entry.is_dir() {
            fs::remove_dir_all(entry).map_err(|e| format!("无法删除文件夹: {e}"))
        } else {
            fs::remove_file(entry).map_err(|e| format!("无法删除文件: {e}"))
        }
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub(crate) async fn resolve_project_path(
    project_path: String,
    path: String,
) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let entry = if path.is_empty() {
            project_root(&project_path)?
        } else {
            safe_project_entry(&project_path, &path)?
        };
        Ok(entry.to_string_lossy().into_owned())
    })
    .await
    .map_err(|e| e.to_string())?
}

#[cfg(test)]
mod project_file_watcher_tests {
    use super::*;
    use notify::event::{AccessKind, CreateKind, DataChange, ModifyKind, RenameMode};

    #[test]
    fn project_watch_path_filters_ignored_directories() {
        let root = Path::new("/workspace/project");
        assert_eq!(
            project_watch_path(root, Path::new("/workspace/project/src/main.rs")),
            Some("src/main.rs".to_string())
        );
        assert_eq!(
            project_watch_path(
                root,
                Path::new("/workspace/project/node_modules/pkg/index.js")
            ),
            None
        );
        assert_eq!(
            project_watch_path(root, Path::new("/workspace/other/main.rs")),
            None
        );
    }

    #[test]
    fn project_watch_event_only_marks_structure_changes() {
        assert!(!project_event_changes_tree(&EventKind::Modify(
            ModifyKind::Data(DataChange::Content)
        )));
        assert!(project_event_changes_tree(&EventKind::Modify(
            ModifyKind::Name(RenameMode::Both)
        )));
        assert!(project_event_changes_tree(&EventKind::Create(
            CreateKind::File
        )));
    }

    #[test]
    fn project_watch_ignores_file_access_events() {
        let root = Path::new("/workspace/project");
        let mut paths = HashSet::new();
        let mut tree_changed = false;
        let mut error = None;
        merge_project_watch_event(
            root,
            Ok(NotifyEvent::new(EventKind::Access(AccessKind::Read))
                .add_path(root.join("src/main.rs"))),
            &mut paths,
            &mut tree_changed,
            &mut error,
        );
        assert!(paths.is_empty());
        assert!(!tree_changed);
        assert!(error.is_none());
    }

    #[test]
    fn external_editor_definitions_have_unique_ids() {
        let mut ids = HashSet::new();
        for editor in EXTERNAL_EDITORS {
            assert!(!editor.id.is_empty());
            assert!(!editor.name.is_empty());
            assert!(!editor.launcher.is_empty());
            assert!(ids.insert(editor.id), "duplicate editor id: {}", editor.id);
        }
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn installed_editor_icons_are_loaded_from_application_bundles() {
        let zed = Path::new("/Applications/Zed.app");
        if !zed.is_dir() {
            return;
        }
        let icon = external_editor_icon_data_url(zed).expect("read Zed application icon");
        assert!(icon.starts_with("data:image/png;base64,"));
    }
}
