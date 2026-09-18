<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  AlertCircleIcon,
  ArrowLeftIcon,
  AudioLinesIcon,
  EyeIcon,
  EyeOffIcon,
  FilmIcon,
  GripVerticalIcon,
  ImagePlusIcon,
  PencilIcon,
  PlusIcon,
  PlugZapIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
  XIcon,
} from "@lucide/vue";
import {
  api,
  type ConnectionConfig,
  type ConnectionType,
  type ModelSettings,
  type ReasoningEffort,
  type VideoProtocol,
} from "@/lib/api";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  normalizeTokenLimitInput,
  tokenLimitEditorValue,
  type TokenLimitValue,
} from "@/views/settings/modelTokenLimits";
import {
  addConfiguredModels,
  normalizeModelID,
  removeConfiguredModel,
} from "@/views/settings/connectionModels";

interface DragPreview {
  name: string;
  detail: string;
  x: number;
  y: number;
  width: number;
}

const { t } = useI18n();
const reasoningEfforts: ReasoningEffort[] = ["low", "medium", "high"];
const connections = ref<ConnectionConfig[]>([]);
const selectedID = ref<string | null>(null);
const name = ref("");
const connectionType = ref<ConnectionType>("language");
const videoProtocol = ref<VideoProtocol | "">("");
const baseURL = ref("");
const modelSettings = ref<Record<string, ModelSettings>>({});
const importedModels = ref<Set<string>>(new Set());
const pendingImportedModels = ref<Set<string>>(new Set());
const modelImportOpen = ref(false);
const modelImportQuery = ref("");
const modelImportLoading = ref(false);
const modelImportError = ref("");
const modelDiscoveryGeneration = ref(0);
const manualModelID = ref("");
const addingManualModel = ref(false);
const modelContextWindowValue = ref<TokenLimitValue>(undefined);
const modelMaxInputTokensValue = ref<TokenLimitValue>(undefined);
const modelMaxOutputTokensValue = ref<TokenLimitValue>(undefined);
const connectionModels = ref<Record<string, string[]>>({});
const discoveredModelSettings = ref<Record<string, Partial<ModelSettings>>>({});
const modelEditor = ref<string | null>(null);
const showApiKey = ref(false);
const apiKey = ref("");
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
const savedFormSnapshot = ref("");
const MODEL_ROW_HEIGHT = 48;
const MODEL_OVERSCAN = 8;

const selectedConnection = computed(
  () => connections.value.find((connection) => connection.id === selectedID.value) ?? null
);
const isNewConnection = computed(() => selectedID.value === null);
const availableConnectionModels = computed(() =>
  connectionModels.value[selectedID.value ?? "draft"] ?? []
);
const selectedConnectionModels = computed(() => [...importedModels.value]);
const canAddManualModel = computed(() => {
  const model = normalizeModelID(manualModelID.value);
  return model !== "" && !importedModels.value.has(model);
});
const showModelSearch = computed(() => selectedConnectionModels.value.length > 5);
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
const hasUnsavedChanges = computed(
  () => connectionEditorOpen.value && serializeFormState() !== savedFormSnapshot.value
);
const discoveryEndpointLabel = computed(() => {
  if (baseURL.value.trim()) return baseURL.value.trim();
  if (connectionType.value === "video") {
    return videoProtocol.value === "minimax_h3"
      ? "https://api.minimax.io"
      : "https://ark.cn-beijing.volces.com";
  }
  return "https://api.openai.com/v1";
});

const defaultModelSettings = (type = connectionType.value): ModelSettings => ({
  image_input: true,
  image_generation: type === "image",
  video_generation: type === "video",
  audio_generation: false,
  tool_calling: type === "language",
  web_search: type === "language",
  reasoning_efforts: [...reasoningEfforts],
});

function modelCapabilities(model: string): ModelSettings {
  return {
    ...defaultModelSettings(),
    ...modelSettings.value[model],
  };
}

function formatTokenLimit(value?: number) {
  if (!value) return "";
  if (value >= 1_000_000) {
    const millions = value / 1_000_000;
    return `${Number.isInteger(millions) ? millions : millions.toFixed(1)}M`;
  }
  if (value >= 1_000) return `${Math.round(value / 1_000)}K`;
  return String(value);
}

