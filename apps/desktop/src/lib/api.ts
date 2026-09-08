// Kernel API wrapper. Components and composables depend only on this module.
import { invoke as tauriInvoke, Channel } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { localizeError, translate } from "@/i18n";

function invoke<T>(
  command: string,
  args?: Parameters<typeof tauriInvoke>[1]
): Promise<T> {
  return tauriInvoke<T>(command, args).catch((error) =>
    Promise.reject(localizeError(error))
  );
}

export interface KernelConnection {
  mode: "local" | "ssh" | "remote";
  url: string;
  has_token: boolean;
  ssh?: SshConnection;
}

export interface KernelConnectionInput {
  mode: "local" | "remote";
  url: string;
  token?: string;
}

export interface SshConnection {
  name: string;
  target: string;
  port: number;
  remote_port: number;
}

export interface SshHostInput {
  name?: string;
  target: string;
  port: number;
}

export interface SshHostProbe {
  os: string;
  architecture: string;
  sandbox_available: boolean;
}

export interface SshConfigHost {
  alias: string;
  hostname: string;
  user: string;
  port: number;
}

// Session metadata aligned with the public subset of Go session.Session.
export type TaskStatus = "pending" | "in_progress" | "completed";

export interface SessionTask {
  content: string;
  status: TaskStatus;
}

export interface Session {
  id: string;
  parent_id?: string;
  spawned_by?: {
    parent_run_id?: string;
    parent_turn_id?: string;
    parent_tool_call_id: string;
  };
  agent_ref?: string;
  agent_name?: string;
  agent_digest?: string;
  phase: string;
  agent_mode?: "execute" | "plan" | "plan_ready";
  connection_id: string;
  model: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
  title?: string;
  title_is_manual?: boolean;
  pinned?: boolean;
  pinned_at?: string;
  tasks?: SessionTask[];
  created_at: string;
  updated_at: string;
}

// Reasoning effort aligned with session.ReasoningEffort. Empty uses the provider default.
export type ReasoningEffort = "" | "low" | "medium" | "high";

// Options a user can choose before creating a chat.
export interface CreateSessionOptions {
  connection_id?: string;
  model?: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
}

export interface ForkSessionOptions {
  title?: string;
  through_seq?: number;
}

// Partial session update. Undefined leaves a field unchanged.
export interface UpdateSessionPatch {
  connection_id?: string;
  model?: string;
  reasoning_effort?: ReasoningEffort;
  project_id?: string;
  approval_mode?: ApprovalMode;
  title?: string;
  pinned?: boolean;
}

// Kernel-owned queued message. Position is zero-based.
export interface QueuedMessage {
  id: string;
  session_id: string;
  text: string;
  skill_ref?: string;
  attachments?: AttachmentRef[];
  browser_elements?: BrowserElementSelection[];
  position: number;
  created_at: string;
  updated_at: string;
}

export interface SubmitTurnResult {
  run_id?: string;
  status: "started" | "queued";
  queued?: QueuedMessage;
}

export interface AttachmentRef {
  id: string;
  name: string;
  kind: "image";
  media_type: string;
  bytes: number;
  width?: number;
  height?: number;
  sha256?: string;
}

export type CanvasNodeType =
  | "image"
  | "video"
  | "text"
  | "generation";

export interface CanvasGenerationSpec {
  connection_id?: string;
  mode: "image" | "video";
  model?: string;
  aspect_ratio?: string;
  quality?: string;
  count?: number;
  duration?: number;
}

export interface CanvasNode {
  id: string;
  type: CanvasNodeType;
  title?: string;
  asset_id?: string;
  text?: string;
  prompt?: string;
  parent_id?: string;
  status?: "idle" | "queued" | "running" | "success" | "error";
  error?: string;
  generation?: CanvasGenerationSpec;
  x: number;
  y: number;
  width: number;
  height: number;
  rotation?: number;
  z_index: number;
}

export interface CanvasEdge {
  id: string;
  from_node_id: string;
  to_node_id: string;
  kind?: "reference" | "variation" | "output";
}

export interface CanvasViewport {
  x: number;
  y: number;
  zoom: number;
}

export interface CanvasAsset {
  id: string;
  name: string;
  kind: "image" | "video";
  media_type: string;
  bytes: number;
  width?: number;
  height?: number;
  sha256: string;
  created_at: string;
}

export interface CanvasDocument {
  id: string;
  title: string;
  session_id?: string;
  project_id?: string;
  revision: number;
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  assets: CanvasAsset[];
  viewport: CanvasViewport;
  background: "dots" | "grid" | "blank";
  created_at: string;
  updated_at: string;
}

export interface RewindFile {
  key: string;
  path: string;
  status: "ready" | "mergeable" | "modified";
  additions: number;
  deletions: number;
  diff: string;
}

export interface RewindTurnResult {
  status: "rewound" | "confirmation_required";
  message: string;
  files?: RewindFile[];
  file_state_token: string;
  head_seq: number;
}

export interface FileReview {
  files: RewindFile[];
  file_state_token: string;
  through_seq: number;
}

export interface UpdateQueuedMessagePatch {
  message?: string;
  position?: number;
}

export interface ContextUsage {
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens: number;
}

export interface DailyUsage {
  date: string;
  message_count: number;
  token_count: number;
}

export interface ModelUsage {
  model: string;
  token_count: number;
  request_count: number;
  share: number;
}

export interface UsageStatistics {
  range_days: 7 | 30;
  total_tokens: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  session_count: number;
  message_count: number;
  active_days: number;
  current_streak: number;
  most_used_model?: string;
  most_used_model_share: number;
  activity: DailyUsage[];
  model_usage: ModelUsage[];
}

export interface CompactSessionResult {
  checkpoint_id?: string;
  phase?: "standalone" | "pre_turn" | "mid_turn";
  projection_kind?: "text" | "provider_native";
  level?: "segmented" | "session";
  segment_count?: number;
  through_seq: number;
  estimated_tokens_before: number;
  estimated_tokens_after: number;
}

export interface ConnectionModelCatalog {
  models: string[];
  context_windows: Record<string, number>;
  capabilities?: Record<string, { image_input?: boolean }>;
}

