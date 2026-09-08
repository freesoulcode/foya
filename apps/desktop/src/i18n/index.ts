import { computed, ref } from "vue";
import { createI18n } from "vue-i18n";
import { zhCN } from "./zh-CN";

export type AppLocale = "system" | "zh-CN" | "en-US";

const STORAGE_KEY = "foya.locale";

function systemLocale(): "zh-CN" | "en-US" {
  return navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en-US";
}

function storedLocale(): AppLocale {
  const value = localStorage.getItem(STORAGE_KEY);
  return value === "zh-CN" || value === "en-US" ? value : "system";
}

function resolvedLocale(value: AppLocale): "zh-CN" | "en-US" {
  return value === "system" ? systemLocale() : value;
}

const preference = ref<AppLocale>(storedLocale());
const enUS = Object.fromEntries(
  Object.keys(zhCN).map((key) => [key, key])
) as Record<string, string>;

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: resolvedLocale(preference.value),
  fallbackLocale: "en-US",
  messageResolver: (messages, key): string | null => {
    const value =
      typeof messages === "object" && messages !== null
        ? (messages as Record<string, unknown>)[key]
        : null;
    return typeof value === "string" ? value : null;
  },
  fallbackFormat: true,
  missingWarn: false,
  fallbackWarn: false,
  messages: {
    "en-US": enUS,
    "zh-CN": zhCN,
  },
});

function applyDocumentLocale() {
  document.documentElement.lang = i18n.global.locale.value;
}

export function initializeLocale() {
  applyDocumentLocale();
  window.addEventListener("languagechange", () => {
    if (preference.value !== "system") return;
    i18n.global.locale.value = systemLocale();
    applyDocumentLocale();
  });
}

export function translate(
  key: string,
  values: Record<string, string | number> = {}
): string {
  return i18n.global.t(key, values);
}

