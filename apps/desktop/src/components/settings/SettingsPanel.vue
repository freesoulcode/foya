<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  ArrowLeftIcon,
  AlertCircleIcon,
  BlocksIcon,
  BotIcon,
  BookOpenIcon,
  BrainIcon,
  CommandIcon,
  DownloadIcon,
  ExternalLinkIcon,
  FileJsonIcon,
  GlobeIcon,
  GripVerticalIcon,
  KeyRoundIcon,
  MonitorIcon,
  MoonIcon,
  PaletteIcon,
  PlusIcon,
  PlugZapIcon,
  RefreshCwIcon,
  SearchIcon,
  ScrollTextIcon,
  SunIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  api,
  type AgentLimits,
  type ConnectionConfig,
  type McpConfig,
  type McpRegistryServer,
  type McpServerConfig,
  type McpStatus,
  type ProjectInfo,
  type SearchProviderConfig,
  type SkillInfo,
  type WebSearchResult,
  type WebSearchSettings,
} from "@/lib/api";
import { useTheme, type Theme } from "@/composables/useTheme";
import {
  useLinkPreference,
  type LinkOpenMode,
} from "@/composables/useLinkPreference";
import { usePlatform } from "@/composables/usePlatform";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
} from "@/components/ui/sidebar";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import ProjectCreateDialog from "@/components/projects/ProjectCreateDialog.vue";
import ContextItemsSettings from "@/components/settings/ContextItemsSettings.vue";
import CommandsSettings from "@/components/settings/CommandsSettings.vue";
import HooksSettings from "@/components/settings/HooksSettings.vue";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const props = defineProps<{ active: boolean; currentProjectId?: string }>();
const emit = defineEmits<{ close: [] }>();
const { theme, setTheme } = useTheme();
const { linkOpenMode, setLinkOpenMode } = useLinkPreference();
const { isMac } = usePlatform();

type SettingsSection =
  | "connections"
  | "rules"
  | "memory"
  | "skills"
  | "commands"
  | "hooks"
  | "mcp"
  | "web-search"
  | "agents"
  | "appearance";
interface DragPreview {
  name: string;
  detail: string;
  x: number;
  y: number;
  width: number;
}
const section = ref<SettingsSection>("connections");
const connections = ref<ConnectionConfig[]>([]);
const selectedID = ref<string | null>(null);
const name = ref("");
const baseURL = ref("");
const contextWindowValue = ref("");
const contextWindowUnit = ref<"K" | "M">("K");
const apiKey = ref("");
const hasKey = ref(false);
const connectionErrors = ref<Record<string, string>>({});
const connectionEditorOpen = ref(false);
const loading = ref(false);
const saving = ref(false);
const deleting = ref(false);
const reordering = ref(false);
const draggingID = ref("");
const dropPosition = ref<number | null>(null);
const pointerDrag = ref(false);
const pointerMoved = ref(false);
const dragPreview = ref<DragPreview | null>(null);
const error = ref("");
const contextRevision = ref(0);
const contextSubscribed = ref(false);
const skills = ref<SkillInfo[]>([]);
const projects = ref<ProjectInfo[]>([]);
const projectCreateOpen = ref(false);
const projectCreateError = ref("");
const skillScope = ref<"global" | "project">("global");
const selectedSkillProjectID = ref("");
const capabilityLoading = ref(false);
const capabilitySaving = ref(false);
const mcpConfig = ref<McpConfig>({ version: 1, servers: [] });
const mcpStatuses = ref<McpStatus[]>([]);
const pendingMcpDelete = ref<McpServerConfig | null>(null);
const mcpAddOpen = ref(false);
const mcpAddMode = ref<"market" | "json">("market");
const mcpJSON = ref("");
const mcpAddError = ref("");
const mcpMarketQuery = ref("");
const mcpMarketLoading = ref(false);
const mcpMarketResults = ref<McpRegistryServer[]>([]);
const webSettings = ref<WebSearchSettings>({ enabled: true, providers: [] });
const searchProviderKind = ref<"duckduckgo" | SearchProviderConfig["kind"]>("duckduckgo");
const searchAPIKey = ref("");
const searchEngineID = ref("");
const searchEndpoint = ref("");
const webTestQuery = ref("Foya agent");
const webTestResults = ref<WebSearchResult[]>([]);
const agentLimits = ref<AgentLimits>({
  max_global_concurrency: 4,
  max_per_root: 4,
  max_tree_tokens: 0,
});
const globalSkills = computed(() =>
  skills.value.filter((item) => item.scope !== "project")
);
const projectSkills = computed(() =>
  skills.value.filter((item) => item.scope === "project")
);
const selectedSkillProject = computed(
  () => projects.value.find((item) => item.id === selectedSkillProjectID.value) ?? null
);
const activeProjects = computed(() =>
  projects.value
);
const skillGroups = computed(() => {
  const global = {
    id: "global",
    title: skillScope.value === "project" ? "继承的全局技能" : "全局技能",
    description: "~/.agents/skills 和 ~/.foya/skills",
    items: globalSkills.value,
  };
  if (skillScope.value === "global") return [global];
  return [{
    id: "project",
    title: selectedSkillProject.value
      ? `项目技能 · ${selectedSkillProject.value.name}`
      : "项目技能",
    description: selectedSkillProject.value
      ? `${selectedSkillProject.value.path}/.agents/skills 和 ${selectedSkillProject.value.path}/.foya/skills`
      : "选择项目后显示",
    items: projectSkills.value,
  }, global];
});

const sections: Array<{ id: SettingsSection; label: string; icon: typeof PlugZapIcon }> = [
  { id: "connections", label: "连接", icon: PlugZapIcon },
  { id: "rules", label: "规则", icon: ScrollTextIcon },
  { id: "memory", label: "记忆", icon: BrainIcon },
  { id: "skills", label: "技能", icon: BookOpenIcon },
  { id: "commands", label: "命令", icon: CommandIcon },
  { id: "hooks", label: "Hooks", icon: FileJsonIcon },
  { id: "mcp", label: "MCP", icon: BlocksIcon },
  { id: "web-search", label: "联网搜索", icon: GlobeIcon },
  { id: "agents", label: "Agent", icon: BotIcon },
  { id: "appearance", label: "外观", icon: PaletteIcon },
];
const themeOptions: Array<{ value: Theme; label: string; icon: typeof SunIcon }> = [
  { value: "system", label: "跟随系统", icon: MonitorIcon },
  { value: "light", label: "浅色", icon: SunIcon },
  { value: "dark", label: "深色", icon: MoonIcon },
];
const linkOpenOptions: Array<{
  value: LinkOpenMode;
  label: string;
  icon: typeof GlobeIcon;
}> = [
  { value: "workbar", label: "内置浏览器", icon: GlobeIcon },
  { value: "system", label: "系统浏览器", icon: ExternalLinkIcon },
];
const selectedConnection = computed(
  () => connections.value.find((connection) => connection.id === selectedID.value) ?? null
);
const selectedConnectionError = computed(
  () => connectionErrors.value[selectedID.value ?? ""] ?? ""
);
const isNewConnection = computed(() => selectedID.value === null);

