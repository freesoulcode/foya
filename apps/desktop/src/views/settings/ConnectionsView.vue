<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  AlertCircleIcon,
  ArrowLeftIcon,
  AudioLinesIcon,
  EyeIcon,
  EyeOffIcon,
  FilmIcon,
  GlobeIcon,
  GripVerticalIcon,
  ImagePlusIcon,
  PlusIcon,
  PlugZapIcon,
  RefreshCwIcon,
  SearchIcon,
  Settings2Icon,
  Trash2Icon,
  WrenchIcon,
} from "@lucide/vue";
import {
  api,
  type ConnectionConfig,
  type ModelSettings,
  type ReasoningEffort,
} from "@/lib/api";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface DragPreview {
  name: string;
  detail: string;
  x: number;
  y: number;
  width: number;
}

const reasoningEfforts: ReasoningEffort[] = ["low", "medium", "high"];
const connections = ref<ConnectionConfig[]>([]);
const selectedID = ref<string | null>(null);
const name = ref("");
const baseURL = ref("");
const modelSettings = ref<Record<string, ModelSettings>>({});
const modelContextWindowValue = ref("");
const modelMaxInputTokensValue = ref("");
const modelMaxOutputTokensValue = ref("");
const connectionModels = ref<Record<string, string[]>>({});
const modelEditor = ref<string | null>(null);
const showApiKey = ref(false);
const apiKey = ref("");
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
const connectionQuery = ref("");
const modelQuery = ref("");
const modelListEl = ref<HTMLElement | null>(null);
const modelScrollTop = ref(0);
const modelViewportHeight = ref(160);
const MODEL_ROW_HEIGHT = 40;
const MODEL_OVERSCAN = 8;

const selectedConnection = computed(
  () => connections.value.find((connection) => connection.id === selectedID.value) ?? null
);
const selectedConnectionError = computed(
  () => connectionErrors.value[selectedID.value ?? ""] ?? ""
);
const isNewConnection = computed(() => selectedID.value === null);
const selectedConnectionModels = computed(() =>
  selectedID.value ? connectionModels.value[selectedID.value] ?? [] : []
);
const filteredConnections = computed(() => {
  const query = connectionQuery.value.trim().toLowerCase();
  if (!query) return connections.value;
  return connections.value.filter((connection) =>
    `${connection.name} ${connection.base_url}`.toLowerCase().includes(query)
  );
});
const filteredConnectionModels = computed(() => {
  const query = modelQuery.value.trim().toLowerCase();
  if (!query) return selectedConnectionModels.value;
  return selectedConnectionModels.value.filter((model) =>
    model.toLowerCase().includes(query)
  );
});
const modelStartIndex = computed(() =>
  Math.max(0, Math.floor(modelScrollTop.value / MODEL_ROW_HEIGHT) - MODEL_OVERSCAN)
);
const modelEndIndex = computed(() =>
  Math.min(
    filteredConnectionModels.value.length,
    Math.ceil((modelScrollTop.value + modelViewportHeight.value) / MODEL_ROW_HEIGHT) +
      MODEL_OVERSCAN
  )
);
const visibleConnectionModels = computed(() =>
  filteredConnectionModels.value.slice(modelStartIndex.value, modelEndIndex.value)
);

const defaultModelSettings = (): ModelSettings => ({
  image_input: true,
  image_generation: true,
  video_generation: true,
  audio_generation: true,
  tool_calling: true,
  web_search: true,
  reasoning_efforts: [...reasoningEfforts],
});

function modelCapabilities(model: string): ModelSettings {
  const settings = modelSettings.value[model];
  if (!settings || !settings.capabilities_configured) {
    return {
      ...settings,
      ...defaultModelSettings(),
    };
  }
  return settings;
}

function onModelListScroll(event: Event) {
  modelScrollTop.value = (event.currentTarget as HTMLElement).scrollTop;
}

watch(modelQuery, () => {
  modelScrollTop.value = 0;
  modelListEl.value?.scrollTo({ top: 0 });
});

watch(
  modelListEl,
  (element, _, onCleanup) => {
    if (!element) return;
    const updateHeight = () => {
      modelViewportHeight.value = Math.max(1, element.clientHeight);
    };
    const observer = new ResizeObserver(updateHeight);
    observer.observe(element);
    updateHeight();
    onCleanup(() => observer.disconnect());
  },
  { flush: "post" }
);

watch(
  () => selectedConnectionModels.value.length,
  () => {
    modelScrollTop.value = 0;
    modelListEl.value?.scrollTo({ top: 0 });
  }
);

