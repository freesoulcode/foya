<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  ArrowLeftIcon,
  FileTextIcon,
  PlusIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  api,
  type ContextItem,
  type ContextItemKind,
  type ContextItemScope,
  type ProjectInfo,
  type RuleTrigger,
} from "@/lib/api";
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

const props = defineProps<{
  kind: ContextItemKind;
  currentProjectId?: string;
  revision?: number;
}>();

const scope = ref<ContextItemScope>(props.currentProjectId ? "project" : "global");
const projectId = ref(props.currentProjectId ?? "");
const projects = ref<ProjectInfo[]>([]);
const items = ref<ContextItem[]>([]);
const selectedId = ref("");
const draft = ref("");
const ruleName = ref("");
const ruleDescription = ref("");
const ruleTrigger = ref<RuleTrigger>("always");
const ruleGlobs = ref("");
const rulePath = ref("");
const ruleEditorOpen = ref(false);
const loading = ref(false);
const saving = ref(false);
const settingsSaving = ref(false);
const memoryEnabled = ref(true);
const memorySettingsSupported = ref(true);
const error = ref("");

const title = computed(() => (props.kind === "rule" ? "规则" : "记忆"));
const selected = computed(
  () => items.value.find((item) => item.id === selectedId.value) ?? null
);
const canCreate = computed(
  () => scope.value === "global" || Boolean(projectId.value)
);
const triggerLabels: Record<RuleTrigger, string> = {
  always: "始终生效",
  glob: "路径匹配",
  model_decision: "模型判断",
  manual: "手动调用",
};
const triggerClasses: Record<RuleTrigger, string> = {
  always: "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300",
  glob: "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-500/30 dark:bg-blue-500/10 dark:text-blue-300",
  model_decision: "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300",
  manual: "border-border bg-muted text-muted-foreground",
};

function unsupportedContextMessage() {
  return `当前内核未加载${title.value}接口，重启桌面端后可用`;
}

function isNotFound(cause: unknown) {
  return String(cause).includes("404");
}

function selectItem(item: ContextItem) {
  selectedId.value = item.id;
  draft.value = item.content;
  ruleName.value = item.name ?? "";
  ruleDescription.value = item.description ?? "";
  ruleTrigger.value = item.trigger ?? "always";
  ruleGlobs.value = (item.globs ?? []).join("\n");
  rulePath.value = item.path ?? "";
  error.value = "";
}

function openRuleEditor(item: ContextItem) {
  selectItem(item);
  ruleEditorOpen.value = true;
}

function startNew() {
  selectedId.value = "";
  draft.value = "";
  ruleName.value = "";
  ruleDescription.value = "";
  ruleTrigger.value = "always";
  ruleGlobs.value = "";
  rulePath.value = "";
  error.value = "";
}

function startNewRule() {
  startNew();
  ruleEditorOpen.value = true;
}

function backToRuleList() {
  ruleEditorOpen.value = false;
  startNew();
}

function ruleLocation(item: ContextItem) {
  if (item.path) return item.path;
  return item.scope === "global" ? "user_rules.md" : ".foya/rules/";
}

function ruleGlobSummary(item: ContextItem) {
  const globs = item.globs ?? [];
  if (globs.length === 0) return "未设置";
  if (globs.length === 1) return globs[0];
  return `${globs[0]} 等 ${globs.length} 条`;
}

async function loadItems(preserveSelection = true) {
  if (!canCreate.value) {
    items.value = [];
    startNew();
    return;
  }
  loading.value = true;
  error.value = "";
  try {
    const next = await api.listContextItems(
      props.kind,
      scope.value,
      scope.value === "project" ? projectId.value : undefined
    );
    items.value = next;
    if (props.kind === "rule" && !ruleEditorOpen.value) {
      startNew();
      return;
    }
    const current = preserveSelection
      ? next.find((item) => item.id === selectedId.value)
      : undefined;
    if (current) {
      selectItem(current);
    } else if (next[0]) {
      selectItem(next[0]);
    } else {
      startNew();
    }
  } catch (cause) {
    error.value = isNotFound(cause) ? unsupportedContextMessage() : String(cause);
    items.value = [];
    startNew();
  } finally {
    loading.value = false;
  }
}