function resetForm(connection?: ConnectionConfig) {
  selectedID.value = connection?.id ?? null;
  name.value = connection?.name ?? "";
  baseURL.value = connection?.base_url ?? "";
  setContextWindow(connection?.context_window);
  hasKey.value = Boolean(connection?.has_api_key);
  apiKey.value = "";
  error.value = "";
}

function setContextWindow(value?: number) {
  if (!value || value <= 0) {
    contextWindowValue.value = "";
    contextWindowUnit.value = "K";
    return;
  }
  contextWindowUnit.value = value >= 1_000_000 ? "M" : "K";
  const divisor = contextWindowUnit.value === "M" ? 1_000_000 : 1_000;
  contextWindowValue.value = String(value / divisor);
}

function parseContextWindow(): number {
  const normalized = String(contextWindowValue.value ?? "").trim();
  if (!normalized) return 0;
  const multiplier = contextWindowUnit.value === "M" ? 1_000_000 : 1_000;
  const parsed = Math.round(Number(normalized) * multiplier);
  if (!Number.isSafeInteger(parsed) || parsed < 1) {
    throw new Error("上下文窗口必须是正数");
  }
  return parsed;
}

function selectContextWindowUnit(value: unknown) {
  if (value === "K" || value === "M") contextWindowUnit.value = value;
}

async function checkConnection(connection: ConnectionConfig) {
  const id = connection.id ?? "";
  if (!id) return;
  try {
    const catalog = await api.listConnectionModels(id);
    const message = catalog.models.length === 0 ? "未发现可用模型" : "";
    connectionErrors.value = { ...connectionErrors.value, [id]: message };
  } catch (cause) {
    connectionErrors.value = {
      ...connectionErrors.value,
      [id]: String(cause),
    };
  }
}

async function loadConnections() {
  loading.value = true;
  error.value = "";
  try {
    connections.value = await api.listConnections();
    connectionErrors.value = {};
    await Promise.all(connections.value.map(checkConnection));
    if (connectionEditorOpen.value) {
      const current = connections.value.find((connection) => connection.id === selectedID.value);
      if (current || selectedID.value) resetForm(current);
    }
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.active,
  (active) => {
    if (active) {
      void loadConnections();
      if (!contextSubscribed.value) {
        contextSubscribed.value = true;
        void api
          .subscribeContextEvents(() => {
            contextRevision.value += 1;
          })
          .catch(() => {
            contextSubscribed.value = false;
          });
      }
    }
  },
  { immediate: true }
);

watch(section, (next) => {
  if (next === "skills") void loadSkills();
  if (next === "mcp") void loadMcp();
  if (next === "web-search") void loadWebSearch();
  if (next === "agents") void loadAgentLimits();
});

async function loadAgentLimits() {
  capabilityLoading.value = true;
  error.value = "";
  try {
    agentLimits.value = await api.getAgentLimits();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilityLoading.value = false;
  }
}

function validInteger(value: number, min: number, max: number): boolean {
  return Number.isInteger(value) && value >= min && value <= max;
}

async function saveAgentLimits() {
  error.value = "";
  const limits = agentLimits.value;
  if (
    !validInteger(limits.max_global_concurrency, 1, 256) ||
    !validInteger(limits.max_per_root, 1, 256) ||
    !Number.isInteger(limits.max_tree_tokens) ||
    limits.max_tree_tokens < 0
  ) {
    error.value = "请检查输入范围：并发 1~256，Token 上限 ≥ 0";
    return;
  }
  capabilitySaving.value = true;
  try {
    agentLimits.value = await api.updateAgentLimits({ ...limits });
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilitySaving.value = false;
  }
}

function openProjectCreate() {
  projectCreateError.value = "";
  projectCreateOpen.value = true;
}

async function createProject(input: { name: string; path: string }) {
  capabilitySaving.value = true;
  projectCreateError.value = "";
  try {
    const item = await api.registerProject(input.path, input.name);
    projects.value = await api.listProjects();
    projectCreateOpen.value = false;
    selectedSkillProjectID.value = item.id;
    await selectSkillScope("project");
  } catch (cause) {
    projectCreateError.value = String(cause);
  } finally {
    capabilitySaving.value = false;
  }
}

async function loadSkills() {
  capabilityLoading.value = true;
  error.value = "";
  try {
    projects.value = await api.listProjects();
    const preferred = props.currentProjectId || selectedSkillProjectID.value;
    if (!activeProjects.value.some((item) => item.id === preferred)) {
      selectedSkillProjectID.value = activeProjects.value[0]?.id ?? "";
    } else {
      selectedSkillProjectID.value = preferred;
    }
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilityLoading.value = false;
  }
}

async function loadSelectedSkills() {
  skills.value =
    skillScope.value === "project" && selectedSkillProjectID.value
      ? await api.listProjectSkills(selectedSkillProjectID.value)
      : await api.listSkills();
}

async function selectSkillScope(next: "global" | "project") {
  skillScope.value = next;
  capabilityLoading.value = true;
  error.value = "";
  try {
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilityLoading.value = false;
  }
}

async function selectSkillProject(value: unknown) {
  if (typeof value !== "string") return;
  selectedSkillProjectID.value = value;
  await selectSkillScope("project");
}

async function toggleSkill(item: SkillInfo) {
  capabilitySaving.value = true;
  try {
    await api.setSkillEnabled(item.ref, !item.enabled);
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilitySaving.value = false;
  }
}

async function loadMcp() {
  capabilityLoading.value = true;
  error.value = "";
  try {
    [mcpConfig.value, mcpStatuses.value] = await Promise.all([
      api.getMcpConfig(),
      api.getMcpStatus(),
    ]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilityLoading.value = false;
  }
}

function mcpStateLabel(state?: McpStatus["state"]): string {
  return {
    connected: "已连接",
    connecting: "连接中",
    disconnected: "未连接",
    disabled: "已停用",
    error: "连接失败",
  }[state ?? "disconnected"];
}

function mcpStateClass(state?: McpStatus["state"]): string {
  if (state === "connected") return "bg-emerald-500";
  if (state === "connecting") return "bg-amber-500";
  if (state === "error") return "bg-destructive";
  return "bg-muted-foreground/40";
}

async function updateMcpServers(servers: McpServerConfig[]) {
  capabilitySaving.value = true;
  error.value = "";
  try {
    mcpConfig.value = await api.updateMcpConfig({ version: 1, servers });
    mcpStatuses.value = await api.getMcpStatus();
  } catch (cause) {
    error.value = String(cause);
    throw cause;
  } finally {
    capabilitySaving.value = false;
  }
}

async function toggleMcpServer(server: McpServerConfig) {
  await updateMcpServers(
    mcpConfig.value.servers.map((item) =>
      item.id === server.id ? { ...item, enabled: !item.enabled } : item
    )
  ).catch(() => undefined);
}

async function deleteMcpServer() {
  const server = pendingMcpDelete.value;
  if (!server) return;
  try {
    await updateMcpServers(
      mcpConfig.value.servers.filter((item) => item.id !== server.id)
    );
    pendingMcpDelete.value = null;
  } catch {}
}

function openMcpAdd(mode: "market" | "json" = "market") {
  mcpAddMode.value = mode;
  mcpAddError.value = "";
  mcpAddOpen.value = true;
  if (mode === "market" && mcpMarketResults.value.length === 0) {
    void searchMcpMarket();
  }
}

function stringRecord(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, item]) => typeof item === "string") as Array<[string, string]>;
  return entries.length ? Object.fromEntries(entries) : undefined;
}