function resetForm(connection?: ConnectionConfig) {
  selectedID.value = connection?.id ?? null;
  modelEditor.value = null;
  modelQuery.value = "";
  modelScrollTop.value = 0;
  modelListEl.value?.scrollTo({ top: 0 });
  showApiKey.value = false;
  name.value = connection?.name ?? "";
  baseURL.value = connection?.base_url ?? "";
  modelSettings.value = { ...(connection?.model_settings ?? {}) };
  apiKey.value = connection?.api_key ?? "";
  error.value = "";
}

function setModelCapability(model: string, checked: boolean | "indeterminate") {
  if (checked === "indeterminate") return;
  modelSettings.value = {
    ...modelSettings.value,
    [model]: {
      ...(modelSettings.value[model] ?? {}),
      capabilities_configured: true,
      image_input: checked,
    },
  };
}

function setModelCapabilityFlag(
  model: string,
  key:
    | "image_generation"
    | "video_generation"
    | "audio_generation"
    | "tool_calling"
    | "web_search",
  checked: boolean | "indeterminate",
) {
  if (checked === "indeterminate") return;
  modelSettings.value = {
    ...modelSettings.value,
    [model]: {
      ...(modelSettings.value[model] ?? {}),
      capabilities_configured: true,
      [key]: checked,
    },
  };
}

function setReasoningEffort(
  model: string,
  effort: ReasoningEffort,
  checked: boolean | "indeterminate",
) {
  if (checked === "indeterminate") return;
  const current = modelSettings.value[model]?.reasoning_efforts ?? [];
  modelSettings.value = {
    ...modelSettings.value,
    [model]: {
      ...(modelSettings.value[model] ?? {}),
      reasoning_efforts: checked
        ? [...new Set([...current, effort])]
        : current.filter((item) => item !== effort),
    },
  };
}

function setTokenValue(value?: number): string {
  return value && value > 0 ? String(value) : "";
}

function updateModelTokenLimits() {
  if (!modelEditor.value) return;
  modelSettings.value = {
    ...modelSettings.value,
    [modelEditor.value]: {
      ...(modelSettings.value[modelEditor.value] ?? {}),
      context_window: parseTokenValue(modelContextWindowValue.value),
      max_input_tokens: parseTokenValue(modelMaxInputTokensValue.value),
      max_output_tokens: parseTokenValue(modelMaxOutputTokensValue.value),
    },
  };
}