const ERROR_KEYS: Record<string, string> = {
  bad_request: "Invalid request",
  internal_error: "Internal error",
  unsupported_channel: "Unsupported channel",
  channel_create_failed: "Failed to create channel",
  feishu_registration_failed: "Failed to register Feishu Bot",
  channel_update_failed: "Failed to update channel",
  channel_delete_failed: "Failed to delete channel",
  automation_create_failed: "Failed to create automation",
  automation_update_failed: "Failed to update automation",
  automation_delete_failed: "Failed to delete automation",
  automation_run_failed: "Failed to run automation",
  automation_not_found: "Automation not found",
  automation_running: "Automation is already running",
  automations_unavailable: "Automations are unavailable",
  invalid_context_item: "Invalid context item",
  context_item_failed: "Failed to update context item",
  agent_limits_failed: "Failed to load agent limits",
  agent_limits_update_failed: "Failed to update agent limits",
  agent_run_failed: "Failed to start agent",
  agents_failed: "Failed to load agents",
  mcp_failed: "MCP operation failed",
  mcp_prompt_get_failed: "Failed to load MCP prompt",
  mcp_prompts_failed: "Failed to load MCP prompts",
  mcp_registry_failed: "Failed to load MCP registry",
  mcp_resource_read_failed: "Failed to read MCP resource",
  mcp_resources_failed: "Failed to load MCP resources",
  plugins_failed: "Failed to load plugins",
  plugin_install_failed: "Failed to install plugin",
  plugin_marketplaces_failed: "Failed to load plugin marketplaces",
  plugin_marketplace_add_failed: "Failed to add plugin marketplace",
  plugin_marketplace_update_failed: "Failed to update plugin marketplace",
  plugin_marketplace_refresh_failed: "Failed to refresh plugin marketplace",
  plugin_marketplace_remove_failed: "Failed to remove plugin marketplace",
  plugin_marketplace_failed: "Failed to load plugin marketplace",
  plugin_preview_failed: "Failed to preview plugin",
  plugin_update_failed: "Failed to update plugin",
  plugin_remove_failed: "Failed to remove plugin",
  mcp_update_failed: "Failed to update MCP configuration",
  skills_failed: "Unable to load skills",
  project_create_failed: "Failed to create project",
  project_update_failed: "Failed to update project",
  project_delete_failed: "Failed to delete project",
  projects_failed: "Failed to load projects",
  project_locked: "The chat project cannot be changed",
  skill_update_failed: "Failed to update skill",
  web_search_failed: "Web search operation failed",
  web_search_update_failed: "Failed to update web search",
  invalid_reasoning_effort: "Invalid reasoning effort",
  invalid_approval_mode: "Invalid approval mode",
  connection_not_found: "Connection not found",
  project_not_found: "Project not found",
  create_failed: "Failed to create chat",
  invalid_fork_boundary: "Invalid fork point",
  fork_failed: "Failed to fork chat",
  rename_failed: "Failed to rename chat",
  pin_failed: "Failed to update chat pin",
  update_failed: "Failed to update chat",
  unsupported_auth_kind: "Unsupported authentication type",
  connection_update_failed: "Failed to update connection",
  connection_delete_failed: "Failed to delete connection",
  default_models_failed: "Failed to update default models",
  missing_asset: "Missing asset",
  asset_too_large: "Asset is too large",
  canvas_failed: "Canvas operation failed",
  canvas_not_found: "Canvas not found",
  canvas_revision_conflict: "The canvas changed. Reload and try again.",
  canvas_asset_too_large: "Canvas asset is too large",
  unsupported_canvas_media: "Unsupported canvas media type",
  generation_provider_failed: "Generation provider failed",
  delete_failed: "Delete operation failed",
  children_failed: "Failed to load child chats",
  history_failed: "Failed to load chat history",
  invalid_artifact: "Invalid artifact",
  missing_artifact: "Missing artifact",
  artifact_committed: "Artifact is already committed",
  artifact_not_found: "Artifact not found",
  artifact_store_failed: "Failed to store artifact",
  artifact_read_failed: "Failed to read artifact",
  artifact_delete_failed: "Failed to delete artifact",
  usage_failed: "Failed to load usage",
  invalid_range: "Invalid statistics range",
  usage_statistics_failed: "Failed to load usage statistics",
  file_review_failed: "File review operation failed",
  no_pending_file_changes: "There are no pending file changes",
  file_changed: "The file changed after the agent edit",
  file_review_changed: "The pending file review changed",
  file_state_changed: "The file state changed",
  invalid_action: "Invalid action",
  invalid_message_seq: "Invalid message",
  message_not_found: "Message not found",
  history_changed: "Chat history changed",
  rewind_context_unsupported: "This message cannot be rewound",
  file_rewind_failed: "Failed to restore files",
  rewind_failed: "Failed to rewind chat",
  tool_cancel_failed: "Failed to stop tool",
  tool_background_failed: "Failed to move command to background",
  tool_reveal_failed: "Failed to open command in terminal",
  background_commands_failed: "Failed to load background commands",
  background_command_failed: "Failed to load background command",
  background_command_not_found: "Background command not found",
  background_command_cancel_failed: "Failed to stop background command",
  invalid_approval_decision: "Invalid approval decision",
  invalid_question_answers: "Invalid answers",
  question_unavailable: "Question is no longer available",
  question_not_found: "Question not found",
  invalid_queue_message: "Invalid queued message",
  image_input_unsupported: "The selected model does not support image input",
  queue_failed: "Queue operation failed",
  queue_not_empty: "The message queue is not empty",
  queued_message_not_found: "Queued message not found",
  terminal_failed: "Terminal operation failed",
  terminal_not_found: "Terminal not found",
  terminal_unavailable: "Terminal is unavailable",
  tool_not_running: "Tool is not running",
  session_busy: "The chat is busy",
  session_not_found: "Chat not found",
  not_found: "Resource not found",
  cancelled: "Operation cancelled",
  compaction_failed: "Compaction failed",
  compaction_unavailable: "Compaction is unavailable",
  nothing_to_compact: "There is nothing to compact",
  connection_create_failed: "Failed to create connection",
  connection_in_use: "Connection is in use",
  models_failed: "Unable to load models",
  channels_unavailable: "Messaging channels are unavailable",
  channel_not_found: "Messaging channel not found",
  feishu_registration_not_found: "Feishu registration not found",
  browser_action_failed: "Browser action failed",
  browser_screenshot_too_large: "Browser screenshot is too large",
  image_too_large: "Image is too large",
  unsupported_image: "Unsupported image",
  web_search_test_failed: "Web search test failed",
  no_flush: "Streaming is unavailable",
};

