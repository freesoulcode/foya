<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  ArrowLeftIcon,
  GripVerticalIcon,
  KeyRoundIcon,
  MonitorIcon,
  MoonIcon,
  PaletteIcon,
  PlusIcon,
  PlugZapIcon,
  SunIcon,
  Trash2Icon,
} from "@lucide/vue";
import { api, type ConnectionConfig } from "@/lib/api";
import { useTheme, type Theme } from "@/composables/useTheme";
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

const props = defineProps<{ active: boolean }>();
const emit = defineEmits<{ close: [] }>();
const { theme, setTheme } = useTheme();
const { isMac } = usePlatform();

type SettingsSection = "connections" | "appearance";
interface DragPreview {
  name: string;
  model: string;
  x: number;
  y: number;
  width: number;
}

const section = ref<SettingsSection>("connections");
const connections = ref<ConnectionConfig[]>([]);
const selectedID = ref<string | null>(null);
const name = ref("");
const baseURL = ref("");
const defaultModel = ref("");
const apiKey = ref("");
const hasKey = ref(false);
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

const sections: Array<{ id: SettingsSection; label: string; icon: typeof PlugZapIcon }> = [
  { id: "connections", label: "连接", icon: PlugZapIcon },
  { id: "appearance", label: "外观", icon: PaletteIcon },
];
const themeOptions: Array<{ value: Theme; label: string; icon: typeof SunIcon }> = [
  { value: "system", label: "跟随系统", icon: MonitorIcon },
  { value: "light", label: "浅色", icon: SunIcon },
  { value: "dark", label: "深色", icon: MoonIcon },
];
const selectedConnection = computed(
  () => connections.value.find((connection) => connection.id === selectedID.value) ?? null
);
const isNewConnection = computed(() => selectedID.value === null);

function resetForm(connection?: ConnectionConfig) {
  selectedID.value = connection?.id ?? null;
  name.value = connection?.name ?? "";
  baseURL.value = connection?.base_url ?? "";
  defaultModel.value = connection?.default_model ?? "";
  hasKey.value = Boolean(connection?.has_api_key);
  apiKey.value = "";
  error.value = "";
}

async function loadConnections() {
  loading.value = true;
  error.value = "";
  try {
    connections.value = await api.listConnections();
    const current = connections.value.find((connection) => connection.id === selectedID.value);
    resetForm(current ?? connections.value[0]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.active,
  (active) => {
    if (active) void loadConnections();
  },
  { immediate: true }
);

function selectConnection(connection: ConnectionConfig) {
  resetForm(connection);
}

function startNewConnection() {
  resetForm();
}

function formPayload(): ConnectionConfig {
  return {
    ...(selectedID.value ? { id: selectedID.value } : {}),
    name: name.value.trim() || "未命名连接",
    kind: "openai",
    auth_kind: "api_key",
    base_url: baseURL.value.trim(),
    default_model: defaultModel.value.trim(),
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
    model: connection.default_model,
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
    connections.value = connections.value.filter((connection) => connection.id !== selectedID.value);
    resetForm(connections.value[0]);
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

    <SidebarInset class="min-w-0 overflow-hidden">
      <section class="min-w-0 flex-1 overflow-y-auto">
        <div v-if="section === 'connections'" class="mx-auto flex min-h-full max-w-5xl flex-col p-6">
          <h2 class="mb-6 text-base font-medium">模型连接</h2>

          <div class="grid min-h-[420px] flex-1 grid-cols-[minmax(190px,0.75fr)_minmax(0,1.5fr)] border border-border">
            <section class="flex min-w-0 flex-col border-r border-border">
              <div class="flex h-11 items-center justify-between border-b border-border px-3">
                <span class="text-sm font-medium">连接</span>
                <button
                  type="button"
                  class="flex size-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  title="添加 API 连接"
                  @click="startNewConnection"
                >
                  <PlusIcon class="size-4" />
                </button>
              </div>
              <div
                class="min-h-0 flex-1 space-y-1 overflow-y-auto p-2"
              >
                <template v-for="(connection, index) in connections" :key="connection.id">
                  <div
                    v-if="dropPosition === index"
                    class="pointer-events-none flex h-2 items-center px-3"
                  >
                    <span class="h-px w-full bg-primary/50" />
                  </div>
                  <div
                    :class="[
                      'flex items-center gap-1 rounded-md transition-opacity duration-150',
                      connection.id === selectedID
                        ? 'bg-accent text-accent-foreground'
                        : 'hover:bg-muted',
                      connection.id === draggingID ? 'opacity-55' : 'opacity-100',
                    ]"
                    data-connection-row
                    :data-connection-id="connection.id"
                    :data-connection-index="index"
                    class="cursor-grab touch-none select-none active:cursor-grabbing"
                    @pointerdown="beginPointerDrag($event, connection)"
                    @pointermove="updatePointerDropPosition"
                    @pointerup="finishPointerDrag"
                    @pointercancel="endDrag"
                  >
                    <GripVerticalIcon
                      class="ml-2 size-4 shrink-0 text-muted-foreground"
                      aria-hidden="true"
                    />
                    <button
                      type="button"
                      class="min-w-0 flex-1 px-2.5 py-2 text-left"
                    >
                      <span class="block truncate text-sm font-medium">{{ connection.name }}</span>
                      <span class="block truncate text-xs text-muted-foreground">
                        {{ connection.default_model || "未设置默认模型" }}
                      </span>
                    </button>
                  </div>
                </template>
                <div
                  v-if="dropPosition === connections.length"
                  class="pointer-events-none flex h-2 items-center px-3"
                >
                  <span class="h-px w-full bg-primary/50" />
                </div>
                <p v-if="!loading && connections.length === 0" class="px-2.5 py-4 text-xs text-muted-foreground">
                  添加一个 API 连接后即可选择模型。
                </p>
              </div>
            </section>

            <section class="flex min-w-0 flex-col">
              <div class="flex h-11 items-center justify-between border-b border-border px-4">
                <div>
                  <h3 class="text-sm font-medium">{{ isNewConnection ? "添加 API 连接" : "编辑 API 连接" }}</h3>
                </div>
                <Button
                  v-if="!isNewConnection"
                  variant="ghost"
                  size="sm"
                  :disabled="deleting || saving"
                  class="text-destructive hover:text-destructive"
                  @click="remove"
                >
                  <Trash2Icon class="size-4" />
                  删除
                </Button>
              </div>
              <div class="space-y-4 p-4">
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
                  <Label for="connection-model">默认模型</Label>
                  <Input id="connection-model" v-model="defaultModel" placeholder="例如：gpt-5" :disabled="loading" />
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
                <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
                <div class="flex justify-end pt-2">
                  <Button :disabled="saving || deleting || loading" @click="save">
                    <KeyRoundIcon class="size-4" />
                    {{ saving ? "保存中…" : isNewConnection ? "添加连接" : "保存连接" }}
                  </Button>
                </div>
              </div>
            </section>
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
        </div>
      </section>
    </SidebarInset>

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
            {{ dragPreview.model || "未设置默认模型" }}
          </span>
        </div>
      </div>
    </Teleport>
  </SidebarProvider>
</template>