function parseTokenValue(value: string): number | undefined {
  const parsed = Number(value.trim());
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

async function checkConnection(connection: ConnectionConfig, force = false) {
  const id = connection.id ?? "";
  if (!id) return;
  if (!force && connection.models_cached) {
    connectionModels.value = {
      ...connectionModels.value,
      [id]: connection.models ?? [],
    };
    connectionErrors.value = {
      ...connectionErrors.value,
      [id]: connection.models?.length ? "" : "未发现可用模型",
    };
    return;
  }
  try {
    const catalog = await api.listConnectionModels(id, force);
    connectionModels.value = {
      ...connectionModels.value,
      [id]: catalog.models,
    };
    connections.value = connections.value.map((item) =>
      item.id === id
        ? { ...item, models: catalog.models, models_cached: true }
        : item
    );
    connectionErrors.value = {
      ...connectionErrors.value,
      [id]: catalog.models.length === 0 ? "未发现可用模型" : "",
    };
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
    void Promise.all(connections.value.map((connection) => checkConnection(connection)));
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
  () => selectedID.value,
  () => {
    if (!selectedID.value) return;
    const connection = connections.value.find((item) => item.id === selectedID.value);
    if (connection) void checkConnection(connection);
  }
);

function selectConnection(connection: ConnectionConfig) {
  resetForm(connection);
  connectionEditorOpen.value = true;
}

function openModelEditor(model: string) {
  modelEditor.value = model;
  const defaults = defaultModelSettings();
  const current = modelSettings.value[model];
  modelSettings.value = {
    ...modelSettings.value,
    [model]: {
      ...defaults,
      ...current,
      reasoning_efforts:
        current?.reasoning_efforts ?? defaults.reasoning_efforts,
    },
  };
  const settings = modelSettings.value[model];
  modelContextWindowValue.value = setTokenValue(settings?.context_window);
  modelMaxInputTokensValue.value = setTokenValue(settings?.max_input_tokens);
  modelMaxOutputTokensValue.value = setTokenValue(settings?.max_output_tokens);
}

function closeModelEditor() {
  modelEditor.value = null;
}

function startNewConnection() {
  resetForm();
  connectionEditorOpen.value = true;
}

function formPayload(): ConnectionConfig {
  return {
    ...(selectedID.value ? { id: selectedID.value } : {}),
    name: name.value.trim() || "未命名连接",
    kind: "openai",
    auth_kind: "api_key",
    base_url: baseURL.value.trim(),
    model_settings: modelSettings.value,
    models: selectedConnection.value?.models ?? [],
    models_cached: selectedConnection.value?.models_cached ?? false,
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
    await checkConnection(saved, true);
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

void loadConnections();
</script>

<template>
  <SettingsPage
    title="连接"
    description="管理模型服务连接、API Key 和模型能力。"
    content-class="min-h-0 flex-1 overflow-hidden"
  >
    <template #actions>
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
    </template>

    <div class="grid h-full min-h-0 lg:grid-cols-[248px_1px_minmax(0,1fr)]">
      <section class="flex h-full min-h-0 flex-col overflow-hidden pr-6">
        <div class="flex items-center gap-2">
          <div class="relative min-w-0 flex-1">
            <SearchIcon class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              v-model="connectionQuery"
              class="h-9 pl-8 text-sm"
              placeholder="搜索连接"
              aria-label="搜索连接"
            />
          </div>
        </div>
        <div class="min-h-0 flex-1 space-y-1.5 overflow-y-auto pt-3">
          <template v-for="(connection, index) in filteredConnections" :key="connection.id">
            <div
              v-if="!connectionQuery && dropPosition === index"
              class="pointer-events-none flex h-2 items-center px-3"
            >
              <span class="h-px w-full bg-primary/50" />
            </div>
            <button
              type="button"
              :class="[
                'flex w-full cursor-grab touch-none select-none items-center gap-2 rounded-lg px-2.5 py-2.5 text-left transition-colors hover:bg-muted/50 active:cursor-grabbing',
                connection.id === selectedID ? 'border border-border bg-muted/60' : 'border border-transparent',
                connection.id === draggingID ? 'opacity-55' : 'opacity-100',
              ]"
              data-connection-row
              :data-connection-id="connection.id"
              :data-connection-index="connections.indexOf(connection)"
              @pointerdown="beginPointerDrag($event, connection)"
              @pointermove="updatePointerDropPosition"
              @pointerup="finishPointerDrag"
              @pointercancel="endDrag"
            >
              <span class="min-w-0 flex-1">
                <span class="block truncate text-[13px] font-medium">{{ connection.name }}</span>
                <span class="block truncate text-[11px] text-muted-foreground">
                  {{ connection.base_url }}
                </span>
              </span>
              <span
                class="size-2 shrink-0 rounded-full"
                :class="connectionErrors[connection.id ?? ''] ? 'bg-destructive' : 'bg-emerald-500'"
                :title="connectionErrors[connection.id ?? ''] ? '连接失败' : '连接可用'"
              />
            </button>
          </template>
          <div
            v-if="!connectionQuery && dropPosition === connections.length"
            class="pointer-events-none flex h-2 items-center px-3"
          >
            <span class="h-px w-full bg-primary/50" />
          </div>
          <div
            v-if="!loading && filteredConnections.length === 0"
            class="flex min-h-48 flex-col items-center justify-center gap-3 px-4 text-center"
          >
            <PlugZapIcon class="size-8 text-muted-foreground/50" />
            <p class="text-xs text-muted-foreground">
              {{ connectionQuery ? "没有匹配的连接" : "暂无模型连接" }}
            </p>
          </div>
        </div>
        <Button
          class="mt-3 w-full shrink-0"
          variant="outline"
          size="sm"
          :disabled="loading || saving"
          @click="startNewConnection"
        >
          <PlusIcon class="size-4" />
          添加连接
        </Button>
      </section>

      <div class="hidden h-full w-px bg-border lg:block" />

      <section
        v-if="connectionEditorOpen"
        class="flex min-h-0 min-w-0 flex-col overflow-hidden lg:pl-8"
      >
        <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-1 pb-3">
          <div class="flex min-w-0 flex-1 items-center gap-3">
            <Button
              v-if="modelEditor"
              size="icon-sm"
              variant="ghost"
              title="返回连接设置"
              aria-label="返回连接设置"
              @click="closeModelEditor"
            >
              <ArrowLeftIcon class="size-4" />
            </Button>
            <div class="min-w-0 flex-1">
              <h3 class="truncate text-lg font-semibold tracking-normal">
                {{ modelEditor ?? (isNewConnection ? "新建连接" : selectedConnection?.name) }}
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
              {{ saving ? "保存中..." : isNewConnection ? "添加连接" : "保存" }}
            </Button>
          </div>
        </div>
        <div v-if="!modelEditor" class="flex min-h-0 max-w-4xl flex-1 flex-col gap-5 overflow-hidden py-6">
          <div class="space-y-1.5">
            <Label for="connection-name">名称</Label>
            <Input id="connection-name" v-model="name" class="h-9 text-sm" placeholder="例如：OpenAI 个人" :disabled="loading" />
          </div>
          <div class="space-y-1.5">
            <Label for="connection-url">Base URL</Label>
            <Input
              id="connection-url"
              v-model="baseURL"
              placeholder="https://api.openai.com/v1"
              class="h-9 text-sm"
              :disabled="loading"
            />
          </div>
          <div class="space-y-1.5">
            <Label for="connection-key">API Key</Label>
            <div class="relative">
              <Input
                id="connection-key"
                v-model="apiKey"
                :type="showApiKey ? 'text' : 'password'"
                placeholder="输入 API Key"
                class="h-9 pr-10 text-sm"
                :disabled="loading"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                class="absolute right-1 top-1/2 -translate-y-1/2 text-muted-foreground"
                :title="showApiKey ? '隐藏 API Key' : '显示 API Key'"
                :aria-label="showApiKey ? '隐藏 API Key' : '显示 API Key'"
                @click="showApiKey = !showApiKey"
              >
                <EyeOffIcon v-if="showApiKey" class="size-4" />
                <EyeIcon v-else class="size-4" />
              </Button>
            </div>
          </div>
          <div class="flex min-h-0 flex-1 flex-col space-y-2">
            <div class="flex items-center justify-between gap-3">
              <p class="text-sm font-medium">模型</p>
              <Button
                size="sm"
                variant="outline"
                :disabled="loading || saving"
                @click="selectedConnection && checkConnection(selectedConnection, true)"
              >
                <RefreshCwIcon class="size-4" />
                获取模型列表
              </Button>
            </div>
            <div v-if="selectedConnectionModels.length" class="flex min-h-0 flex-1 flex-col gap-2">
              <div class="relative">
                <SearchIcon class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  v-model="modelQuery"
                  class="h-9 pl-8 text-sm"
                  placeholder="搜索模型"
                  aria-label="搜索模型"
                />
              </div>
              <div
                ref="modelListEl"
                class="min-h-0 flex-1 overflow-y-auto pr-1"
                @scroll="onModelListScroll"
              >
                <div
                  class="relative"
                  :style="{ height: `${filteredConnectionModels.length * MODEL_ROW_HEIGHT}px` }"
                >
                  <div
                    v-for="(model, index) in visibleConnectionModels"
                    :key="model"
                    class="absolute inset-x-0 flex h-10 items-center gap-3 px-2.5"
                    :style="{ top: `${(modelStartIndex + index) * MODEL_ROW_HEIGHT}px` }"
                  >
                    <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ model }}</span>
                    <div class="flex shrink-0 items-center gap-0.5 text-muted-foreground">
                      <Tooltip v-if="modelCapabilities(model).image_input">
                        <TooltipTrigger as-child>
                          <span class="flex size-6 items-center justify-center">
                            <EyeIcon class="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>支持图片输入</TooltipContent>
                      </Tooltip>
                      <Tooltip v-if="modelCapabilities(model).image_generation">
                        <TooltipTrigger as-child>
                          <span class="flex size-6 items-center justify-center">
                            <ImagePlusIcon class="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>支持生成图片</TooltipContent>
                      </Tooltip>
                      <Tooltip v-if="modelCapabilities(model).video_generation">
                        <TooltipTrigger as-child>
                          <span class="flex size-6 items-center justify-center">
                            <FilmIcon class="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>支持生成视频</TooltipContent>
                      </Tooltip>
                      <Tooltip v-if="modelCapabilities(model).audio_generation">
                        <TooltipTrigger as-child>
                          <span class="flex size-6 items-center justify-center">
                            <AudioLinesIcon class="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>支持生成音频</TooltipContent>
                      </Tooltip>
                      <Tooltip v-if="modelCapabilities(model).tool_calling">
                        <TooltipTrigger as-child>
                          <span class="flex size-6 items-center justify-center">
                            <WrenchIcon class="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>支持工具调用</TooltipContent>
                      </Tooltip>
                      <Tooltip v-if="modelCapabilities(model).web_search">
                        <TooltipTrigger as-child>
                          <span class="flex size-6 items-center justify-center">
                            <GlobeIcon class="size-3.5" />
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>支持联网</TooltipContent>
                      </Tooltip>
                    </div>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      title="模型设置"
                      :aria-label="`${model} 模型设置`"
                      @click="openModelEditor(model)"
                    >
                      <Settings2Icon class="size-4" />
                    </Button>
                  </div>
                </div>
                <p v-if="filteredConnectionModels.length === 0" class="px-2 py-6 text-center text-xs text-muted-foreground">
                  没有匹配的模型
                </p>
              </div>
            </div>
          </div>
        </div>
        <div v-else class="max-w-4xl space-y-7 py-6">
          <div>
            <h4 class="text-base font-semibold">模型设置</h4>
          </div>

          <section class="space-y-3">
            <h5 class="text-sm font-medium">模型能力</h5>
            <div class="flex flex-wrap gap-2">
              <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).image_input"
                  :disabled="loading"
                  @update:model-value="setModelCapability(modelEditor, $event)"
                />
                <span class="text-sm">支持图片输入</span>
              </label>
                  <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                    <Checkbox
                      :model-value="modelCapabilities(modelEditor).image_generation"
                      :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'image_generation', $event)"
                    />
                    <ImagePlusIcon class="size-4 text-muted-foreground" />
                    <span class="text-sm">生成图片</span>
                  </label>
                  <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                    <Checkbox
                      :model-value="modelCapabilities(modelEditor).video_generation"
                      :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'video_generation', $event)"
                    />
                    <FilmIcon class="size-4 text-muted-foreground" />
                    <span class="text-sm">生成视频</span>
                  </label>
                  <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                    <Checkbox
                      :model-value="modelCapabilities(modelEditor).audio_generation"
                      :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'audio_generation', $event)"
                    />
                    <AudioLinesIcon class="size-4 text-muted-foreground" />
                    <span class="text-sm">生成音频</span>
                  </label>
              <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).tool_calling"
                  :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'tool_calling', $event)"
                />
                <span class="text-sm">支持工具调用</span>
              </label>
              <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).web_search"
                  :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'web_search', $event)"
                />
                <span class="text-sm">支持联网</span>
              </label>
            </div>
          </section>

          <section class="space-y-3">
            <h5 class="text-sm font-medium">思考强度</h5>
            <div class="flex flex-wrap gap-2">
              <label
                v-for="effort in reasoningEfforts"
                :key="effort"
                class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground"
              >
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).reasoning_efforts?.includes(effort)"
                  :disabled="loading"
                  @update:model-value="setReasoningEffort(modelEditor, effort, $event)"
                />
                <span class="text-sm">{{ effort }}</span>
              </label>
            </div>
          </section>

          <section class="grid max-w-4xl gap-4 sm:grid-cols-3">
            <div class="space-y-1.5">
              <Label for="model-context-window">上下文长度</Label>
              <Input
                id="model-context-window"
                v-model="modelContextWindowValue"
                type="number"
                min="0"
                step="1"
                placeholder="使用服务默认值"
                :disabled="loading"
                @change="updateModelTokenLimits"
              />
            </div>
            <div class="space-y-1.5">
              <Label for="model-max-input-tokens">最大输入 Token</Label>
              <Input
                id="model-max-input-tokens"
                v-model="modelMaxInputTokensValue"
                type="number"
                min="0"
                step="1"
                placeholder="使用服务默认值"
                :disabled="loading"
                @change="updateModelTokenLimits"
              />
            </div>
            <div class="space-y-1.5">
              <Label for="model-max-output-tokens">最大输出 Token</Label>
              <Input
                id="model-max-output-tokens"
                v-model="modelMaxOutputTokensValue"
                type="number"
                min="0"
                step="1"
                placeholder="使用服务默认值"
                :disabled="loading"
                @change="updateModelTokenLimits"
              />
            </div>
          </section>
        </div>
        <div class="mt-auto flex min-h-10 shrink-0 items-center justify-between gap-3 px-1">
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
      <section
        v-else
        class="flex min-h-0 flex-col items-center justify-center rounded-xl border border-dashed border-border bg-background px-6 text-center lg:ml-8"
      >
        <PlugZapIcon class="size-10 text-muted-foreground/40" />
        <p class="mt-3 text-sm font-medium">选择一个模型连接</p>
        <p class="mt-1 max-w-sm text-xs leading-5 text-muted-foreground">
          在左侧选择连接，配置 API 地址、密钥和具体模型的视觉能力。
        </p>
      </section>
    </div>
  </SettingsPage>

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
</template>