function isMemorySettingsUnsupported(cause: unknown) {
  return isNotFound(cause);
}

async function loadMemorySettings() {
  try {
    memoryEnabled.value = (await api.getMemorySettings()).enabled;
    memorySettingsSupported.value = true;
  } catch (cause) {
    if (isMemorySettingsUnsupported(cause)) {
      memoryEnabled.value = true;
      memorySettingsSupported.value = false;
      return;
    }
    throw cause;
  }
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    projects.value = await api.listProjects();
    if (props.kind === "memory") {
      await loadMemorySettings();
    }
    if (scope.value === "project") {
      const preferred = props.currentProjectId || projectId.value;
      projectId.value =
        projects.value.find((item) => item.id === preferred)?.id ??
        projects.value[0]?.id ??
        "";
    }
    await loadItems(false);
  } catch (cause) {
    error.value = String(cause);
    loading.value = false;
  }
}

async function toggleMemory() {
  if (!memorySettingsSupported.value) {
    error.value = "当前内核未加载记忆开关接口，重启桌面端后可用";
    return;
  }
  settingsSaving.value = true;
  error.value = "";
  try {
    const settings = await api.updateMemorySettings({
      enabled: !memoryEnabled.value,
    });
    memoryEnabled.value = settings.enabled;
  } catch (cause) {
    error.value = String(cause);
  } finally {
    settingsSaving.value = false;
  }
}

async function setScope(next: ContextItemScope) {
  scope.value = next;
  if (next === "project" && !projectId.value) {
    projectId.value =
      projects.value.find((item) => item.id === props.currentProjectId)?.id ??
      projects.value[0]?.id ??
      "";
  }
  ruleEditorOpen.value = false;
  startNew();
  await loadItems(false);
}

async function setProject(value: unknown) {
  if (typeof value !== "string") return;
  projectId.value = value;
  ruleEditorOpen.value = false;
  startNew();
  await loadItems(false);
}

function setRuleTrigger(value: unknown) {
  if (
    value === "always" ||
    value === "glob" ||
    value === "model_decision" ||
    value === "manual"
  ) {
    ruleTrigger.value = value;
  }
}

function parsedGlobs(): string[] {
  return ruleGlobs.value
    .split(/[\n,]/)
    .map((value) => value.trim())
    .filter(Boolean);
}

async function save() {
  const content = draft.value.trim();
  if (!content) {
    error.value = `${title.value}内容不能为空`;
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const ruleFields =
      props.kind === "rule"
        ? {
            name: ruleName.value.trim(),
            description: ruleDescription.value.trim(),
            trigger: ruleTrigger.value,
            globs: parsedGlobs(),
            ...(scope.value === "project"
              ? { path: rulePath.value.trim() }
              : {}),
          }
        : {};
    const item = selected.value
      ? await api.updateContextItem(props.kind, selected.value.id, {
          content,
          ...ruleFields,
        })
      : await api.createContextItem(props.kind, {
          scope: scope.value,
          ...(scope.value === "project" ? { project_id: projectId.value } : {}),
          content,
          ...ruleFields,
        });
    await loadItems(false);
    selectItem(items.value.find((candidate) => candidate.id === item.id) ?? item);
    if (props.kind === "rule") {
      ruleEditorOpen.value = true;
    }
  } catch (cause) {
    error.value = isNotFound(cause) ? unsupportedContextMessage() : String(cause);
  } finally {
    saving.value = false;
  }
}

async function remove() {
  if (!selected.value) return;
  saving.value = true;
  error.value = "";
  try {
    await api.deleteContextItem(props.kind, selected.value.id);
    if (props.kind === "rule") {
      ruleEditorOpen.value = false;
    }
    await loadItems(false);
  } catch (cause) {
    error.value = isNotFound(cause) ? unsupportedContextMessage() : String(cause);
  } finally {
    saving.value = false;
  }
}