export interface ModelSettings {
  context_window?: number;
  max_input_tokens?: number;
  max_output_tokens?: number;
  image_input: boolean;
  image_generation: boolean;
  video_generation: boolean;
  audio_generation: boolean;
  tool_calling: boolean;
  web_search: boolean;
  reasoning_efforts?: ReasoningEffort[];
}

export interface ConnectionModelGroup extends ConnectionConfig {
  models: string[];
  context_windows: Record<string, number>;
  models_error?: string;
}

export interface TerminalResource {
  ref: string;
  session_id: string;
  running: boolean;
  exit_code?: number;
  buffer?: string;
  seq: number;
}

export interface TerminalDataEvent {
  session_id: string;
  ref: string;
  seq: number;
  data?: string;
  exited?: boolean;
  exit_code?: number;
}

export interface BackgroundCommand {
  command_id: string;
  session_id: string;
  command: string;
  pid?: number;
  running: boolean;
  exit_code?: number;
  stdout?: string;
  stderr?: string;
  started_at: string;
  backgrounded_by?: "agent" | "user";
  stopped_by?: "agent" | "user";
}

export interface BrowserViewport {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface BrowserElementSelection {
  page_url: string;
  page_title: string;
  tag: string;
  selector: string;
  text: string;
  html: string;
}

export interface BrowserActionRequest {
  id: string;
  session_id: string;
  tool_call_id?: string;
  browser_id: string;
  action:
    | "open"
    | "navigate"
    | "snapshot"
    | "click"
    | "type"
    | "press_key"
    | "scroll"
    | "wait"
    | "extract"
    | "back"
    | "reload"
    | "screenshot";
  url?: string;
  ref?: string;
  observation_id?: string;
  text?: string;
  key?: string;
  direction?: "up" | "down" | "left" | "right";
  amount?: number;
  timeout_ms?: number;
  clear?: boolean;
  full_page?: boolean;
  created_at: string;
}

export interface BrowserActionResult {
  url?: string;
  title?: string;
  revision?: number;
  observation_id?: string;
  snapshot?: string;
  screenshot_base64?: string;
  media_type?: string;
  code?: string;
  message?: string;
  pre_url?: string;
  post_url?: string;
  verified?: boolean;
  actual_text?: string;
  trace?: Record<string, unknown>;
  error?: string;
}

export interface ProjectEntry {
  path: string;
  name: string;
  is_dir: boolean;
}

export interface ProjectFilesChanged {
  paths: string[];
  tree_changed: boolean;
  error?: string;
}

export interface ExternalEditor {
  id: string;
  name: string;
  icon_data_url?: string;
}

// Approval modes aligned with Go approval.Mode.
export type ApprovalMode = "manual" | "auto" | "full_access";
export type ApprovalDecision =
  | "approved"
  | "approved_for_session"
  | "denied";

export interface QuestionOption {
  label: string;
  description?: string;
  recommended?: boolean;
}

export interface UserQuestion {
  id: string;
  question: string;
  description?: string;
  options?: QuestionOption[];
  allow_custom?: boolean;
}

export interface PendingQuestionBatch {
  id: string;
  session_id: string;
  run_id?: string;
  tool_call_id?: string;
  questions: UserQuestion[];
  created_at: string;
}

export interface QuestionAnswer {
  question_id: string;
  value: string;
}

// Chat message aligned with Go message.Message.
// Error is frontend-only optimistic state and is never persisted by the backend.
export interface ToolCallView {
  id: string;
  name: string;
  input: string;
  status: "queued" | "running" | "done" | "error";
  output?: string;
  child_session_id?: string;
  agent_ref?: string;
  agent_name?: string;
  agent_run?: AgentRunSnapshot;
  child_messages?: ChatMessage[];
  // Unified file diff for write and edit tools, used only for UI rendering.
  diff?: string;
  attachments?: AttachmentRef[];
}

// Ordered assistant segments preserve interleaved reasoning, tools, and text.
export type MessageSegment =
  | { kind: "reasoning"; text: string }
  | { kind: "text"; text: string }
  | { kind: "tool"; tool: ToolCallView };

export interface ChatMessage {
  role: "user" | "assistant" | "system" | "tool";
  content: string;
  command?: string;
  skill_ref?: string;
  attachments?: AttachmentRef[];
  browser_elements?: BrowserElementSelection[];
  event_seq?: number;
  reasoning?: string;
  tool_calls?: ToolCallView[];
  // Ordered assistant segments take precedence over legacy flat fields.
  segments?: MessageSegment[];
  tool_call_id?: string;
  // File diff on historical tool messages, used to rebuild tool segments.
  diff?: string;
  // Turn lifecycle timestamps carried by the final assistant message.
  turn_started_at?: string;
  turn_completed_at?: string;
  turn_status?: "completed" | "failed" | "cancelled";
  turn_reason?: string;
  error?: boolean;
}

// A connection is one model account or endpoint. API keys are write-only.
export type ConnectionType = "language" | "image" | "video";
export type VideoProtocol = "seedance" | "minimax_h3";

export interface ConnectionConfig {
  id?: string;
  name: string;
  type: ConnectionType;
  video_protocol?: VideoProtocol;
  kind: string;
  auth_kind: "api_key";
  base_url: string;
  model_settings: Record<string, ModelSettings>;
  models?: string[];
  models_cached?: boolean;
  api_key?: string;
  has_api_key?: boolean;
  sort_order: number;
}

export interface ModelRef {
  connection_id: string;
  model: string;
}

export interface DefaultModels {
  language: ModelRef;
  fast: ModelRef;
  image: ModelRef;
  video: ModelRef;
}

export interface SkillInfo {
  ref: string;
  name: string;
  description?: string;
  scope: "builtin" | "plugin" | "global" | "user" | "project";
  path?: string;
  root?: string;
  main_path?: string;
  enabled: boolean;
  pinned?: boolean;
  allowed_tools?: string[];
  required_tools?: string[];
  required_capabilities?: string[];
  resources?: SkillResourceInfo[];
  content_hash?: string;
  diagnostics?: SkillDiagnostic[];
}

export interface SkillResourceInfo {
  path: string;
  size?: number;
  media_type?: string;
}

export interface SkillDiagnostic {
  ref?: string;
  name?: string;
  path?: string;
  code: string;
  severity: string;
  message: string;
  field?: string;
}

export interface AgentInfo {
  ref: string;
  name: string;
  description: string;
  scope: "builtin" | "user" | "project";
  path?: string;
  model?: string;
  tools?: string[];
  max_turns?: number;
  timeout?: string;
  digest: string;
}

export type AgentRunStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "interrupted";

export interface AgentRunSnapshot {
  id: string;
  status: AgentRunStatus;
  root_session_id: string;
  root_run_id: string;
  parent_session_id: string;
  parent_tool_call_id?: string;
  child_session_id?: string;
  agent_ref?: string;
  agent_name?: string;
  task: string;
  depth: number;
  tokens_used?: number;
  output?: string;
  error?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface AgentBudget {
  root_session_id: string;
  root_run_id: string;
  tokens_used: number;
  max_tokens?: number;
  exceeded: boolean;
}

export interface AgentLimits {
  max_global_concurrency: number;
  max_per_root: number;
  max_tree_tokens: number;
}

export type FeishuBotStatus = "stopped" | "running" | "error";

export interface FeishuBotSettings {
  id: string;
  kind: "feishu";
  name: string;
  locale: "zh-CN" | "en-US";
  enabled: boolean;
  app_id: string;
  has_app_secret: boolean;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  allowed_users: string[];
  allowed_chats: string[];
  allow_all: boolean;
  status: FeishuBotStatus;
  last_error?: string;
}

export interface FeishuBotUpdate {
  name: string;
  locale: "zh-CN" | "en-US";
  enabled: boolean;
  app_id: string;
  app_secret?: string;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  allowed_users: string[];
  allowed_chats: string[];
  allow_all: boolean;
}

export type ChannelCreate = FeishuBotUpdate & { kind: "feishu" };

export type FeishuRegistrationStatus =
  | "starting"
  | "pending"
  | "completing"
  | "completed"
  | "denied"
  | "expired"
  | "cancelled"
  | "error";

export interface FeishuRegistrationInput {
  name: string;
  locale: "zh-CN" | "en-US";
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  allowed_users: string[];
  allowed_chats: string[];
  allow_all: boolean;
}

export interface FeishuRegistrationState {
  id: string;
  status: FeishuRegistrationStatus;
  qr_code_url?: string;
  expires_at?: string;
  channel?: FeishuBotSettings;
  error?: string;
}

export type AutomationRunStatus =
  | "idle"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface AutomationTask {
  id: string;
  name: string;
  prompt: string;
  cron: string;
  timezone: string;
  enabled: boolean;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
  last_status: AutomationRunStatus;
  last_error?: string;
  last_session_id?: string;
  last_run_at?: string;
  next_run_at?: string;
  created_at: string;
  updated_at: string;
}

export interface AutomationInput {
  name: string;
  prompt: string;
  cron: string;
  timezone?: string;
  enabled: boolean;
  connection_id?: string;
  model?: string;
  project_id?: string;
  approval_mode: "auto" | "full_access";
}

export type HookEvent =
  | "SessionStart"
  | "SessionEnd"
  | "UserPromptSubmit"
  | "PreToolUse"
  | "PostToolUse"
  | "PermissionRequest"
  | "SubagentStart"
  | "SubagentStop"
  | "PreCompact"
  | "PostCompact"
  | "Stop"
  | "TurnComplete"
  | "Notification";

export interface HookConfig {
  id?: string;
  name?: string;
  event: HookEvent;
  matcher?: string;
  command: string;
  timeout?: number;
  async?: boolean;
  enabled?: boolean;
}

export type CommandScope = "builtin" | "global" | "project";
export type CommandKind = "prompt" | "workflow";

export interface CommandInfo {
  ref: string;
  name: string;
  description?: string;
  scope: CommandScope;
  project_id?: string;
  kind: CommandKind;
  path?: string;
  body?: string;
  builtin?: boolean;
  updated_at?: string;
}

export interface CommandExecution {
  command: CommandInfo;
  status: string;
  submission?: SubmitTurnResult;
  workflow?: WorkflowRecord;
  message?: string;
}

export interface WorkflowRecord {
  id: string;
  session_id: string;
  kind: "plan" | "spec" | "goal";
  status: "active" | "ready" | "approved" | "completed" | "closed";
  goal: string;
  title?: string;
  content?: string;
  path?: string;
  artifact_root?: string;
  artifacts?: {
    spec: string;
    tasks: string;
    checklist: string;
  };
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface WorkflowApproval {
  workflow: WorkflowRecord;
  submission: SubmitTurnResult;
}

export interface StartAgentRequest {
  task: string;
  root_run_id?: string;
  agent_ref?: string;
  context?: {
    mode?: "none" | "selected" | "summary" | "last_n_turns";
    message_seqs?: number[];
    last_turns?: number;
  };
}

export interface ProjectInfo {
  id: string;
  name: string;
  path: string;
  available: boolean;
  pinned?: boolean;
  pinned_at?: string;
  created_at: string;
  updated_at: string;
}

export type ContextItemKind = "rule" | "memory";
export type ContextItemScope = "global" | "project";
export type RuleTrigger = "always" | "glob" | "model_decision" | "manual";

export interface ContextItem {
  id: string;
  name?: string;
  description?: string;
  trigger?: RuleTrigger;
  globs?: string[];
  path?: string;
  scope: ContextItemScope;
  project_id?: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export interface MemorySettings {
  enabled: boolean;
}

export interface SearchProviderConfig {
  id: string;
  kind: "google_cse" | "bing" | "baidu";
  name: string;
  enabled: boolean;
  api_key?: string;
  has_api_key?: boolean;
  search_engine_id?: string;
  endpoint?: string;
}

export interface WebSearchSettings {
  enabled: boolean;
  default_provider?: string;
  providers: SearchProviderConfig[];
}

export interface WebSearchResult {
  title: string;
  url: string;
  snippet?: string;
  source?: string;
  rank: number;
}

export interface McpServerConfig {
  id: string;
  name: string;
  enabled: boolean;
  transport: "stdio" | "streamable_http" | "sse";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  cwd?: string;
  url?: string;
  headers?: Record<string, string>;
  bearer_token?: string;
  has_token?: boolean;
}

export interface McpConfig {
  version: number;
  servers: McpServerConfig[];
}

export interface McpStatus {
  id: string;
  name: string;
  state: "disabled" | "disconnected" | "connecting" | "connected" | "error";
  transport: string;
  tool_count: number;
  resource_count: number;
  prompt_count: number;
  error?: string;
  plugin_id?: string;
}

export interface McpRegistryServer {
  id: string;
  name: string;
  description?: string;
  version?: string;
  installable: boolean;
  reason?: string;
  config: McpServerConfig;
}

export interface PluginAuthor {
  name?: string;
  email?: string;
  url?: string;
}

export interface PluginDiagnostic {
  component?: string;
  path?: string;
  code: string;
  severity: "warning" | "error";
  message: string;
}

export interface AgentPlugin {
  $schema: string;
  name: string;
  version?: string;
  description?: string;
  author?: PluginAuthor;
  homepage?: string;
  repository?: string;
  license?: string;
  keywords?: string[];
  path: string;
  data_path: string;
  source?: string;
  enabled: boolean;
  valid: boolean;
  skill_count: number;
  mcp_server_count: number;
  diagnostics?: PluginDiagnostic[];
}

export interface PluginMarketplace {
  id: string;
  name: string;
  description?: string;
  repository: string;
  owner: PluginAuthor;
  source: string;
  ref?: string;
  sparse_paths?: string[];
  resolved_sha?: string;
  format: "codex" | "claude" | "copilot";
  enabled: boolean;
}

export interface MarketplacePlugin {
  name: string;
  description?: string;
  version?: string;
  author?: PluginAuthor;
  homepage?: string;
  repository?: string;
  license?: string;
  keywords?: string[];
  category?: string;
  tags?: string[];
  source: string;
  installable: boolean;
  reason?: string;
  installed: boolean;
  enabled?: boolean;
  installed_version?: string;
}

export interface PluginMarketplaceCatalog extends PluginMarketplace {
  plugins: MarketplacePlugin[];
}

export interface MarketplacePluginPreview {
  name: string;
  valid: boolean;
  compatibility: "compatible" | "partial" | "unsupported";
  skill_count: number;
  mcp_server_count: number;
  unsupported_components?: string[];
  diagnostics?: PluginDiagnostic[];
}

function parseWebSearchSettings(raw: string): WebSearchSettings {
  const parsed = JSON.parse(raw) as Partial<WebSearchSettings> | null;
  return {
    enabled: parsed?.enabled ?? true,
    default_provider: parsed?.default_provider,
    providers: Array.isArray(parsed?.providers) ? parsed.providers : [],
  };
}

function parseMcpConfig(raw: string): McpConfig {
  const parsed = JSON.parse(raw) as Partial<McpConfig> | null;
  return {
    version: parsed?.version ?? 1,
    servers: Array.isArray(parsed?.servers) ? parsed.servers : [],
  };
}

export const api = {
  getKernelConnection: () =>
    invoke<string>("get_kernel_connection").then(
      (result) => JSON.parse(result) as KernelConnection
    ),

  listSshHosts: () =>
    invoke<string>("list_ssh_hosts").then(
      (result) => (JSON.parse(result) as SshConfigHost[]) ?? []
    ),

  testSshHost: (input: SshHostInput) =>
    invoke<string>("test_ssh_host", { input }).then(
      (result) => JSON.parse(result) as SshHostProbe
    ),

  deploySshKernel: (input: SshHostInput) =>
    invoke("deploy_ssh_kernel", { input }),

  testKernelConnection: (input: KernelConnectionInput) =>
    invoke("test_kernel_connection", { input }),

  updateKernelConnection: (input: KernelConnectionInput) =>
    invoke("update_kernel_connection", { input }),

  createSession: (opts?: CreateSessionOptions) =>
    invoke<string>("create_session", { options: opts ?? null }).then(
      (r) => JSON.parse(r) as Session
    ),

  forkSession: (sessionId: string, opts?: ForkSessionOptions) =>
    invoke<string>("fork_session", { sessionId, options: opts ?? null }).then(
      (r) => JSON.parse(r) as Session
    ),

  updateSession: (sessionId: string, patch: UpdateSessionPatch) =>
    invoke<string>("update_session", { sessionId, patch }).then(
      (r) => JSON.parse(r) as Session
    ),

  // Delete a chat, including cancellation, history removal, and broadcast.
  deleteSession: (sessionId: string) =>
    invoke("delete_session", { sessionId }),

  pickFolder: () =>
    open({ directory: true, multiple: false, title: translate("Select working folder") }),

  listProjectFiles: (projectPath: string) =>
    invoke<string>("list_project_files", { projectPath }).then(
      (result) => (JSON.parse(result) as ProjectEntry[]) ?? []
    ),

  readProjectFile: (projectPath: string, path: string) =>
    invoke<string>("read_project_file", { projectPath, path }),

  listExternalEditors: () =>
    invoke<string>("list_external_editors").then(
      (result) => (JSON.parse(result) as ExternalEditor[]) ?? []
    ),

  openProjectInExternalEditor: (projectPath: string, editorId: string) =>
    invoke("open_project_in_external_editor", { projectPath, editorId }),

  watchProjectFiles: (
    projectPath: string,
    onEvent: (event: ProjectFilesChanged) => void
  ) => {
    const channel = new Channel<string>();
    channel.onmessage = (data) => {
      onEvent(JSON.parse(data) as ProjectFilesChanged);
    };
    return invoke<string>("watch_project_files", { projectPath, channel });
  },

  unwatchProjectFiles: (watchId: string) =>
    invoke("unwatch_project_files", { watchId }),

  createProjectFile: (projectPath: string, path: string) =>
    invoke<string>("create_project_file", { projectPath, path }),

  createProjectDirectory: (projectPath: string, path: string) =>
    invoke<string>("create_project_directory", { projectPath, path }),

  renameProjectEntry: (projectPath: string, path: string, newName: string) =>
    invoke<string>("rename_project_entry", { projectPath, path, newName }),

  deleteProjectEntry: (projectPath: string, path: string) =>
    invoke("delete_project_entry", { projectPath, path }),

  resolveProjectPath: (projectPath: string, path = "") =>
    invoke<string>("resolve_project_path", { projectPath, path }),

  listSessions: () =>
    invoke<string>("list_sessions").then((r) => (JSON.parse(r) as Session[]) ?? []),

  listChildSessions: (sessionId: string) =>
    invoke<string>("list_child_sessions", { sessionId }).then(
      (r) => (JSON.parse(r) as Session[]) ?? []
    ),

  listAgentRuns: (sessionId: string) =>
    invoke<string>("list_agent_runs", { sessionId }).then(
      (r) => (JSON.parse(r) as AgentRunSnapshot[]) ?? []
    ),

  startAgent: (sessionId: string, request: StartAgentRequest) =>
    invoke<string>("start_agent", { sessionId, request }).then(
      (r) => JSON.parse(r) as AgentRunSnapshot
    ),

  cancelAgent: (sessionId: string, runId: string) =>
    invoke("cancel_agent", { sessionId, runId }),

  loadAgentBudget: (sessionId: string) =>
    invoke<string>("load_agent_budget", { sessionId }).then(
      (r) => JSON.parse(r) as AgentBudget
    ),

  loadHistory: (sessionId: string) =>
    invoke<string>("load_history", { sessionId }).then(
      (r) => (JSON.parse(r) as ChatMessage[]) ?? []
    ),

  loadUsage: (sessionId: string) =>
    invoke<string>("load_usage", { sessionId }).then(
      (r) => JSON.parse(r) as ContextUsage | null
    ),

  loadUsageStatistics: (days: 7 | 30) =>
    invoke<string>("load_usage_statistics", { days }).then(
      (r) => JSON.parse(r) as UsageStatistics
    ),

  submitTurn: (
    sessionId: string,
    message: string,
    attachments: AttachmentRef[] = [],
    browserElements: BrowserElementSelection[] = [],
    skillRef = ""
  ) =>
    invoke<string>("submit_turn", {
      sessionId,
      message,
      skillRef,
      attachments,
      browserElements,
    }).then(
      (r) => JSON.parse(r) as SubmitTurnResult
    ),

  uploadImage: async (sessionId: string, file: File) => {
    const data = Array.from(new Uint8Array(await file.arrayBuffer()));
    return invoke<string>("upload_image", { sessionId, name: file.name, data }).then(
      (r) => (JSON.parse(r) as { attachment: AttachmentRef }).attachment
    );
  },

  readArtifact: (sessionId: string, artifactId: string) =>
    invoke<number[]>("read_artifact", { sessionId, artifactId }).then(
      (bytes) => new Uint8Array(bytes)
    ),

  deleteArtifact: (sessionId: string, artifactId: string) =>
    invoke("delete_artifact", { sessionId, artifactId }),

  listCanvases: () =>
    invoke<string>("list_canvases", { sessionId: undefined }).then(
      (result) => (JSON.parse(result) as CanvasDocument[]) ?? []
    ),

  createCanvas: (title?: string) =>
    invoke<string>("create_canvas", {
      sessionId: undefined,
      projectId: undefined,
      title,
    }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  getCanvas: (canvasId: string) =>
    invoke<string>("get_canvas", { canvasId }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  updateCanvas: (
    canvasId: string,
    patch: {
      expected_revision: number;
      title?: string;
      nodes?: CanvasNode[];
      edges?: CanvasEdge[];
      viewport?: CanvasViewport;
      background?: CanvasDocument["background"];
    }
  ) =>
    invoke<string>("update_canvas", { canvasId, patch }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  deleteCanvas: (canvasId: string) => invoke("delete_canvas", { canvasId }),

  uploadCanvasAsset: async (canvasId: string, file: File) => {
    const data = Array.from(new Uint8Array(await file.arrayBuffer()));
    return invoke<string>("upload_canvas_asset", {
      canvasId,
      name: file.name,
      mediaType: file.type || "application/octet-stream",
      data,
    }).then(
      (result) => JSON.parse(result) as { asset: CanvasAsset; canvas: CanvasDocument }
    );
  },

  readCanvasAsset: (canvasId: string, assetId: string) =>
    invoke<number[]>("read_canvas_asset", { canvasId, assetId }).then(
      (bytes) => new Uint8Array(bytes)
    ),

  generateCanvasImage: (
    canvasId: string,
    request: {
      expected_revision: number;
      config_node_id: string;
      output_node_id: string;
      connection_id?: string;
    }
  ) =>
    invoke<string>("generate_canvas_image", { canvasId, request }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  generateCanvasVideo: (
    canvasId: string,
    request: {
      expected_revision: number;
      config_node_id: string;
      output_node_id: string;
      connection_id?: string;
    }
  ) =>
    invoke<string>("generate_canvas_video", { canvasId, request }).then(
      (result) => JSON.parse(result) as CanvasDocument
    ),

  subscribeCanvasEvents: (canvasId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_canvas_events", { canvasId, channel });
  },

  rewindTurn: (
    sessionId: string,
    messageSeq: number,
    confirm = false,
    expectedHeadSeq = 0,
    expectedFileState = "",
    forceFileKeys: string[] = []
  ) =>
    invoke<string>("rewind_turn", {
      sessionId,
      messageSeq,
      confirm,
      expectedHeadSeq,
      expectedFileState,
      forceFileKeys,
    }).then((r) => JSON.parse(r) as RewindTurnResult),

  loadFileReview: (sessionId: string) =>
    invoke<string>("load_file_review", { sessionId }).then(
      (result) => JSON.parse(result) as FileReview
    ),

  resolveFileReview: (
    sessionId: string,
    action: "keep" | "undo",
    expectedThroughSeq: number,
    expectedFileState: string,
    forceFileKeys: string[] = []
  ) =>
    invoke<string>("resolve_file_review", {
      sessionId,
      action,
      expectedThroughSeq,
      expectedFileState,
      forceFileKeys,
    }),

  compactSession: (sessionId: string) =>
    invoke<string>("compact_session", { sessionId }).then(
      (r) => JSON.parse(r) as CompactSessionResult
    ),

  listQueuedMessages: (sessionId: string) =>
    invoke<string>("list_queued_messages", { sessionId }).then(
      (r) => (JSON.parse(r) as QueuedMessage[]) ?? []
    ),

  enqueueMessage: (
    sessionId: string,
    message: string,
    attachments: AttachmentRef[] = [],
    browserElements: BrowserElementSelection[] = [],
    skillRef = ""
  ) =>
    invoke<string>("enqueue_message", {
      sessionId,
      message,
      skillRef,
      attachments,
      browserElements,
    }).then(
      (r) => JSON.parse(r) as QueuedMessage
    ),

  updateQueuedMessage: (
    sessionId: string,
    messageId: string,
    patch: UpdateQueuedMessagePatch
  ) =>
    invoke<string>("update_queued_message", { sessionId, messageId, patch }).then(
      (r) => JSON.parse(r) as QueuedMessage
    ),

  deleteQueuedMessage: (sessionId: string, messageId: string) =>
    invoke("delete_queued_message", { sessionId, messageId }),

  dispatchQueuedMessage: (sessionId: string, messageId: string) =>
    invoke<string>("dispatch_queued_message", { sessionId, messageId }).then(
      (r) => JSON.parse(r) as QueuedMessage
    ),

  // Stop the current turn.
  cancelTurn: (sessionId: string) =>
    invoke("cancel_turn", { sessionId }),

  cancelTool: (sessionId: string, toolCallId: string) =>
    invoke("cancel_tool", { sessionId, toolCallId }),

  backgroundTool: (sessionId: string, toolCallId: string) =>
    invoke<string>("background_tool", { sessionId, toolCallId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  revealToolCommand: (sessionId: string, toolCallId: string) =>
    invoke<string>("reveal_tool_command", { sessionId, toolCallId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  listBackgroundCommands: (sessionId: string) =>
    invoke<string>("list_background_commands", { sessionId }).then(
      (result) => (JSON.parse(result) as BackgroundCommand[]) ?? []
    ),

  getBackgroundCommand: (sessionId: string, commandId: string) =>
    invoke<string>("get_background_command", { sessionId, commandId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  stopBackgroundCommand: (sessionId: string, commandId: string) =>
    invoke<string>("stop_background_command", { sessionId, commandId }).then(
      (result) => JSON.parse(result) as BackgroundCommand
    ),

  listConnections: () =>
    invoke<string>("list_connections").then(
      (r) => (JSON.parse(r) as ConnectionConfig[]) ?? []
    ),

  createConnection: (config: ConnectionConfig) =>
    invoke<string>("create_connection", { config }).then(
      (r) => JSON.parse(r) as ConnectionConfig
    ),

  updateConnection: (connectionId: string, config: ConnectionConfig) =>
    invoke<string>("update_connection", { connectionId, config }).then(
      (r) => JSON.parse(r) as ConnectionConfig
    ),

  deleteConnection: (connectionId: string) =>
    invoke("delete_connection", { connectionId }),

  listConnectionModels: (connectionId: string, refresh = false) =>
    invoke<string>("list_connection_models", { connectionId, refresh }).then(
      (r) => {
        const result = JSON.parse(r) as Partial<ConnectionModelCatalog>;
        return {
          models: result.models ?? [],
          context_windows: result.context_windows ?? {},
          capabilities: result.capabilities ?? {},
        } satisfies ConnectionModelCatalog;
      }
    ),

  getDefaultModels: () =>
    invoke<string>("get_default_models").then(
      (r) => JSON.parse(r) as DefaultModels
    ),

  updateDefaultModels: (defaults: DefaultModels) =>
    invoke<string>("update_default_models", { defaults }).then(
      (r) => JSON.parse(r) as DefaultModels
    ),

  listSkills: () =>
    invoke<string>("list_skills").then(
      (r) => (JSON.parse(r) as SkillInfo[]) ?? []
    ),

  listAvailableSkills: (projectId = "") =>
    invoke<string>("list_available_skills", { projectId }).then(
      (r) => (JSON.parse(r) as SkillInfo[]) ?? []
    ),

  listAgents: () =>
    invoke<string>("list_agents").then(
      (r) => (JSON.parse(r) as AgentInfo[]) ?? []
    ),

  listProjects: () =>
    invoke<string>("list_projects").then(
      (r) => (JSON.parse(r) as ProjectInfo[]) ?? []
    ),

  registerProject: (path: string, name = "") =>
    invoke<string>("register_project", { path, name: name.trim() }).then(
      (r) => JSON.parse(r) as ProjectInfo
    ),

  updateProject: (
    projectId: string,
    patch: { name?: string; pinned?: boolean }
  ) =>
    invoke<string>("update_project", { projectId, patch }).then(
      (r) => JSON.parse(r) as ProjectInfo
    ),

  deleteProject: (projectId: string) =>
    invoke("delete_project", { projectId }),

  listProjectSkills: (projectId: string) =>
    invoke<string>("list_project_skills", { projectId }).then(
      (r) => (JSON.parse(r) as SkillInfo[]) ?? []
    ),

  listProjectAgents: (projectId: string) =>
    invoke<string>("list_project_agents", { projectId }).then(
      (r) => (JSON.parse(r) as AgentInfo[]) ?? []
    ),

  listContextItems: (
    kind: ContextItemKind,
    scope: ContextItemScope,
    projectId?: string
  ) =>
    invoke<string>("list_context_items", { kind, scope, projectId }).then(
      (r) => (JSON.parse(r) as ContextItem[]) ?? []
    ),

  createContextItem: (
    kind: ContextItemKind,
    item: {
      scope: ContextItemScope;
      project_id?: string;
      content: string;
      name?: string;
      description?: string;
      trigger?: RuleTrigger;
      globs?: string[];
      path?: string;
    }
  ) =>
    invoke<string>("create_context_item", { kind, item }).then(
      (r) => JSON.parse(r) as ContextItem
    ),

  updateContextItem: (
    kind: ContextItemKind,
    itemId: string,
    item: {
      content: string;
      name?: string;
      description?: string;
      trigger?: RuleTrigger;
      globs?: string[];
      path?: string;
    }
  ) =>
    invoke<string>("update_context_item", { kind, itemId, item }).then(
      (r) => JSON.parse(r) as ContextItem
    ),

  deleteContextItem: (kind: ContextItemKind, itemId: string) =>
    invoke("delete_context_item", { kind, itemId }),

  subscribeContextEvents: (onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_context_events", { channel });
  },

  getMemorySettings: () =>
    invoke<string>("get_memory_settings").then(
      (r) => JSON.parse(r) as MemorySettings
    ),

  updateMemorySettings: (settings: MemorySettings) =>
    invoke<string>("update_memory_settings", { settings }).then(
      (r) => JSON.parse(r) as MemorySettings
    ),

  getAgentLimits: () =>
    invoke<string>("get_agent_limits").then(
      (r) => JSON.parse(r) as AgentLimits
    ),

  updateAgentLimits: (limits: AgentLimits) =>
    invoke<string>("update_agent_limits", { limits }).then(
      (r) => JSON.parse(r) as AgentLimits
    ),

  getFeishuBotSettings: () =>
    invoke<string>("get_feishu_bot_settings").then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  updateFeishuBotSettings: (settings: FeishuBotUpdate) =>
    invoke<string>("update_feishu_bot_settings", { settings }).then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  listChannels: () =>
    invoke<string>("list_channels").then(
      (r) => (JSON.parse(r) as FeishuBotSettings[]) ?? []
    ),

  createChannel: (settings: ChannelCreate) =>
    invoke<string>("create_channel", { settings }).then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  startFeishuRegistration: (settings: FeishuRegistrationInput) =>
    invoke<string>("start_feishu_registration", { settings }).then(
      (r) => JSON.parse(r) as FeishuRegistrationState
    ),

  getFeishuRegistration: (registrationId: string) =>
    invoke<string>("get_feishu_registration", { registrationId }).then(
      (r) => JSON.parse(r) as FeishuRegistrationState
    ),

  cancelFeishuRegistration: (registrationId: string) =>
    invoke("cancel_feishu_registration", { registrationId }),

  updateChannel: (channelId: string, settings: FeishuBotUpdate) =>
    invoke<string>("update_channel", { channelId, settings }).then(
      (r) => JSON.parse(r) as FeishuBotSettings
    ),

  deleteChannel: (channelId: string) =>
    invoke("delete_channel", { channelId }),

  listAutomations: () =>
    invoke<string>("list_automations").then(
      (r) => (JSON.parse(r) as AutomationTask[]) ?? []
    ),

  createAutomation: (input: AutomationInput) =>
    invoke<string>("create_automation", { input }).then(
      (r) => JSON.parse(r) as AutomationTask
    ),

  updateAutomation: (automationId: string, input: AutomationInput) =>
    invoke<string>("update_automation", { automationId, input }).then(
      (r) => JSON.parse(r) as AutomationTask
    ),

  deleteAutomation: (automationId: string) =>
    invoke("delete_automation", { automationId }),

  runAutomation: (automationId: string) =>
    invoke<string>("run_automation", { automationId }).then(
      (r) => JSON.parse(r) as AutomationTask
    ),

  getHooks: (scope: "global" | "project", projectId?: string) =>
    invoke<string>("get_hooks", { scope, projectId: projectId ?? "" }).then(
      (r) => (JSON.parse(r) as HookConfig[]) ?? []
    ),

  updateHooks: (
    scope: "global" | "project",
    hooks: HookConfig[],
    projectId?: string
  ) =>
    invoke<string>("update_hooks", {
      request: {
        scope,
        project_id: projectId ?? "",
        hooks,
      },
    }).then((r) => (JSON.parse(r) as HookConfig[]) ?? []),

  listCommands: (scope: "global" | "project", projectId?: string) =>
    invoke<string>("list_commands", { scope, projectId: projectId ?? "" }).then(
      (r) => (JSON.parse(r) as CommandInfo[]) ?? []
    ),

  createCommand: (input: {
    scope: "global" | "project";
    project_id?: string;
    name: string;
    body?: string;
  }) =>
    invoke<string>("create_command", { request: input }).then(
      (r) => JSON.parse(r) as CommandInfo
    ),

  updateCommand: (
    commandRef: string,
    input: {
      scope: "global" | "project";
      project_id?: string;
      name: string;
      description?: string;
      body: string;
    }
  ) =>
    invoke<string>("update_command", { commandRef, request: input }).then(
      (r) => JSON.parse(r) as CommandInfo
    ),

  deleteCommand: (
    commandRef: string,
    scope: "global" | "project",
    projectId?: string
  ) =>
    invoke("delete_command", {
      commandRef,
      scope,
      projectId: projectId ?? "",
    }),

  listSessionCommands: (sessionId: string) =>
    invoke<string>("list_session_commands", { sessionId }).then(
      (r) => (JSON.parse(r) as CommandInfo[]) ?? []
    ),

  executeCommand: (sessionId: string, name: string, args = "") =>
    invoke<string>("execute_command", { sessionId, name, args }).then(
      (r) => JSON.parse(r) as CommandExecution
    ),

  getWorkflow: (sessionId: string) =>
    invoke<string>("get_workflow", { sessionId }).then(
      (r) => JSON.parse(r) as WorkflowRecord | null
    ),

  approveWorkflow: (sessionId: string, workflowId: string) =>
    invoke<string>("approve_workflow", { sessionId, workflowId }).then(
      (r) => JSON.parse(r) as WorkflowApproval
    ),

  closeWorkflow: (sessionId: string, workflowId: string) =>
    invoke<string>("close_workflow", { sessionId, workflowId }).then(
      (r) => JSON.parse(r) as WorkflowRecord
    ),

  setSkillEnabled: (skillRef: string, enabled: boolean) =>
    invoke("set_skill_enabled", { skillRef, enabled }),

  setSkillPinned: (skillRef: string, pinned: boolean) =>
    invoke("set_skill_pinned", { skillRef, pinned }),

  getWebSearchSettings: () =>
    invoke<string>("get_web_search_settings").then(
      parseWebSearchSettings
    ),

  updateWebSearchSettings: (settings: WebSearchSettings) =>
    invoke<string>("update_web_search_settings", { settings }).then(
      parseWebSearchSettings
    ),

  testWebSearch: (providerId: string, query: string) =>
    invoke<string>("test_web_search", { providerId, query }).then(
      (r) => (JSON.parse(r) as WebSearchResult[]) ?? []
    ),

  getMcpConfig: () =>
    invoke<string>("get_mcp_config").then(parseMcpConfig),

  updateMcpConfig: (config: McpConfig) =>
    invoke<string>("update_mcp_config", { config }).then(
      parseMcpConfig
    ),

  getMcpStatus: () =>
    invoke<string>("get_mcp_status").then(
      (r) => (JSON.parse(r) as McpStatus[]) ?? []
    ),

  searchMcpRegistry: (query: string) =>
    invoke<string>("search_mcp_registry", { query }).then(
      (r) => (JSON.parse(r) as McpRegistryServer[]) ?? []
    ),

  listPlugins: () =>
    invoke<string>("list_plugins").then(
      (r) => (JSON.parse(r) as AgentPlugin[]) ?? []
    ),

  listPluginMarketplaces: () =>
    invoke<string>("list_plugin_marketplaces").then(
      (r) => (JSON.parse(r) as PluginMarketplace[]) ?? []
    ),

  addPluginMarketplace: (
    source: string,
    gitRef = "",
    sparsePaths: string[] = []
  ) =>
    invoke<string>("add_plugin_marketplace", {
      source,
      gitRef,
      sparsePaths,
    }).then((r) => JSON.parse(r) as PluginMarketplace),

  browsePluginMarketplace: (name: string) =>
    invoke<string>("browse_plugin_marketplace", { name }).then(
      (r) => JSON.parse(r) as PluginMarketplaceCatalog
    ),

  refreshPluginMarketplace: (name: string) =>
    invoke<string>("refresh_plugin_marketplace", { name }).then(
      (r) => JSON.parse(r) as PluginMarketplace
    ),

  setPluginMarketplaceEnabled: (name: string, enabled: boolean) =>
    invoke<string>("set_plugin_marketplace_enabled", {
      name,
      enabled,
    }).then((r) => (JSON.parse(r) as PluginMarketplace[]) ?? []),

  removePluginMarketplace: (name: string) =>
    invoke("remove_plugin_marketplace", { name }),

  previewMarketplacePlugin: (marketplace: string, pluginName: string) =>
    invoke<string>("preview_marketplace_plugin", {
      marketplace,
      pluginName,
    }).then((r) => JSON.parse(r) as MarketplacePluginPreview),

  installMarketplacePlugin: (
    marketplace: string,
    pluginName: string,
    replace = false
  ) =>
    invoke<string>("install_marketplace_plugin", {
      marketplace,
      pluginName,
      replace,
    }).then((r) => JSON.parse(r) as AgentPlugin),

  installPlugin: (source: string, replace = false) =>
    invoke<string>("install_plugin", { source, replace }).then(
      (r) => JSON.parse(r) as AgentPlugin
    ),

  setPluginEnabled: (name: string, enabled: boolean) =>
    invoke<string>("set_plugin_enabled", { name, enabled }).then(
      (r) => (JSON.parse(r) as AgentPlugin[]) ?? []
    ),

  removePlugin: (name: string) =>
    invoke("remove_plugin", { name }),

  subscribeEvents: (sessionId: string, onEvent: (data: string) => void) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_events", { sessionId, channel });
  },

  startTerminal: (sessionId: string, cols: number, rows: number) =>
    invoke<string>("start_terminal", { sessionId, cols, rows }).then(
      (r) => JSON.parse(r) as TerminalResource
    ),

  attachTerminal: (sessionId: string, terminalRef: string) =>
    invoke<string>("attach_terminal", { sessionId, terminalRef }).then(
      (r) => JSON.parse(r) as TerminalResource
    ),

  writeTerminal: (sessionId: string, terminalRef: string, input: string) =>
    invoke("write_terminal", { sessionId, terminalRef, input }),

  resizeTerminal: (
    sessionId: string,
    terminalRef: string,
    cols: number,
    rows: number
  ) => invoke("resize_terminal", { sessionId, terminalRef, cols, rows }),

  stopTerminal: (sessionId: string, terminalRef: string) =>
    invoke("stop_terminal", { sessionId, terminalRef }),

  subscribeTerminal: (
    sessionId: string,
    terminalRef: string,
    after: number,
    onEvent: (data: string) => void
  ) => {
    const channel = new Channel<string>();
    channel.onmessage = onEvent;
    return invoke("subscribe_terminal", {
      sessionId,
      terminalRef,
      after,
      channel,
    });
  },

  setBrowserViewport: (
    browserId: string,
    viewport: BrowserViewport | null
  ) => invoke("set_browser_viewport", { browserId, viewport }),

  navigateBrowser: (
    browserId: string,
    url: string,
    viewport: BrowserViewport,
    visible = true
  ) => invoke("navigate_browser", { browserId, url, viewport, visible }),

  browserBack: (browserId: string) => invoke("browser_back", { browserId }),

  browserForward: (browserId: string) =>
    invoke("browser_forward", { browserId }),

  browserReload: (browserId: string) =>
    invoke("browser_reload", { browserId }),

  setBrowserElementPicker: (browserId: string, enabled: boolean) =>
    invoke("set_browser_element_picker", {
      browserId,
      enabled,
      addLabel: translate("Add to chat"),
      cancelLabel: translate("Cancel selection"),
    }),

  executeBrowserAction: (request: BrowserActionRequest) =>
    invoke<BrowserActionResult>("execute_browser_action", { request }),

  resolveBrowserAction: (
    sessionId: string,
    requestId: string,
    result: BrowserActionResult
  ) => invoke("resolve_browser_action", { sessionId, requestId, result }),

  hideBrowser: (browserId: string) =>
    invoke("hide_browser", { browserId }),

  closeBrowser: (browserId: string) =>
    invoke("close_browser", { browserId }),

  // Resolve an approval request.
  resolveApproval: (
    sessionId: string,
    requestId: string,
    decision: ApprovalDecision
  ) =>
    invoke("resolve_approval", { sessionId, requestId, decision }),

  answerQuestions: (
    sessionId: string,
    batchId: string,
    answers: QuestionAnswer[]
  ) => invoke("answer_questions", { sessionId, batchId, answers }),

  cancelQuestions: (sessionId: string, batchId: string) =>
    invoke("cancel_questions", { sessionId, batchId }),
};