function normalizeMcpServer(id: string, value: unknown): McpServerConfig {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${id} 的配置格式无效`);
  }
  const item = value as Record<string, unknown>;
  const command = typeof item.command === "string" ? item.command : "";
  const url = typeof item.url === "string" ? item.url : "";
  const rawTransport =
    typeof item.transport === "string"
      ? item.transport
      : typeof item.type === "string"
        ? item.type
        : "";
  const transport = command
    ? "stdio"
    : rawTransport.replace("-", "_") === "sse"
      ? "sse"
      : "streamable_http";
  if (!command && !url) throw new Error(`${id} 缺少 command 或 url`);
  return {
    id,
    name: typeof item.name === "string" ? item.name : id,
    enabled: item.enabled !== false,
    transport,
    ...(command
      ? {
          command,
          args: Array.isArray(item.args)
            ? item.args.filter((arg): arg is string => typeof arg === "string")
            : [],
          cwd: typeof item.cwd === "string" ? item.cwd : undefined,
          env: stringRecord(item.env),
        }
      : {
          url,
          headers: stringRecord(item.headers),
          bearer_token:
            typeof item.bearer_token === "string" ? item.bearer_token : undefined,
        }),
  };
}

function parseMcpJSON(source: string): McpServerConfig[] {
  const parsed = JSON.parse(source) as unknown;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("JSON 顶层必须是对象");
  }
  const root = parsed as Record<string, unknown>;
  if (Array.isArray(root.servers)) {
    return root.servers.map((item, index) => {
      const value = item as Record<string, unknown>;
      const id = typeof value.id === "string" ? value.id : `server-${index + 1}`;
      return normalizeMcpServer(id, value);
    });
  }
  const map =
    root.mcpServers && typeof root.mcpServers === "object"
      ? (root.mcpServers as Record<string, unknown>)
      : root.servers && typeof root.servers === "object"
        ? (root.servers as Record<string, unknown>)
        : root;
  return Object.entries(map).map(([id, value]) => normalizeMcpServer(id, value));
}

async function addMcpFromJSON() {
  mcpAddError.value = "";
  try {
    const incoming = parseMcpJSON(mcpJSON.value);
    if (incoming.length === 0) throw new Error("JSON 中没有 MCP 服务器");
    const merged = new Map(mcpConfig.value.servers.map((item) => [item.id, item]));
    for (const item of incoming) merged.set(item.id, item);
    await updateMcpServers(Array.from(merged.values()));
    mcpJSON.value = "";
    mcpAddOpen.value = false;
  } catch (cause) {
    mcpAddError.value = String(cause);
  }
}

async function searchMcpMarket() {
  mcpMarketLoading.value = true;
  mcpAddError.value = "";
  try {
    mcpMarketResults.value = await api.searchMcpRegistry(mcpMarketQuery.value);
  } catch (cause) {
    mcpAddError.value = String(cause);
  } finally {
    mcpMarketLoading.value = false;
  }
}

function mcpInstalled(id: string): boolean {
  return mcpConfig.value.servers.some((item) => item.id === id);
}

async function installMarketMcp(item: McpRegistryServer) {
  if (!item.installable || mcpInstalled(item.id)) return;
  mcpAddError.value = "";
  try {
    await updateMcpServers([...mcpConfig.value.servers, item.config]);
  } catch (cause) {
    mcpAddError.value = String(cause);
  }
}

async function loadWebSearch() {
  capabilityLoading.value = true;
  error.value = "";
  try {
    webSettings.value = await api.getWebSearchSettings();
    const selected = webSettings.value.providers.find(
      (item) => item.id === webSettings.value.default_provider
    );
    selectSearchProvider(selected?.kind ?? "duckduckgo");
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilityLoading.value = false;
  }
}

function selectSearchProvider(kind: "duckduckgo" | SearchProviderConfig["kind"]) {
  searchProviderKind.value = kind;
  const existing = webSettings.value.providers.find((item) => item.kind === kind);
  searchAPIKey.value = "";
  searchEngineID.value = existing?.search_engine_id ?? "";
  searchEndpoint.value = existing?.endpoint ?? "";
}

function selectSearchProviderValue(value: unknown) {
  if (
    value === "duckduckgo" ||
    value === "google_cse" ||
    value === "bing" ||
    value === "baidu"
  ) {
    selectSearchProvider(value);
  }
}

async function saveWebSearch() {
  capabilitySaving.value = true;
  error.value = "";
  try {
    const kind = searchProviderKind.value;
    const providers = [...webSettings.value.providers];
    let defaultProvider = "";
    if (kind !== "duckduckgo") {
      const existing = providers.find((item) => item.kind === kind);
      const id = existing?.id ?? kind.replace("_cse", "");
      const provider: SearchProviderConfig = {
        id,
        kind,
        name:
          kind === "google_cse" ? "Google" : kind === "bing" ? "Bing" : "百度",
        enabled: true,
        ...(searchAPIKey.value.trim()
          ? { api_key: searchAPIKey.value.trim() }
          : {}),
        ...(kind === "google_cse"
          ? { search_engine_id: searchEngineID.value.trim() }
          : {}),
        ...(kind === "bing" && searchEndpoint.value.trim()
          ? { endpoint: searchEndpoint.value.trim() }
          : {}),
      };
      const index = providers.findIndex((item) => item.id === id);
      if (index >= 0) providers[index] = provider;
      else providers.push(provider);
      defaultProvider = id;
    }
    webSettings.value = await api.updateWebSearchSettings({
      enabled: webSettings.value.enabled,
      default_provider: defaultProvider,
      providers,
    });
    searchAPIKey.value = "";
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilitySaving.value = false;
  }
}

async function testWebSearch() {
  await saveWebSearch();
  if (error.value) return;
  const id = webSettings.value.default_provider || "duckduckgo";
  capabilityLoading.value = true;
  error.value = "";
  try {
    webTestResults.value = await api.testWebSearch(id, webTestQuery.value);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    capabilityLoading.value = false;
  }
}

function selectConnection(connection: ConnectionConfig) {
  resetForm(connection);
  connectionEditorOpen.value = true;
}

function startNewConnection() {
  resetForm();
  connectionEditorOpen.value = true;
}

function backToConnectionList() {
  connectionEditorOpen.value = false;
  resetForm();
}

function formPayload(): ConnectionConfig {
  return {
    ...(selectedID.value ? { id: selectedID.value } : {}),
    name: name.value.trim() || "未命名连接",
    kind: "openai",
    auth_kind: "api_key",
    base_url: baseURL.value.trim(),
    default_model: selectedConnection.value?.default_model ?? "",
    context_window: parseContextWindow(),
    sort_order: selectedConnection.value?.sort_order ?? connections.value.length,
    ...(apiKey.value.trim() ? { api_key: apiKey.value.trim() } : {}),
  };
}

async function save() {
  saving.value = true;
  error.value = "";
  try {
    const payload = formPayload();
    const saved = selectedID.value
      ? await api.updateConnection(selectedID.value, payload)
      : await api.createConnection(payload);
    const index = connections.value.findIndex((connection) => connection.id === saved.id);
    if (index >= 0) connections.value[index] = saved;
    else connections.value.push(saved);
    resetForm(saved);
    connectionEditorOpen.value = true;
    await checkConnection(saved);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

function beginPointerDrag(event: PointerEvent, connection: ConnectionConfig) {
  const id = connection.id ?? "";
  if (event.button !== 0 || !id || reordering.value) return;
  event.preventDefault();
  (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  draggingID.value = id;
  dropPosition.value = null;
  pointerDrag.value = true;
  pointerMoved.value = false;
  const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect();
  dragPreview.value = {
    name: connection.name,
    detail: connection.base_url,
    x: event.clientX + 14,
    y: event.clientY + 14,
    width: bounds.width,
  };
}

function updatePointerDropPosition(event: PointerEvent) {
  if (!pointerDrag.value || !draggingID.value) return;
  pointerMoved.value = true;
  if (dragPreview.value) {
    dragPreview.value.x = event.clientX + 14;
    dragPreview.value.y = event.clientY + 14;
  }
  const rows = Array.from(
    document.querySelectorAll<HTMLElement>("[data-connection-row]")
  ).filter((row) => row.dataset.connectionId !== draggingID.value);
  let position = connections.value.length;
  for (const row of rows) {
    const bounds = row.getBoundingClientRect();
    if (event.clientY < bounds.top + bounds.height / 2) {
      position = Number(row.dataset.connectionIndex);
      break;
    }
    position = Number(row.dataset.connectionIndex) + 1;
  }
  dropPosition.value = position;
}

async function finishPointerDrag() {
  const id = draggingID.value;
  pointerDrag.value = false;
  const sourceIndex = connections.value.findIndex((connection) => connection.id === id);
  if (!pointerMoved.value) {
    const connection = connections.value[sourceIndex];
    if (connection) selectConnection(connection);
    endDrag();
    return;
  }
  if (!id || sourceIndex < 0 || dropPosition.value === null) {
    endDrag();
    return;
  }
  const targetIndex =
    sourceIndex < dropPosition.value ? dropPosition.value - 1 : dropPosition.value;
  if (sourceIndex === targetIndex) {
    endDrag();
    return;
  }
  const connection = connections.value[sourceIndex];
  const previous = connections.value;
  const next = [...previous];
  next.splice(sourceIndex, 1);
  next.splice(targetIndex, 0, connection);
  connections.value = next.map((item, index) => ({ ...item, sort_order: index }));
  dropPosition.value = null;
  reordering.value = true;
  error.value = "";
  try {
    await api.updateConnection(id, { ...connection, sort_order: targetIndex });
    await loadConnections();
  } catch (cause) {
    connections.value = previous;
    error.value = String(cause);
  } finally {
    reordering.value = false;
    endDrag();
  }
}

function endDrag() {
  draggingID.value = "";
  dropPosition.value = null;
  pointerMoved.value = false;
  dragPreview.value = null;
}

async function remove() {
  if (!selectedID.value || !selectedConnection.value) return;
  deleting.value = true;
  error.value = "";
  try {
    await api.deleteConnection(selectedID.value);
    const removedID = selectedID.value;
    connections.value = connections.value.filter((connection) => connection.id !== selectedID.value);
    const remainingErrors = { ...connectionErrors.value };
    delete remainingErrors[removedID];
    connectionErrors.value = remainingErrors;
    connectionEditorOpen.value = false;
    resetForm();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    deleting.value = false;
  }
}
</script>

<template>
  <SidebarProvider class="h-svh w-full">
    <Sidebar collapsible="none" class="border-r border-sidebar-border">
      <SidebarHeader
        data-tauri-drag-region
        class="h-12 flex-row items-center p-0"
        :class="isMac ? 'pl-[72px]' : 'pl-3'"
      />
      <SidebarContent class="p-2">
        <SidebarGroup class="p-0">
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton @click="emit('close')">
                  <ArrowLeftIcon />
                  <span>返回工作区</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup class="p-0 pt-4">
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem v-for="item in sections" :key="item.id">
                <SidebarMenuButton
                  :is-active="section === item.id"
                  @click="section = item.id"
                >
                  <component :is="item.icon" />
                  <span>{{ item.label }}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
    </Sidebar>

    <SidebarInset class="relative min-w-0 overflow-hidden">
      <section class="min-w-0 flex-1 overflow-y-auto">
        <div v-if="section === 'connections'" class="mx-auto flex min-h-full max-w-5xl flex-col px-6 pb-6">
          <div
            data-tauri-drag-region
            class="mb-6 flex min-h-11 flex-wrap items-center justify-between gap-3 pt-4"
          >
            <div>
              <h2 class="text-base font-medium">模型连接</h2>
              <p class="mt-1 text-xs text-muted-foreground">
                配置 Agent 使用的模型服务与 API Key。
              </p>
            </div>
            <div class="no-drag flex items-center gap-2">
              <Button
                size="icon-sm"
                variant="ghost"
                :disabled="loading || saving || deleting"
                title="刷新模型连接"
                aria-label="刷新模型连接"
                @click="loadConnections"
              >
                <RefreshCwIcon class="size-4" :class="loading && 'animate-spin'" />
              </Button>
              <Button
                v-if="!connectionEditorOpen"
                size="sm"
                :disabled="loading || saving"
                @click="startNewConnection"
              >
                <PlusIcon class="size-4" />
                添加连接
              </Button>
            </div>
          </div>

          <section
            v-if="!connectionEditorOpen"
            class="flex min-h-[420px] flex-1 flex-col border-y border-border bg-background"
          >
            <div class="grid h-11 shrink-0 grid-cols-[36px_minmax(160px,0.8fr)_minmax(220px,1.4fr)_minmax(100px,0.45fr)_132px] items-center gap-3 border-b border-border px-4 text-xs font-medium text-muted-foreground">
              <span />
              <span>名称</span>
              <span>Base URL</span>
              <span>状态</span>
              <span class="text-right">上下文窗口</span>
            </div>
            <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
              <template v-for="(connection, index) in connections" :key="connection.id">
                <div
                  v-if="dropPosition === index"
                  class="pointer-events-none flex h-2 items-center px-4"
                >
                  <span class="h-px w-full bg-primary/50" />
                </div>
                <button
                  type="button"
                  :class="[
                    'grid w-full cursor-grab touch-none select-none grid-cols-[36px_minmax(160px,0.8fr)_minmax(220px,1.4fr)_minmax(100px,0.45fr)_132px] items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50 active:cursor-grabbing',
                    connection.id === draggingID ? 'opacity-55' : 'opacity-100',
                  ]"
                  data-connection-row
                  :data-connection-id="connection.id"
                  :data-connection-index="index"
                  @pointerdown="beginPointerDrag($event, connection)"
                  @pointermove="updatePointerDropPosition"
                  @pointerup="finishPointerDrag"
                  @pointercancel="endDrag"
                >
                  <GripVerticalIcon class="size-4 text-muted-foreground" aria-hidden="true" />
                  <span class="truncate text-sm font-medium">{{ connection.name }}</span>
                  <span class="truncate font-mono text-xs text-muted-foreground">
                    {{ connection.base_url }}
                  </span>
                  <span
                    class="inline-flex w-fit items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs"
                    :class="connectionErrors[connection.id ?? ''] ? 'border-destructive/30 bg-destructive/10 text-destructive' : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300'"
                  >
                    <span
                      class="size-1.5 rounded-full"
                      :class="connectionErrors[connection.id ?? ''] ? 'bg-destructive' : 'bg-emerald-500'"
                    />
                    {{ connectionErrors[connection.id ?? ""] ? "失败" : "可用" }}
                  </span>
                  <span class="truncate text-right text-xs text-muted-foreground">
                    {{ connection.context_window ? connection.context_window.toLocaleString() : "默认" }}
                  </span>
                </button>
              </template>
              <div
                v-if="dropPosition === connections.length"
                class="pointer-events-none flex h-2 items-center px-4"
              >
                <span class="h-px w-full bg-primary/50" />
              </div>
              <div
                v-if="!loading && connections.length === 0"
                class="flex min-h-72 flex-col items-center justify-center gap-3 px-4 text-center"
              >
                <PlugZapIcon class="size-8 text-muted-foreground/50" />
                <div>
                  <p class="text-sm font-medium">暂无模型连接</p>
                  <p class="mt-1 text-xs text-muted-foreground">
                    添加连接后即可在会话中选择模型
                  </p>
                </div>
              </div>
            </div>
            <div class="flex min-h-11 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
              <p class="truncate text-sm text-destructive">{{ error }}</p>
              <p v-if="!error" class="text-xs text-muted-foreground">
                {{ connections.length }} 个连接
              </p>
            </div>
          </section>

          <section
            v-else
            class="flex min-h-[420px] flex-1 flex-col border-y border-border bg-background"
          >
            <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
              <div class="flex min-w-0 items-center gap-3">
                <Button
                  size="icon-sm"
                  variant="ghost"
                  title="返回连接列表"
                  aria-label="返回连接列表"
                  @click="backToConnectionList"
                >
                  <ArrowLeftIcon class="size-4" />
                </Button>
                <div class="min-w-0">
                  <h3 class="truncate text-sm font-medium">
                    {{ isNewConnection ? "添加 API 连接" : "编辑 API 连接" }}
                  </h3>
                  <p v-if="!isNewConnection" class="truncate text-xs text-muted-foreground">
                    {{ selectedConnection?.base_url }}
                  </p>
                </div>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <Button
                  v-if="!isNewConnection"
                  variant="ghost"
                  size="icon-sm"
                  :disabled="deleting || saving"
                  class="text-muted-foreground hover:text-destructive"
                  title="删除连接"
                  aria-label="删除连接"
                  @click="remove"
                >
                  <Trash2Icon class="size-4" />
                </Button>
                <Button :disabled="saving || deleting || loading" @click="save">
                  <KeyRoundIcon class="size-4" />
                  {{ saving ? "保存中…" : isNewConnection ? "添加连接" : "保存连接" }}
                </Button>
              </div>
            </div>
            <div class="grid gap-4 p-4 lg:grid-cols-2">
              <div class="space-y-1.5">
                <Label for="connection-name">名称</Label>
                <Input id="connection-name" v-model="name" placeholder="例如：OpenAI 个人" :disabled="loading" />
              </div>
              <div class="space-y-1.5">
                <Label for="connection-url">Base URL</Label>
                <Input
                  id="connection-url"
                  v-model="baseURL"
                  placeholder="https://api.openai.com/v1"
                  :disabled="loading"
                />
              </div>
              <div class="space-y-1.5">
                <Label for="connection-context-window">上下文窗口（可选）</Label>
                <div class="flex gap-2">
                  <Input
                    id="connection-context-window"
                    v-model="contextWindowValue"
                    type="number"
                    min="0"
                    step="0.1"
                    placeholder="默认 200"
                    :disabled="loading"
                  />
                  <Select
                    :model-value="contextWindowUnit"
                    @update:model-value="selectContextWindowUnit"
                  >
                    <SelectTrigger class="w-24 shrink-0">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="K">K</SelectItem>
                      <SelectItem value="M">M</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div class="space-y-1.5">
                <Label for="connection-key">API Key</Label>
                <Input
                  id="connection-key"
                  v-model="apiKey"
                  type="password"
                  :placeholder="hasKey ? '已配置，留空则保持不变' : 'sk-...'"
                  :disabled="loading"
                />
              </div>
            </div>
            <div class="mt-auto flex min-h-12 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
              <div class="min-w-0">
                <p class="truncate text-sm text-destructive">{{ error }}</p>
                <div
                  v-if="selectedConnectionError"
                  class="flex items-start gap-2 text-sm text-destructive"
                >
                  <AlertCircleIcon class="mt-0.5 size-4 shrink-0" />
                  <span class="break-all">{{ selectedConnectionError }}</span>
                </div>
              </div>
            </div>
          </section>
        </div>

        <ContextItemsSettings
          v-else-if="section === 'rules'"
          kind="rule"
          :current-project-id="props.currentProjectId"
          :revision="contextRevision"
        />

        <ContextItemsSettings
          v-else-if="section === 'memory'"
          kind="memory"
          :current-project-id="props.currentProjectId"
          :revision="contextRevision"
        />

        <HooksSettings
          v-else-if="section === 'hooks'"
          :current-project-id="props.currentProjectId"
        />
        <CommandsSettings
          v-else-if="section === 'commands'"
          :current-project-id="currentProjectId"
        />

        <div v-else-if="section === 'skills'" class="mx-auto w-full max-w-3xl p-6">
          <h2 class="mb-6 text-base font-medium">技能</h2>
          <div class="mb-6 flex flex-wrap items-center gap-3">
            <ButtonGroup aria-label="技能范围">
              <Button
                size="sm"
                :variant="skillScope === 'global' ? 'default' : 'outline'"
                @click="selectSkillScope('global')"
              >
                全局
              </Button>
              <Button
                size="sm"
                :variant="skillScope === 'project' ? 'default' : 'outline'"
                :disabled="activeProjects.length === 0"
                @click="selectSkillScope('project')"
              >
                项目
              </Button>
            </ButtonGroup>
            <Select
              v-if="skillScope === 'project'"
              :model-value="selectedSkillProjectID"
              :disabled="capabilityLoading || activeProjects.length === 0"
              @update:model-value="selectSkillProject"
            >
              <SelectTrigger size="sm" class="min-w-48 max-w-full">
                <SelectValue placeholder="选择项目" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem
                  v-for="project in activeProjects"
                  :key="project.id"
                  :value="project.id"
                >
                  {{ project.name }} · {{ project.path }}
                </SelectItem>
              </SelectContent>
            </Select>
            <Button
              v-if="skillScope === 'project'"
              size="icon-sm"
              variant="outline"
              title="添加项目"
              aria-label="添加项目"
              :disabled="capabilitySaving"
              @click="openProjectCreate"
            >
              <PlusIcon class="size-4" />
            </Button>
          </div>
          <div class="space-y-7">
            <section v-for="group in skillGroups" :key="group.id">
              <div class="mb-2">
                <h3 class="text-sm font-medium">{{ group.title }}</h3>
                <p class="truncate text-xs text-muted-foreground" :title="group.description">
                  {{ group.description }}
                </p>
              </div>
              <div class="divide-y divide-border border-y border-border">
                <div
                  v-for="item in group.items"
                  :key="item.ref"
                  class="flex min-h-14 items-center justify-between gap-4 py-3"
                >
                  <div class="min-w-0">
                    <div class="flex items-center gap-2">
                      <p class="truncate text-sm font-medium">{{ item.name }}</p>
                      <span v-if="item.scope === 'builtin'" class="text-xs text-muted-foreground">
                        内置
                      </span>
                    </div>
                    <p class="truncate text-xs text-muted-foreground" :title="item.path">
                      {{ item.description || item.path }}
                    </p>
                  </div>
                  <input
                    type="checkbox"
                    :checked="item.enabled"
                    :disabled="capabilitySaving"
                    :aria-label="`${item.enabled ? '停用' : '启用'} ${item.name}`"
                    class="size-4 accent-primary"
                    @change="toggleSkill(item)"
                  />
                </div>
                <p
                  v-if="!capabilityLoading && group.items.length === 0"
                  class="py-4 text-sm text-muted-foreground"
                >
                  {{ group.id === "project" && !selectedSkillProject ? "当前未选择项目。" : "尚未发现技能。" }}
                </p>
              </div>
            </section>
          </div>
          <p v-if="error" class="mt-4 text-sm text-destructive">{{ error }}</p>
        </div>

        <div v-else-if="section === 'mcp'" class="mx-auto flex min-h-full w-full max-w-5xl flex-col p-6">
          <div class="mb-6 flex items-center justify-between gap-3">
            <h2 class="text-base font-medium">MCP 服务器</h2>
            <div class="flex items-center gap-1">
              <Button
                size="icon-sm"
                variant="ghost"
                :disabled="capabilityLoading || capabilitySaving"
                title="刷新状态"
                aria-label="刷新 MCP 状态"
                @click="loadMcp"
              >
                <RefreshCwIcon
                  class="size-4"
                  :class="capabilityLoading && 'animate-spin'"
                />
              </Button>
              <Button
                size="sm"
                variant="outline"
                :disabled="capabilitySaving"
                @click="openMcpAdd()"
              >
                <PlusIcon class="size-4" />
                添加服务器
              </Button>
            </div>
          </div>

          <div class="min-h-[360px] flex-1 border-y border-border">
            <section class="flex min-w-0 flex-col">
              <div class="flex h-11 items-center border-b border-border px-3 text-sm font-medium">
                已安装
              </div>
              <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
                <div
                  v-for="server in mcpConfig.servers"
                  :key="server.id"
                  class="flex min-h-16 items-center gap-3 px-3 py-2"
                >
                  <span
                    class="size-2 shrink-0 rounded-full"
                    :class="mcpStateClass(mcpStatuses.find((item) => item.id === server.id)?.state)"
                  />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium">
                      {{ server.name || server.id }}
                    </span>
                    <span class="block truncate text-xs text-muted-foreground">
                      {{ mcpStateLabel(mcpStatuses.find((item) => item.id === server.id)?.state) }}
                      <template v-if="mcpStatuses.find((item) => item.id === server.id)?.state === 'connected'">
                        · {{ mcpStatuses.find((item) => item.id === server.id)?.tool_count ?? 0 }} 工具
                      </template>
                    </span>
                  </span>
                  <label class="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
                    <input
                      type="checkbox"
                      class="size-4 accent-primary"
                      :checked="server.enabled"
                      :disabled="capabilitySaving"
                      :aria-label="`${server.enabled ? '停用' : '启用'} ${server.name}`"
                      @change="toggleMcpServer(server)"
                    />
                  </label>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    class="text-muted-foreground hover:text-destructive"
                    :disabled="capabilitySaving"
                    title="删除服务器"
                    @click="pendingMcpDelete = server"
                  >
                    <Trash2Icon class="size-4" />
                  </Button>
                </div>
                <p
                  v-if="!capabilityLoading && mcpConfig.servers.length === 0"
                  class="px-3 py-8 text-center text-sm text-muted-foreground"
                >
                  暂无 MCP 服务器
                </p>
              </div>
            </section>

          </div>
          <p v-if="error" class="mt-4 text-sm text-destructive">{{ error }}</p>
        </div>

        <div v-else-if="section === 'web-search'" class="mx-auto w-full max-w-3xl p-6">
          <h2 class="mb-6 text-base font-medium">联网搜索</h2>
          <div class="space-y-5">
            <label class="flex items-center justify-between border-b border-border py-3">
              <span class="text-sm font-medium">启用联网搜索</span>
              <input v-model="webSettings.enabled" type="checkbox" class="size-4 accent-primary" />
            </label>
            <div class="space-y-1.5">
              <Label for="search-provider">搜索引擎</Label>
              <Select
                :model-value="searchProviderKind"
                @update:model-value="selectSearchProviderValue"
              >
                <SelectTrigger id="search-provider" class="w-full">
                  <SelectValue placeholder="选择搜索引擎" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="duckduckgo">DuckDuckGo</SelectItem>
                  <SelectItem value="google_cse">Google</SelectItem>
                  <SelectItem value="bing">Bing</SelectItem>
                  <SelectItem value="baidu">百度</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <template v-if="searchProviderKind === 'google_cse'">
              <div class="space-y-1.5">
                <Label for="google-cx">Search Engine ID</Label>
                <Input id="google-cx" v-model="searchEngineID" placeholder="cx" />
              </div>
              <div class="space-y-1.5">
                <Label for="google-key">API Key</Label>
                <Input
                  id="google-key"
                  v-model="searchAPIKey"
                  type="password"
                  placeholder="留空则保持原值"
                />
              </div>
            </template>

            <template v-else-if="searchProviderKind === 'bing'">
              <div class="space-y-1.5">
                <Label for="bing-key">API Key</Label>
                <Input
                  id="bing-key"
                  v-model="searchAPIKey"
                  type="password"
                  placeholder="留空则保持原值"
                />
              </div>
              <div class="space-y-1.5">
                <Label for="bing-endpoint">Endpoint</Label>
                <Input
                  id="bing-endpoint"
                  v-model="searchEndpoint"
                  class="font-mono"
                  placeholder="https://api.bing.microsoft.com/v7.0/search"
                />
              </div>
            </template>

            <div class="flex justify-end">
              <Button :disabled="capabilitySaving" @click="saveWebSearch">
                {{ capabilitySaving ? "保存中…" : "保存" }}
              </Button>
            </div>
            <div class="space-y-2 border-t border-border pt-4">
              <Label for="web-test-query">测试查询</Label>
              <div class="flex gap-2">
                <Input id="web-test-query" v-model="webTestQuery" />
                <Button
                  variant="outline"
                  :disabled="capabilityLoading || !webTestQuery.trim()"
                  @click="testWebSearch"
                >
                  测试
                </Button>
              </div>
              <a
                v-for="result in webTestResults"
                :key="result.url"
                :href="result.url"
                target="_blank"
                rel="noreferrer"
                class="block border-b border-border py-2 text-sm hover:text-primary"
              >
                <span class="font-medium">{{ result.title }}</span>
                <span class="block truncate text-xs text-muted-foreground">{{ result.url }}</span>
              </a>
            </div>
            <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
          </div>
        </div>

        <div v-else-if="section === 'agents'" class="mx-auto w-full max-w-3xl p-6">
          <h2 class="mb-6 text-base font-medium">Agent 调度</h2>
          <p class="mb-4 text-xs text-muted-foreground">
            当前子 Agent 默认无递归派工能力，调度层级固定为 1；系统内置 Child 总数安全网以防失控。
          </p>
          <div class="divide-y divide-border border-y border-border">
            <label class="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-6 py-4">
              <span class="min-w-0">
                <span class="block text-sm font-medium">全局并发</span>
                <span class="block text-xs text-muted-foreground">整个内核同时运行的 Child 数</span>
              </span>
              <Input
                v-model.number="agentLimits.max_global_concurrency"
                type="number"
                min="1"
                max="256"
                step="1"
                :disabled="capabilityLoading"
              />
            </label>
            <label class="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-6 py-4">
              <span class="min-w-0">
                <span class="block text-sm font-medium">单任务并发</span>
                <span class="block text-xs text-muted-foreground">单个根任务同时运行的 Child 数</span>
              </span>
              <Input
                v-model.number="agentLimits.max_per_root"
                type="number"
                min="1"
                max="256"
                step="1"
                :disabled="capabilityLoading"
              />
            </label>
            <label class="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-6 py-4">
              <span class="min-w-0">
                <span class="block text-sm font-medium">任务树 Token 上限</span>
                <span class="block text-xs text-muted-foreground">0 表示不限制</span>
              </span>
              <Input
                v-model.number="agentLimits.max_tree_tokens"
                type="number"
                min="0"
                step="1000"
                :disabled="capabilityLoading"
              />
            </label>
          </div>
          <p v-if="error" class="mt-4 text-sm text-destructive">{{ error }}</p>
          <div class="mt-5 flex justify-end">
            <Button
              :disabled="capabilityLoading || capabilitySaving"
              @click="saveAgentLimits"
            >
              {{ capabilitySaving ? "保存中…" : "保存" }}
            </Button>
          </div>
        </div>

        <div v-else class="mx-auto w-full max-w-3xl p-6">
          <h2 class="mb-6 text-base font-medium">外观</h2>
          <div class="flex items-center justify-between border-b border-border py-3">
            <span class="text-sm font-medium">主题</span>
            <ButtonGroup aria-label="主题">
              <Button
                v-for="option in themeOptions"
                :key="option.value"
                variant="outline"
                size="sm"
                :class="[
                  'min-w-20 shadow-none',
                  theme === option.value
                    ? 'bg-accent text-accent-foreground'
                    : 'text-muted-foreground',
                ]"
                :aria-pressed="theme === option.value"
                @click="setTheme(option.value)"
              >
                <component :is="option.icon" />
                {{ option.label }}
              </Button>
            </ButtonGroup>
          </div>
          <div class="flex items-center justify-between gap-6 border-b border-border py-3">
            <span class="text-sm font-medium">链接打开方式</span>
            <ButtonGroup aria-label="链接打开方式">
              <Button
                v-for="option in linkOpenOptions"
                :key="option.value"
                variant="outline"
                size="sm"
                :class="[
                  'min-w-24 shadow-none',
                  linkOpenMode === option.value
                    ? 'bg-accent text-accent-foreground'
                    : 'text-muted-foreground',
                ]"
                :aria-pressed="linkOpenMode === option.value"
                @click="setLinkOpenMode(option.value)"
              >
                <component :is="option.icon" />
                {{ option.label }}
              </Button>
            </ButtonGroup>
          </div>
        </div>
      </section>
    </SidebarInset>

    <Dialog v-model:open="mcpAddOpen">
      <DialogContent class="flex max-h-[78vh] max-w-2xl flex-col gap-0 p-0">
        <DialogHeader class="border-b border-border px-5 py-4">
          <DialogTitle>添加 MCP</DialogTitle>
          <DialogDescription class="sr-only">
            从市场安装或导入 MCP JSON 配置
          </DialogDescription>
        </DialogHeader>

        <div class="flex items-center gap-1 border-b border-border px-5 py-2">
          <Button
            size="sm"
            :variant="mcpAddMode === 'market' ? 'secondary' : 'ghost'"
            @click="openMcpAdd('market')"
          >
            <DownloadIcon class="size-4" />
            市场
          </Button>
          <Button
            size="sm"
            :variant="mcpAddMode === 'json' ? 'secondary' : 'ghost'"
            @click="openMcpAdd('json')"
          >
            <FileJsonIcon class="size-4" />
            JSON
          </Button>
        </div>

        <div v-if="mcpAddMode === 'market'" class="flex min-h-0 flex-1 flex-col">
          <div class="flex gap-2 border-b border-border p-4">
            <div class="relative min-w-0 flex-1">
              <SearchIcon class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                v-model="mcpMarketQuery"
                class="pl-9"
                placeholder="搜索 MCP 服务"
                @keydown.enter.prevent="searchMcpMarket"
              />
            </div>
            <Button
              variant="outline"
              :disabled="mcpMarketLoading"
              @click="searchMcpMarket"
            >
              <RefreshCwIcon
                class="size-4"
                :class="mcpMarketLoading && 'animate-spin'"
              />
              搜索
            </Button>
          </div>

          <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
            <div
              v-for="item in mcpMarketResults"
              :key="`${item.id}:${item.version}`"
              class="flex items-center gap-4 px-5 py-3"
            >
              <div class="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted">
                <BlocksIcon class="size-4 text-muted-foreground" />
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <p class="truncate text-sm font-medium">{{ item.name }}</p>
                  <span v-if="item.version" class="shrink-0 text-[11px] text-muted-foreground">
                    {{ item.version }}
                  </span>
                </div>
                <p class="line-clamp-2 text-xs text-muted-foreground">
                  {{ item.description || item.id }}
                </p>
              </div>
              <Button
                size="sm"
                variant="outline"
                class="shrink-0"
                :disabled="
                  capabilitySaving ||
                  mcpInstalled(item.id) ||
                  !item.installable
                "
                :title="item.reason"
                @click="installMarketMcp(item)"
              >
                {{
                  mcpInstalled(item.id)
                    ? "已安装"
                    : item.installable
                      ? "安装"
                      : item.reason || "不可安装"
                }}
              </Button>
            </div>
            <p
              v-if="!mcpMarketLoading && mcpMarketResults.length === 0"
              class="px-5 py-10 text-center text-sm text-muted-foreground"
            >
              未找到可用服务
            </p>
          </div>
          <p class="border-t border-border px-5 py-2 text-[11px] text-muted-foreground">
            数据来自官方 MCP Registry（Preview）
          </p>
        </div>

        <div v-else class="space-y-4 p-5">
          <Textarea
            v-model="mcpJSON"
            class="min-h-72 resize-y font-mono text-xs"
            placeholder='{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"]
    }
  }
}'
          />
          <p v-if="mcpAddError" class="text-sm text-destructive">
            {{ mcpAddError }}
          </p>
          <div class="flex justify-end">
            <Button
              :disabled="capabilitySaving || !mcpJSON.trim()"
              @click="addMcpFromJSON"
            >
              {{ capabilitySaving ? "添加中…" : "添加" }}
            </Button>
          </div>
        </div>

        <p
          v-if="mcpAddMode === 'market' && mcpAddError"
          class="border-t border-border px-5 py-3 text-sm text-destructive"
        >
          {{ mcpAddError }}
        </p>
      </DialogContent>
    </Dialog>

    <Dialog
      :open="pendingMcpDelete !== null"
      @update:open="(open) => { if (!open) pendingMcpDelete = null; }"
    >
      <DialogContent class="max-w-md">
        <DialogHeader>
          <DialogTitle>删除 MCP 服务器</DialogTitle>
          <DialogDescription>
            将删除“{{ pendingMcpDelete?.name || pendingMcpDelete?.id }}”的配置并断开连接。
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            variant="outline"
            :disabled="capabilitySaving"
            @click="pendingMcpDelete = null"
          >
            取消
          </Button>
          <Button
            variant="destructive"
            :disabled="capabilitySaving"
            @click="deleteMcpServer"
          >
            {{ capabilitySaving ? "删除中…" : "删除" }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <ProjectCreateDialog
      v-model:open="projectCreateOpen"
      :busy="capabilitySaving"
      :error="projectCreateError"
      @confirm="createProject"
    />

    <Teleport to="body">
      <div
        v-if="dragPreview"
        class="pointer-events-none fixed z-50 flex items-center gap-2 rounded-md border border-border bg-popover/95 px-3 py-2 shadow-md"
        :style="{
          left: `${dragPreview.x}px`,
          top: `${dragPreview.y}px`,
          width: `${dragPreview.width}px`,
        }"
      >
        <GripVerticalIcon class="size-4 shrink-0 text-muted-foreground" />
        <div class="min-w-0">
          <span class="block truncate text-sm font-medium">{{ dragPreview.name }}</span>
          <span class="block truncate text-xs text-muted-foreground">
            {{ dragPreview.detail }}
          </span>
        </div>
      </div>
    </Teleport>
  </SidebarProvider>
</template>