watch(
  () => props.revision,
  () => {
    void loadItems();
    if (props.kind === "memory") {
      void api
        .getMemorySettings()
        .then((settings) => {
          memoryEnabled.value = settings.enabled;
          memorySettingsSupported.value = true;
        })
        .catch((cause) => {
          if (isMemorySettingsUnsupported(cause)) {
            memoryEnabled.value = true;
            memorySettingsSupported.value = false;
            return;
          }
          error.value = String(cause);
        });
    }
  }
);

onMounted(() => void load());
</script>

<template>
  <div class="mx-auto flex min-h-full w-full max-w-6xl flex-col px-5 pb-6 sm:px-8">
    <div
      data-tauri-drag-region
      class="mb-6 flex min-h-11 flex-wrap items-center justify-between gap-3 pt-4"
    >
      <div>
        <h2 class="text-base font-medium">{{ title }}</h2>
        <p class="mt-1 text-xs text-muted-foreground">
          {{
            kind === "rule"
              ? "为 Agent 设定可复用的行为约束。"
              : "记录跨会话可复用的偏好、事实和经验。"
          }}
        </p>
      </div>
      <div class="no-drag flex min-w-0 flex-wrap items-center justify-end gap-2">
        <Button
          v-if="kind === 'rule'"
          size="sm"
          :disabled="!canCreate || saving"
          @click="startNewRule"
        >
          <PlusIcon class="size-4" />
          新建规则
        </Button>
        <div
          v-if="kind === 'memory'"
          class="inline-flex h-9 shrink-0 items-center gap-3 whitespace-nowrap rounded-md border border-border bg-background px-3 text-sm"
        >
          <span class="leading-none text-muted-foreground">启用记忆</span>
          <button
            type="button"
            role="switch"
            :aria-checked="memoryEnabled"
            :disabled="settingsSaving || loading || !memorySettingsSupported"
            :title="memorySettingsSupported ? '切换记忆功能' : '重启桌面端后可用'"
            class="relative inline-flex h-5 w-10 shrink-0 items-center rounded-full border transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
            :class="memoryEnabled ? 'border-primary bg-primary' : 'border-input bg-muted'"
            @click="toggleMemory"
          >
            <span
              class="pointer-events-none size-4 rounded-full bg-background shadow-sm transition-transform"
              :class="memoryEnabled ? 'translate-x-5' : 'translate-x-0.5'"
            />
          </button>
        </div>
        <Button
          size="icon-sm"
          variant="ghost"
          :disabled="loading || saving || settingsSaving"
          :title="`刷新${title}`"
          :aria-label="`刷新${title}`"
          @click="loadItems()"
        >
          <RefreshCwIcon class="size-4" :class="loading && 'animate-spin'" />
        </Button>
      </div>
    </div>

    <div class="mb-5 flex flex-wrap items-center gap-3">
      <ButtonGroup :aria-label="`${title}范围`">
        <Button
          size="sm"
          :variant="scope === 'global' ? 'default' : 'outline'"
          @click="setScope('global')"
        >
          全局
        </Button>
        <Button
          size="sm"
          :variant="scope === 'project' ? 'default' : 'outline'"
          :disabled="projects.length === 0"
          @click="setScope('project')"
        >
          项目
        </Button>
      </ButtonGroup>
      <Select
        v-if="scope === 'project'"
        :model-value="projectId"
        :disabled="loading || projects.length === 0"
        @update:model-value="setProject"
      >
        <SelectTrigger size="sm" class="min-w-52 max-w-full">
          <SelectValue placeholder="选择项目" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem
            v-for="project in projects"
            :key="project.id"
            :value="project.id"
          >
            {{ project.name }} · {{ project.path }}
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

    <section v-if="kind === 'rule'" class="flex min-h-[520px] flex-1 flex-col">
      <div v-if="!ruleEditorOpen" class="flex min-h-0 flex-1 flex-col border-y border-border bg-background">
        <div class="grid h-11 shrink-0 grid-cols-[minmax(0,1.5fr)_minmax(120px,0.7fr)_minmax(112px,0.5fr)_minmax(120px,0.7fr)_132px] items-center gap-3 border-b border-border px-4 text-xs font-medium text-muted-foreground">
          <span>规则文件</span>
          <span>位置</span>
          <span>生效方式</span>
          <span>匹配路径</span>
          <span class="text-right">更新时间</span>
        </div>
        <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
          <button
            v-for="item in items"
            :key="item.id"
            type="button"
            class="grid w-full grid-cols-[minmax(0,1.5fr)_minmax(120px,0.7fr)_minmax(112px,0.5fr)_minmax(120px,0.7fr)_132px] items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50"
            @click="openRuleEditor(item)"
          >
            <span class="flex min-w-0 items-center gap-2">
              <FileTextIcon class="size-4 shrink-0 text-muted-foreground" />
              <span class="min-w-0">
                <span class="block truncate text-sm font-medium">
                  {{ item.name || "未命名规则" }}
                </span>
                <span v-if="item.description" class="mt-0.5 block truncate text-xs text-muted-foreground">
                  {{ item.description }}
                </span>
              </span>
            </span>
            <span class="truncate font-mono text-xs text-muted-foreground">
              {{ ruleLocation(item) }}
            </span>
            <span
              class="w-fit rounded-full border px-2 py-0.5 text-xs"
              :class="triggerClasses[item.trigger ?? 'always']"
            >
              {{ triggerLabels[item.trigger ?? "always"] }}
            </span>
            <span class="truncate font-mono text-xs text-muted-foreground">
              {{ ruleGlobSummary(item) }}
            </span>
            <span class="truncate text-right text-xs text-muted-foreground">
              {{ new Date(item.updated_at).toLocaleString() }}
            </span>
          </button>
          <div
            v-if="!loading && items.length === 0"
            class="flex min-h-72 flex-col items-center justify-center gap-3 px-4 text-center"
          >
            <FileTextIcon class="size-8 text-muted-foreground/50" />
            <div>
              <p class="text-sm font-medium">暂无规则</p>
              <p class="mt-1 text-xs text-muted-foreground">
                新建规则后会在这里按文件形式管理
              </p>
            </div>
          </div>
        </div>
        <div class="flex min-h-11 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
          <p class="truncate text-sm text-destructive">{{ error }}</p>
          <p v-if="!error" class="text-xs text-muted-foreground">
            {{ items.length }} 条规则
          </p>
        </div>
      </div>

      <div v-else class="flex min-h-0 flex-1 flex-col border-y border-border bg-background">
        <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
          <div class="flex min-w-0 items-center gap-3">
            <Button
              size="icon-sm"
              variant="ghost"
              title="返回规则列表"
              aria-label="返回规则列表"
              @click="backToRuleList"
            >
              <ArrowLeftIcon class="size-4" />
            </Button>
            <div class="min-w-0">
              <h3 class="truncate text-sm font-medium">
                {{ selected ? "编辑规则" : "新建规则" }}
              </h3>
              <p class="truncate text-xs text-muted-foreground">
                {{ scope === "global" ? "全局规则" : rulePath || ".foya/rules/" }}
              </p>
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Button
              v-if="selected"
              size="icon-sm"
              variant="ghost"
              class="text-muted-foreground hover:text-destructive"
              :disabled="saving"
              title="删除规则"
              aria-label="删除规则"
              @click="remove"
            >
              <Trash2Icon class="size-4" />
            </Button>
            <Button
              :disabled="saving || loading || !canCreate || !draft.trim()"
              @click="save"
            >
              <SaveIcon class="size-4" />
              {{ saving ? "保存中…" : "保存" }}
            </Button>
          </div>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto">
          <div class="grid gap-4 border-b border-border p-4">
            <div class="grid gap-4 lg:grid-cols-[minmax(220px,1fr)_minmax(260px,1.2fr)]">
              <div class="grid gap-2">
                <Label for="rule-name">规则名称</Label>
                <Input
                  id="rule-name"
                  v-model="ruleName"
                  placeholder="例如：go-testing"
                  :disabled="!canCreate || loading"
                />
              </div>
              <div class="grid gap-2">
                <Label for="rule-description">描述</Label>
                <Input
                  id="rule-description"
                  v-model="ruleDescription"
                  placeholder="供模型判断是否需要加载"
                  :disabled="!canCreate || loading"
                />
              </div>
            </div>
            <div class="grid gap-4 lg:grid-cols-2">
              <div class="grid gap-2">
                <Label for="rule-trigger">生效方式</Label>
                <Select
                  :model-value="ruleTrigger"
                  :disabled="!canCreate || loading"
                  @update:model-value="setRuleTrigger"
                >
                  <SelectTrigger id="rule-trigger" class="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="always">始终生效</SelectItem>
                    <SelectItem value="glob">路径匹配</SelectItem>
                    <SelectItem value="model_decision">模型判断</SelectItem>
                    <SelectItem value="manual">手动调用</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div v-if="ruleTrigger === 'glob'" class="grid gap-2">
                <Label for="rule-globs">匹配路径</Label>
                <Input
                  id="rule-globs"
                  v-model="ruleGlobs"
                  placeholder="module-a/**, internal/**/*.go"
                  :disabled="!canCreate || loading"
                />
              </div>
            </div>
          </div>
          <Textarea
            v-model="draft"
            class="min-h-[420px] resize-y rounded-none border-0 bg-transparent p-5 font-mono text-sm leading-6 shadow-none focus-visible:border-transparent focus-visible:ring-0"
            maxlength="6000"
            placeholder="输入需要遵循的规则"
            :disabled="!canCreate || loading"
          />
        </div>
        <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
          <div class="min-w-0">
            <p class="truncate text-sm text-destructive">{{ error }}</p>
            <p v-if="!error" class="text-xs text-muted-foreground">
              {{ draft.length }} / 6000
            </p>
          </div>
        </div>
      </div>
    </section>

    <section
      v-else
      class="flex min-h-[520px] flex-1 flex-col overflow-hidden border-y border-border bg-background"
    >
      <div class="flex h-11 shrink-0 items-center justify-between border-b border-border px-2">
        <div class="flex min-w-0 items-center gap-2">
          <span class="size-2 rounded-full" :class="memoryEnabled ? 'bg-emerald-500' : 'bg-muted-foreground/40'" />
          <span class="truncate font-mono text-xs text-muted-foreground">
            {{ scope === "global" ? "user_profile.md" : "project_memory.md" }}
          </span>
        </div>
        <span v-if="selected" class="shrink-0 text-xs text-muted-foreground">
          更新于 {{ new Date(selected.updated_at).toLocaleString() }}
        </span>
      </div>
      <div class="flex min-h-0 flex-1 flex-col">
        <Textarea
          v-model="draft"
          class="min-h-80 flex-1 resize-none rounded-none border-0 bg-transparent px-3 py-5 font-mono text-sm leading-6 shadow-none focus-visible:border-transparent focus-visible:ring-0 sm:px-6"
          :maxlength="2000"
          placeholder="记录对后续协作有价值的偏好、事实和经验"
          :disabled="!canCreate || loading || !memoryEnabled"
        />
        <div class="flex min-h-14 shrink-0 items-center justify-between gap-3 border-t border-border px-2 sm:px-3">
          <div class="min-w-0">
            <p class="truncate text-sm text-destructive">{{ error }}</p>
            <p v-if="!error" class="text-xs text-muted-foreground">
              {{ draft.length }} / 2000
            </p>
          </div>
          <Button
            class="shrink-0"
            :disabled="saving || loading || !canCreate || !draft.trim() || !memoryEnabled"
            @click="save"
          >
            <SaveIcon class="size-4" />
            {{ saving ? "保存中…" : "保存" }}
          </Button>
        </div>
      </div>
    </section>
  </div>
</template>