const NATIVE_ERROR_PREFIXES = [
  "Unable to resolve kernel socket path",
  "Failed to build request",
  "Failed to connect to kernel",
  "Failed to read response",
  "Subscription failed",
  "Failed to read event stream",
  "Attachment response is not UTF-8",
  "Canvas asset response is not UTF-8",
  "Event subscription ended before connecting",
  "Canvas event subscription ended before connecting",
  "Terminal subscription ended before connecting",
  "Unknown context type",
  "Invalid browser instance ID",
  "Invalid element source URL",
  "Element sources must use HTTP or HTTPS",
  "Selected page element information is incomplete",
  "Only HTTP or HTTPS URLs can be opened",
  "Invalid browser preview bounds",
  "Main window is unavailable",
  "Browser page is not open",
  "Unable to update element picker state",
  "Webview screenshot failed",
  "Unable to read Webview screenshot",
  "Unable to convert Webview screenshot",
  "Unable to encode Webview screenshot",
  "Webview screenshot timed out",
  "Webview screenshot channel closed",
  "Webview screenshots are not supported on this platform",
  "Invalid browser action ID",
  "Unable to create browser action",
  "Browser action channel closed",
  "Browser action timed out",
  "Unable to read browser action",
  "Browser action already ended",
  "Unable to access project",
  "Project path is not a directory",
  "Invalid project path",
  "Unable to access project entry",
  "Symbolic links are not supported",
  "Project entry is outside the current project",
  "Project entry is not a file",
  "Unable to access parent directory",
  "Parent directory is outside the current project",
  "A file or folder with the same name already exists",
  "Unable to inspect destination path",
  "Name cannot contain path separators",
  "Unable to read project directory",
  "Unable to read project directory entry",
  "Unable to read file type",
  "External editor is not installed or available",
  "Unable to open external editor",
  "Unable to create file watcher",
  "Unable to watch project directory",
  "File watcher state is unavailable",
  "Unable to start file watcher",
  "Unable to read file metadata",
  "Files larger than 2 MiB cannot be previewed",
  "Unable to read file",
  "Binary file previews are not supported",
  "Unable to create file",
  "Unable to create folder",
  "Unable to rename",
  "Unable to delete file",
  "Unable to delete folder",
  "SSH host is required",
  "SSH host contains unsupported characters",
  "SSH port must be between 1 and 65535",
  "Failed to run SSH",
  "Failed to start SSH tunnel",
  "Remote kernel did not become ready",
  "Remote server requires Bubblewrap",
  "Remote kernel requires Linux",
  "Unsupported remote Linux architecture",
  "Remote kernel resource is missing",
  "Invalid remote kernel resource",
  "Unable to resolve remote kernel cache",
  "Unable to download remote kernel",
  "Invalid downloaded remote kernel",
  "Unable to cache remote kernel",
] as const;

function errorPayload(raw: string): { code?: string; message?: string } | null {
  const start = raw.indexOf("{");
  if (start < 0) return null;
  try {
    const value = JSON.parse(raw.slice(start)) as unknown;
    return value && typeof value === "object"
      ? (value as { code?: string; message?: string })
      : null;
  } catch {
    return null;
  }
}

export function localizeError(error: unknown): string {
  const raw = error instanceof Error ? error.message : String(error);
  const payload = errorPayload(raw);
  if (payload?.code) {
    const key = ERROR_KEYS[payload.code] ?? "Request failed";
    const summary = translate(key);
    if (i18n.global.locale.value === "en-US" && payload.message) {
      return `${summary}: ${payload.message}`;
    }
    return summary;
  }
  if (raw.includes("Failed to connect to kernel")) {
    return translate("Unable to connect to the kernel");
  }
  for (const prefix of NATIVE_ERROR_PREFIXES) {
    if (raw.startsWith(prefix)) {
      return `${translate(prefix)}${raw.slice(prefix.length)}`;
    }
  }
  return raw;
}

const RUNTIME_TEXT_PREFIXES = [
  "Call MCP tool",
  "Control built-in browser:",
  "Browser access:",
  "Edit file:",
  "Move to Trash:",
  "Web search:",
  "Read web page:",
  "Write file:",
] as const;

export function localizeRuntimeText(value: string): string {
  for (const prefix of RUNTIME_TEXT_PREFIXES) {
    if (value.startsWith(prefix)) {
      return `${translate(prefix)}${value.slice(prefix.length)}`;
    }
  }
  return value;
}

export function useLocale() {
  const locale = computed(() => preference.value);
  const resolved = computed(() => i18n.global.locale.value);

  function setLocale(value: AppLocale) {
    preference.value = value;
    localStorage.setItem(STORAGE_KEY, value);
    i18n.global.locale.value = resolvedLocale(value);
    applyDocumentLocale();
  }

  return { locale, resolvedLocale: resolved, setLocale };
}