function connectionTypeLabel(type?: ConnectionType) {
  if (type === "language") return t("Language");
  if (type === "image") return t("Image");
  if (type === "video") return t("Video");
  return t("Type not set");
}

function serializeFormState() {
  const models = [...importedModels.value];
  return JSON.stringify({
    name: name.value,
    type: connectionType.value,
    video_protocol: videoProtocol.value,
    base_url: baseURL.value,
    api_key: apiKey.value,
    models,
    model_settings: Object.fromEntries(
      models.map((model) => [model, modelSettings.value[model] ?? defaultModelSettings()])
    ),
  });
}

const filteredImportModels = computed(() => {
  const query = modelImportQuery.value.trim().toLowerCase();
  const available = availableConnectionModels.value.filter(
    (model) => !importedModels.value.has(model)
  );
  return query
    ? available.filter((model) => model.toLowerCase().includes(query))
    : available;
});
const allImportModelsSelected = computed(
  () => filteredImportModels.value.length > 0 &&
    filteredImportModels.value.every((model) => pendingImportedModels.value.has(model))
);

function setPendingModelImported(model: string, checked: boolean | "indeterminate") {
  if (checked === "indeterminate") return;
  const next = new Set(pendingImportedModels.value);
  checked ? next.add(model) : next.delete(model);
  pendingImportedModels.value = next;
}

function setAllModelsImported(checked: boolean | "indeterminate") {
  if (checked === "indeterminate") return;
  const next = new Set(pendingImportedModels.value);
  for (const model of filteredImportModels.value) {
    checked ? next.add(model) : next.delete(model);
  }
  pendingImportedModels.value = next;
}

async function openModelImporter() {
  const requestID = ++modelDiscoveryGeneration.value;
  modelImportLoading.value = true;
  modelImportError.value = "";
  modelImportQuery.value = "";
  pendingImportedModels.value = new Set();
  modelImportOpen.value = true;
  await discoverConnectionModels(requestID);
  if (requestID === modelDiscoveryGeneration.value) {
    modelImportLoading.value = false;
  }
}

function confirmModelImport() {
  const configured = addConfiguredModels(
    importedModels.value,
    modelSettings.value,
    pendingImportedModels.value,
    () => defaultModelSettings(),
  );
  for (const model of pendingImportedModels.value) {
    configured.settings[model] = {
      ...configured.settings[model],
      ...discoveredModelSettings.value[model],
    };
  }
  importedModels.value = configured.models;
  modelSettings.value = configured.settings;
  pendingImportedModels.value = new Set();
  modelImportOpen.value = false;
}

function addManualModel() {
  if (!canAddManualModel.value) return;
  const configured = addConfiguredModels(
    importedModels.value,
    modelSettings.value,
    [manualModelID.value],
    defaultModelSettings,
  );
  importedModels.value = configured.models;
  modelSettings.value = configured.settings;
  manualModelID.value = "";
  addingManualModel.value = false;
}

async function startAddingModel() {
  addingManualModel.value = true;
  await nextTick();
  document.getElementById("connection-model-id")?.focus();
}

function cancelAddingModel() {
  manualModelID.value = "";
  addingManualModel.value = false;
}

function removeModel(model: string) {
  if (modelEditor.value === model) modelEditor.value = null;
  const configured = removeConfiguredModel(
    importedModels.value,
    modelSettings.value,
    model,
  );
  importedModels.value = configured.models;
  modelSettings.value = configured.settings;
  const pending = new Set(pendingImportedModels.value);
  pending.delete(model);
  pendingImportedModels.value = pending;
}

function onModelListScroll(event: Event) {
  modelScrollTop.value = (event.currentTarget as HTMLElement).scrollTop;
}

watch(modelQuery, () => {
  modelScrollTop.value = 0;
  modelListEl.value?.scrollTo({ top: 0 });
});

watch(showModelSearch, (visible) => {
  if (!visible) modelQuery.value = "";
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
  manualModelID.value = "";
  addingManualModel.value = false;
  modelScrollTop.value = 0;
  modelListEl.value?.scrollTo({ top: 0 });
  showApiKey.value = false;
  name.value = connection?.name ?? "";
  connectionType.value = connection?.type ?? "language";
  videoProtocol.value = connection?.video_protocol ?? "";
  baseURL.value = connection?.base_url ?? "";
  modelSettings.value = { ...(connection?.model_settings ?? {}) };
  importedModels.value = new Set(connection?.models ?? []);
  apiKey.value = connection?.api_key ?? "";
  error.value = "";
  savedFormSnapshot.value = serializeFormState();
}

function setModelCapability(model: string, checked: boolean | "indeterminate") {
  if (checked === "indeterminate") return;
  modelSettings.value = {
    ...modelSettings.value,
    [model]: {
      ...modelCapabilities(model),
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
      ...modelCapabilities(model),
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

function updateModelTokenLimits() {
  if (!modelEditor.value) return;
  modelSettings.value = {
    ...modelSettings.value,
    [modelEditor.value]: {
      ...(modelSettings.value[modelEditor.value] ?? {}),
      context_window: modelContextWindowValue.value,
      max_input_tokens: modelMaxInputTokensValue.value,
      max_output_tokens: modelMaxOutputTokensValue.value,
    },
  };
}

function updateTokenInput(target: "context_window" | "max_input_tokens" | "max_output_tokens", value: string | number) {
  const normalized = normalizeTokenLimitInput(value);
  if (target === "context_window") modelContextWindowValue.value = normalized;
  else if (target === "max_input_tokens") modelMaxInputTokensValue.value = normalized;
  else modelMaxOutputTokensValue.value = normalized;
  updateModelTokenLimits();
}

function preventNonNumericTokenInput(event: KeyboardEvent) {
  if (event.metaKey || event.ctrlKey || event.altKey) return;
  if (event.key.length === 1 && !/^\d$/.test(event.key)) {
    event.preventDefault();
  }
}

function preventNonNumericTokenPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData("text") ?? "";
  if (text && !/^\d+$/.test(text.trim())) {
    event.preventDefault();
  }
}

async function discoverConnectionModels(requestID: number) {
  const catalogKey = selectedID.value ?? "draft";
  const payload = formPayload();
  try {
    const catalog = await api.discoverConnectionModels(payload);
    if (
      requestID !== modelDiscoveryGeneration.value ||
      catalogKey !== (selectedID.value ?? "draft")
    ) return;
    connectionModels.value = { ...connectionModels.value, [catalogKey]: catalog.models };
    discoveredModelSettings.value = Object.fromEntries(
      catalog.models.map((model) => [
        model,
        {
          ...(catalog.context_windows[model]
            ? { context_window: catalog.context_windows[model] }
            : {}),
          ...(catalog.capabilities?.[model]?.image_input !== undefined
            ? { image_input: catalog.capabilities[model].image_input }
            : {}),
        },
      ])
    );
  } catch (cause) {
    if (
      requestID !== modelDiscoveryGeneration.value ||
      catalogKey !== (selectedID.value ?? "draft")
    ) return;
    connectionModels.value = { ...connectionModels.value, [catalogKey]: [] };
    discoveredModelSettings.value = {};
    modelImportError.value = String(cause);
  }
}

async function loadConnections() {
  loading.value = true;
  error.value = "";
  try {
    connections.value = await api.listConnections();
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
  modelContextWindowValue.value = tokenLimitEditorValue(settings?.context_window);
  modelMaxInputTokensValue.value = tokenLimitEditorValue(settings?.max_input_tokens);
  modelMaxOutputTokensValue.value = tokenLimitEditorValue(settings?.max_output_tokens);
}

function closeModelEditor() {
  updateModelTokenLimits();
  modelEditor.value = null;
}

function startNewConnection() {
  resetForm();
  connectionEditorOpen.value = true;
}

function updateConnectionType(value: unknown) {
  if (value !== "language" && value !== "image" && value !== "video") return;
  connectionType.value = value;
  videoProtocol.value = "";
  modelSettings.value = Object.fromEntries(
    [...importedModels.value].map((model) => [model, defaultModelSettings(value)])
  );
}

function updateVideoProtocol(value: unknown) {
  if (value !== "seedance" && value !== "minimax_h3") return;
  videoProtocol.value = value;
  importedModels.value = new Set();
  modelSettings.value = {};
  if (selectedID.value) {
    connectionModels.value = { ...connectionModels.value, [selectedID.value]: [] };
  }
}

function formPayload(): ConnectionConfig {
  const type = connectionType.value || "language";
  const importedSettings = Object.fromEntries(
    [...importedModels.value].map((model) => [
      model,
      modelSettings.value[model] ?? defaultModelSettings(type),
    ])
  );
  return {
    ...(selectedID.value ? { id: selectedID.value } : {}),
    name: name.value.trim() || t("Unnamed connection"),
    type,
    video_protocol: type === "video" ? videoProtocol.value || undefined : undefined,
    kind: "openai",
    auth_kind: "api_key",
    base_url: baseURL.value.trim(),
    model_settings: importedSettings,
    models: [...importedModels.value],
    models_cached: true,
    sort_order: selectedConnection.value?.sort_order ?? connections.value.length,
    ...(apiKey.value.trim() ? { api_key: apiKey.value.trim() } : {}),
  };
}

async function save() {
  saving.value = true;
  error.value = "";
  try {
    updateModelTokenLimits();
    const payload = formPayload();
    const saved = selectedID.value
      ? await api.updateConnection(selectedID.value, payload)
      : await api.createConnection(payload);
    const index = connections.value.findIndex((connection) => connection.id === saved.id);
    if (index >= 0) connections.value[index] = saved;
    else connections.value.push(saved);
    resetForm(saved);
    connectionEditorOpen.value = true;
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
    const remainingModels = { ...connectionModels.value };
    delete remainingModels[removedID];
    connectionModels.value = remainingModels;
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
    :title="$t('Model settings')"
    :description="$t('Manage model providers, API keys, and model capabilities.')"
    content-class="min-h-0 flex-1 overflow-hidden"
  >
    <template #actions>
      <Button
        size="icon-sm"
        variant="ghost"
        :disabled="loading || saving || deleting"
        :title="$t('Refresh model connections')"
        :aria-label="$t('Refresh model connections')"
        @click="loadConnections"
      >
        <RefreshCwIcon class="size-4" :class="loading && 'animate-spin'" />
      </Button>
    </template>

    <div class="grid h-full min-h-0 grid-cols-[220px_minmax(0,1fr)] overflow-hidden rounded-md border border-border sm:grid-cols-[248px_minmax(0,1fr)]">
      <section class="flex h-full min-h-0 flex-col overflow-hidden border-r border-border bg-muted/15 p-4">
        <p class="mb-3 text-xs font-medium text-muted-foreground">{{ $t("Providers") }}</p>
        <div class="flex items-center gap-2">
          <div class="relative min-w-0 flex-1">
            <SearchIcon class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              v-model="connectionQuery"
              class="h-9 pl-8 text-sm"
              :placeholder="$t('Search providers')"
              :aria-label="$t('Search providers')"
            />
          </div>
        </div>
        <div class="model-settings-scrollbar min-h-0 flex-1 space-y-1.5 overflow-y-auto pt-3">
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
                  {{ connectionTypeLabel(connection.type) }} · {{ connection.base_url }}
                </span>
              </span>
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
              {{ connectionQuery ? $t("No matching providers") : $t("No model providers") }}
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
          {{ $t("Add provider") }}
        </Button>
      </section>

      <section
        v-if="connectionEditorOpen"
        class="flex min-h-0 min-w-0 flex-col overflow-hidden px-6"
      >
        <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-1 pb-3">
          <div class="flex min-w-0 flex-1 items-center gap-3">
            <Button
              v-if="modelEditor"
              size="icon-sm"
              variant="ghost"
              :title="$t('Back to connection settings')"
              :aria-label="$t('Back to connection settings')"
              @click="closeModelEditor"
            >
              <ArrowLeftIcon class="size-4" />
            </Button>
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 items-center gap-2">
                <h3 class="truncate text-lg font-semibold tracking-normal">
                  {{ modelEditor ?? (isNewConnection ? $t("New provider") : selectedConnection?.name) }}
                </h3>
                <span
                  v-if="hasUnsavedChanges"
                  class="flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground"
                >
                  <span class="size-1.5 rounded-full bg-amber-500" />
                  {{ $t("Unsaved changes") }}
                </span>
              </div>
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
              :title="$t('Delete provider')"
              :aria-label="$t('Delete provider')"
              @click="remove"
            >
              <Trash2Icon class="size-4" />
            </Button>
            <Button
              :disabled="saving || deleting || loading || (!isNewConnection && !hasUnsavedChanges) || (connectionType === 'video' && !videoProtocol)"
              @click="save"
            >
              {{ saving ? $t("Saving") : isNewConnection ? $t("Add provider") : $t("Save") }}
            </Button>
          </div>
        </div>
        <div v-if="!modelEditor" class="model-settings-scrollbar min-h-0 max-w-3xl flex-1 space-y-5 overflow-y-auto py-6 pr-2">
          <div class="space-y-1.5">
            <Label for="connection-name" class="text-sm">{{ $t("Name") }}</Label>
            <Input id="connection-name" v-model="name" class="h-9 text-sm" :placeholder="$t('For example: Personal OpenAI')" :disabled="loading" />
          </div>
          <div class="space-y-1.5">
            <Label class="text-sm">{{ $t("Connection type") }}</Label>
            <Select :model-value="connectionType" @update:model-value="updateConnectionType">
              <SelectTrigger class="h-9 w-full">
                <SelectValue :placeholder="$t('Select connection type')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="language">{{ $t("Language model") }}</SelectItem>
                <SelectItem value="image">{{ $t("Image model") }}</SelectItem>
                <SelectItem value="video">{{ $t("Video model") }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="connectionType === 'video'" class="space-y-1.5">
            <Label class="text-sm">{{ $t("Video protocol") }}</Label>
            <Select :model-value="videoProtocol" @update:model-value="updateVideoProtocol">
              <SelectTrigger class="h-9 w-full">
                <SelectValue :placeholder="$t('Select video protocol')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="seedance">Seedance</SelectItem>
                <SelectItem value="minimax_h3">MiniMax H3</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div class="space-y-1.5">
            <Label for="connection-url" class="text-sm">Base URL</Label>
            <Input
              id="connection-url"
              v-model="baseURL"
              :placeholder="connectionType === 'video'
                ? videoProtocol === 'minimax_h3'
                  ? 'https://api.minimax.io'
                  : 'https://ark.cn-beijing.volces.com'
                : 'https://api.openai.com/v1'"
              class="h-9 text-sm"
              :disabled="loading"
            />
          </div>
          <div class="space-y-1.5">
            <Label for="connection-key" class="text-sm">API Key</Label>
            <div class="relative">
              <Input
                id="connection-key"
                v-model="apiKey"
                :type="showApiKey ? 'text' : 'password'"
                :placeholder="selectedConnection?.has_api_key
                  ? $t('Saved; leave blank to keep it')
                  : $t('Enter API Key')"
                class="h-9 pr-10 text-sm"
                :disabled="loading"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                class="absolute right-1 top-1/2 -translate-y-1/2 text-muted-foreground"
                :title="showApiKey ? $t('Hide API Key') : $t('Show API Key')"
                :aria-label="showApiKey ? $t('Hide API Key') : $t('Show API Key')"
                @click="showApiKey = !showApiKey"
              >
                <EyeOffIcon v-if="showApiKey" class="size-4" />
                <EyeIcon v-else class="size-4" />
              </Button>
            </div>
          </div>
          <div class="space-y-3 pt-1">
            <div class="flex items-center justify-between gap-3">
              <Label class="text-sm">{{ $t("Model list") }}</Label>
              <Button
                size="xs"
                variant="ghost"
                :disabled="loading || saving || (connectionType === 'video' && (!videoProtocol || (videoProtocol === 'seedance' && !baseURL.trim())))"
                @click="openModelImporter"
              >
                <RefreshCwIcon class="size-4" />
                {{ $t("Discover models") }}
              </Button>
            </div>
            <InputGroup v-if="addingManualModel">
              <InputGroupInput
                id="connection-model-id"
                v-model="manualModelID"
                class="font-mono text-sm"
                :placeholder="$t('Enter model ID')"
                :aria-label="$t('Model ID')"
                :disabled="loading || saving"
                @keydown.enter.prevent="addManualModel"
              />
              <InputGroupAddon align="inline-end">
                <InputGroupButton
                  size="icon-sm"
                  :title="$t('Cancel')"
                  :aria-label="$t('Cancel')"
                  @click="cancelAddingModel"
                >
                  <XIcon class="size-4" />
                </InputGroupButton>
                <InputGroupButton
                  size="icon-sm"
                  :disabled="loading || saving || !canAddManualModel"
                  :title="$t('Add model')"
                  :aria-label="$t('Add model')"
                  @click="addManualModel"
                >
                  <PlusIcon class="size-4" />
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
            <div v-if="selectedConnectionModels.length" class="flex min-h-0 flex-1 flex-col gap-2">
              <div v-if="showModelSearch" class="relative">
                <SearchIcon class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  v-model="modelQuery"
                  class="h-9 pl-8 text-sm"
                  :placeholder="$t('Search imported models')"
                  :aria-label="$t('Search imported models')"
                />
              </div>
              <div
                ref="modelListEl"
                class="model-settings-scrollbar min-h-0 max-h-72 overflow-y-auto rounded-md border border-border"
                @scroll="onModelListScroll"
              >
                <div
                  class="relative"
                  :style="{ height: `${filteredConnectionModels.length * MODEL_ROW_HEIGHT}px` }"
                >
                  <div
                    v-for="(model, index) in visibleConnectionModels"
                    :key="model"
                    class="absolute inset-x-0 flex h-12 items-center gap-2 border-b border-border px-3 last:border-b-0"
                    :style="{ top: `${(modelStartIndex + index) * MODEL_ROW_HEIGHT}px` }"
                  >
                    <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ model }}</span>
                    <span
                      v-if="formatTokenLimit(modelCapabilities(model).context_window)"
                      class="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground"
                    >
                      {{ formatTokenLimit(modelCapabilities(model).context_window) }}
                    </span>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      class="text-muted-foreground"
                      :title="$t('Model settings')"
                      :aria-label="$t('{model} settings', { model })"
                      @click="openModelEditor(model)"
                    >
                      <PencilIcon class="size-4" />
                    </Button>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      class="text-muted-foreground hover:text-destructive"
                      :disabled="saving"
                      :title="$t('Remove model')"
                      :aria-label="$t('Remove {model}', { model })"
                      @click="removeModel(model)"
                    >
                      <Trash2Icon class="size-4" />
                    </Button>
                  </div>
                </div>
                <p v-if="filteredConnectionModels.length === 0" class="px-2 py-6 text-center text-xs text-muted-foreground">
                  {{ $t("No matching models") }}
                </p>
              </div>
            </div>
            <div v-else class="grid min-h-24 place-items-center rounded-md border border-dashed border-border text-xs text-muted-foreground">
              {{ $t("No models configured") }}
            </div>
            <Button
              v-if="!addingManualModel"
              size="sm"
              variant="secondary"
              class="w-fit"
              :disabled="loading || saving"
              @click="startAddingModel"
            >
              <PlusIcon class="size-4" />
              {{ $t("Add model") }}
            </Button>
          </div>
        </div>
        <div v-else class="model-settings-scrollbar min-h-0 max-w-4xl flex-1 space-y-7 overflow-y-auto py-6 pr-2">
          <div>
            <h4 class="text-base font-semibold">{{ $t("Model settings") }}</h4>
          </div>

          <section class="space-y-3">
            <h5 class="text-sm font-medium">{{ $t("Model capabilities") }}</h5>
            <div class="flex flex-wrap gap-2">
              <label class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).image_input"
                  :disabled="loading"
                  @update:model-value="setModelCapability(modelEditor, $event)"
                />
                <span class="text-sm">{{ $t("Supports image input") }}</span>
              </label>
                  <label v-if="connectionType === 'image'" class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                    <Checkbox
                      :model-value="modelCapabilities(modelEditor).image_generation"
                      :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'image_generation', $event)"
                    />
                    <ImagePlusIcon class="size-4 text-muted-foreground" />
                    <span class="text-sm">{{ $t("Generate images") }}</span>
                  </label>
                  <label v-if="connectionType === 'video'" class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                    <Checkbox
                      :model-value="modelCapabilities(modelEditor).video_generation"
                      :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'video_generation', $event)"
                    />
                    <FilmIcon class="size-4 text-muted-foreground" />
                    <span class="text-sm">{{ $t("Generate videos") }}</span>
                  </label>
                  <label v-if="connectionType === 'language'" class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                    <Checkbox
                      :model-value="modelCapabilities(modelEditor).audio_generation"
                      :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'audio_generation', $event)"
                    />
                    <AudioLinesIcon class="size-4 text-muted-foreground" />
                    <span class="text-sm">{{ $t("Generate audio") }}</span>
                  </label>
              <label v-if="connectionType === 'language'" class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).tool_calling"
                  :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'tool_calling', $event)"
                />
                <span class="text-sm">{{ $t("Supports tool calls") }}</span>
              </label>
              <label v-if="connectionType === 'language'" class="flex cursor-pointer items-center gap-2 py-1.5 pr-4 transition-colors hover:text-foreground">
                <Checkbox
                  :model-value="modelCapabilities(modelEditor).web_search"
                  :disabled="loading"
                      @update:model-value="setModelCapabilityFlag(modelEditor, 'web_search', $event)"
                />
                <span class="text-sm">{{ $t("Supports web access") }}</span>
              </label>
            </div>
          </section>

          <section v-if="connectionType === 'language'" class="space-y-3">
            <h5 class="text-sm font-medium">{{ $t("Reasoning effort") }}</h5>
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

          <section v-if="connectionType === 'language'" class="grid max-w-4xl gap-4 sm:grid-cols-3">
            <div class="space-y-1.5">
              <Label for="model-context-window">{{ $t("Context window") }}</Label>
              <Input
                id="model-context-window"
                :model-value="modelContextWindowValue"
                type="number"
                inputmode="numeric"
                pattern="[0-9]*"
                min="1"
                step="1"
                :placeholder="$t('Use service default')"
                :disabled="loading"
                @keydown="preventNonNumericTokenInput"
                @paste="preventNonNumericTokenPaste"
                @update:model-value="updateTokenInput('context_window', $event)"
                @change="updateModelTokenLimits"
              />
            </div>
            <div class="space-y-1.5">
              <Label for="model-max-input-tokens">{{ $t("Maximum input tokens") }}</Label>
              <Input
                id="model-max-input-tokens"
                :model-value="modelMaxInputTokensValue"
                type="number"
                inputmode="numeric"
                pattern="[0-9]*"
                min="1"
                step="1"
                :placeholder="$t('Use service default')"
                :disabled="loading"
                @keydown="preventNonNumericTokenInput"
                @paste="preventNonNumericTokenPaste"
                @update:model-value="updateTokenInput('max_input_tokens', $event)"
                @change="updateModelTokenLimits"
              />
            </div>
            <div class="space-y-1.5">
              <Label for="model-max-output-tokens">{{ $t("Maximum output tokens") }}</Label>
              <Input
                id="model-max-output-tokens"
                :model-value="modelMaxOutputTokensValue"
                type="number"
                inputmode="numeric"
                pattern="[0-9]*"
                min="1"
                step="1"
                :placeholder="$t('Use service default')"
                :disabled="loading"
                @keydown="preventNonNumericTokenInput"
                @paste="preventNonNumericTokenPaste"
                @update:model-value="updateTokenInput('max_output_tokens', $event)"
                @change="updateModelTokenLimits"
              />
            </div>
          </section>
        </div>
        <div class="mt-auto flex min-h-10 shrink-0 items-center justify-between gap-3 px-1">
          <div class="min-w-0">
            <p class="truncate text-sm text-destructive">{{ error }}</p>
          </div>
        </div>
      </section>
      <section
        v-else
        class="flex min-h-0 flex-col items-center justify-center bg-background px-6 text-center"
      >
        <PlugZapIcon class="size-10 text-muted-foreground/40" />
        <p class="mt-3 text-sm font-medium">{{ $t("Select a model provider") }}</p>
        <p class="mt-1 max-w-sm text-xs leading-5 text-muted-foreground">
          {{ $t("Select a provider on the left to configure its API address, key, and model capabilities.") }}
        </p>
      </section>
    </div>
  </SettingsPage>

  <Dialog v-model:open="modelImportOpen">
    <DialogContent class="flex max-h-[78vh] max-w-2xl flex-col">
      <DialogHeader>
        <DialogTitle>{{ $t("Discover models") }}</DialogTitle>
        <DialogDescription class="truncate font-mono text-xs">
          {{ discoveryEndpointLabel }}
        </DialogDescription>
      </DialogHeader>
      <div class="flex min-h-0 flex-1 flex-col gap-3">
        <div class="flex items-center gap-3">
          <div class="relative min-w-0 flex-1">
            <SearchIcon class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              v-model="modelImportQuery"
              class="h-9 pl-8 text-sm"
              :placeholder="$t('Search provider models')"
              :aria-label="$t('Search provider models')"
              :disabled="modelImportLoading || !!modelImportError"
            />
          </div>
          <label class="flex shrink-0 items-center gap-2 text-sm">
            <Checkbox
              :model-value="allImportModelsSelected"
              :disabled="modelImportLoading || !!modelImportError || filteredImportModels.length === 0"
              @update:model-value="setAllModelsImported"
            />
            {{ $t("Select all") }}
          </label>
        </div>
        <div class="model-settings-scrollbar min-h-64 flex-1 overflow-y-auto border-y border-border">
          <div v-if="modelImportLoading" class="grid min-h-32 place-items-center text-sm text-muted-foreground">
            {{ $t("Fetching model list") }}
          </div>
          <div
            v-else-if="modelImportError"
            class="flex min-h-40 flex-col items-center justify-center gap-3 px-6 text-center"
          >
            <AlertCircleIcon class="size-6 text-destructive" />
            <p class="max-w-lg break-words text-sm text-destructive">{{ modelImportError }}</p>
            <Button size="sm" variant="outline" @click="openModelImporter">
              <RefreshCwIcon class="size-4" />
              {{ $t("Retry") }}
            </Button>
          </div>
          <template v-else>
            <label
              v-for="model in filteredImportModels"
              :key="model"
              class="flex h-10 cursor-pointer items-center gap-3 px-2 hover:bg-muted/50"
            >
              <Checkbox
                :model-value="pendingImportedModels.has(model)"
                @update:model-value="setPendingModelImported(model, $event)"
              />
              <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ model }}</span>
            </label>
          </template>
          <div
            v-if="!modelImportLoading && !modelImportError && filteredImportModels.length === 0"
            class="grid min-h-32 place-items-center text-sm text-muted-foreground"
          >
            {{ $t("No additional models found") }}
          </div>
        </div>
      </div>
      <DialogFooter>
        <Button variant="outline" @click="modelImportOpen = false">{{ $t("Cancel") }}</Button>
        <Button
          :disabled="modelImportLoading || !!modelImportError || pendingImportedModels.size === 0"
          @click="confirmModelImport"
        >
          {{ $t("Add {count} models", { count: pendingImportedModels.size }) }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

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

<style scoped>
.model-settings-scrollbar {
  scrollbar-color: transparent transparent;
  scrollbar-width: thin;
}

.model-settings-scrollbar:hover {
  scrollbar-color: color-mix(in oklch, var(--muted-foreground) 28%, transparent) transparent;
}

.model-settings-scrollbar::-webkit-scrollbar {
  width: 6px;
  height: 6px;
}

.model-settings-scrollbar::-webkit-scrollbar-track {
  background: transparent;
}

.model-settings-scrollbar::-webkit-scrollbar-thumb {
  min-height: 32px;
  border: 1px solid transparent;
  border-radius: 999px;
  background: transparent;
  background-clip: padding-box;
}

.model-settings-scrollbar:hover::-webkit-scrollbar-thumb {
  background-color: color-mix(in oklch, var(--muted-foreground) 28%, transparent);
}

.model-settings-scrollbar::-webkit-scrollbar-thumb:hover {
  background-color: color-mix(in oklch, var(--muted-foreground) 48%, transparent);
}
</style>
