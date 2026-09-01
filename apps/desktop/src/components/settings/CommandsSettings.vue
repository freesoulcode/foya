<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  ArrowLeftIcon,
  FileTextIcon,
  PlusIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
} from "@lucide/vue";
import { api, type CommandInfo, type ProjectInfo } from "@/lib/api";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const props = defineProps<{ currentProjectId?: string }>();

type Scope = "global" | "project";
const scope = ref<Scope>("global");
const projects = ref<ProjectInfo[]>([]);
const projectId = ref("");
const items = ref<CommandInfo[]>([]);
const selected = ref<CommandInfo | null>(null);
const editorOpen = ref(false);
const name = ref("");
const description = ref("");
const body = ref("");
const createOpen = ref(false);
const createName = ref("");
const loading = ref(false);
const saving = ref(false);
const error = ref("");

const selectedProject = computed(
  () => projects.value.find((item) => item.id === projectId.value) ?? null
);
const scopePath = computed(() =>
  scope.value === "global"
    ? "~/.foya/commands"
    : selectedProject.value
      ? `${selectedProject.value.path}/.foya/commands`
      : "选择项目后显示"
);
const canUseScope = computed(
  () => scope.value === "global" || Boolean(projectId.value)
);

function resetEditor(item?: CommandInfo) {
  selected.value = item ?? null;
  name.value = item?.name ?? "";
  description.value = item?.description ?? "";
  body.value = item?.body ?? "";
  error.value = "";
}

async function loadProjects() {
  projects.value = await api.listProjects();
  const preferred = props.currentProjectId || projectId.value;
  projectId.value = projects.value.some((item) => item.id === preferred)
    ? preferred
    : projects.value[0]?.id ?? "";
}

async function loadCommands() {
  loading.value = true;
  error.value = "";
  try {
    if (!canUseScope.value) {
      items.value = [];
      resetEditor();
      return;
    }
    items.value = await api.listCommands(
      scope.value,
      scope.value === "project" ? projectId.value : undefined
    );
    if (!editorOpen.value) resetEditor();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function initialize() {
  loading.value = true;
  try {
    await loadProjects();
    await loadCommands();
  } finally {
    loading.value = false;
  }
}

async function setScope(next: Scope) {
  scope.value = next;
  editorOpen.value = false;
  resetEditor();
  await loadCommands();
}

async function setProject(value: unknown) {
  if (typeof value !== "string") return;
  projectId.value = value;
  editorOpen.value = false;
  resetEditor();
  await loadCommands();
}

function openCreate() {
  createName.value = "";
  error.value = "";
  createOpen.value = true;
}

async function create() {
  if (!createName.value.trim()) {
    error.value = "请输入命令名称";
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const item = await api.createCommand({
      scope: scope.value,
      ...(scope.value === "project" ? { project_id: projectId.value } : {}),
      name: createName.value.trim(),
    });
    createOpen.value = false;
    items.value = await api.listCommands(
      scope.value,
      scope.value === "project" ? projectId.value : undefined
    );
    const next = items.value.find((candidate) => candidate.ref === item.ref) ?? item;
    resetEditor(next);
    editorOpen.value = true;
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

function edit(item: CommandInfo) {
  resetEditor(item);
  editorOpen.value = true;
}

function back() {
  editorOpen.value = false;
  resetEditor();
}

async function save() {
  const item = selected.value;
  if (!item || !name.value.trim() || !body.value.trim()) {
    error.value = "命令名称和指令不能为空";
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const updated = await api.updateCommand(item.ref, {
      scope: scope.value,
      ...(scope.value === "project" ? { project_id: projectId.value } : {}),
      name: name.value.trim(),
      description: description.value.trim(),
      body: body.value.trim(),
    });
    items.value = await api.listCommands(
      scope.value,
      scope.value === "project" ? projectId.value : undefined
    );
    resetEditor(items.value.find((candidate) => candidate.ref === updated.ref) ?? updated);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function remove() {
  const item = selected.value;
  if (!item) return;
  saving.value = true;
  error.value = "";
  try {
    await api.deleteCommand(
      item.ref,
      scope.value,
      scope.value === "project" ? projectId.value : undefined
    );
    editorOpen.value = false;
    await loadCommands();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

watch(
  () => props.currentProjectId,
  () => {
    if (scope.value === "project") void initialize();
  }
);

void initialize();
</script>

<template>
  <div class="mx-auto flex min-h-full w-full max-w-6xl flex-col px-5 pb-6 sm:px-8">
    <div class="mb-6 flex min-h-11 flex-wrap items-center justify-between gap-3 pt-4">
      <div>
        <h2 class="text-base font-medium">命令</h2>
        <p class="mt-1 text-xs text-muted-foreground">
          将常用 Prompt 保存为可通过 <code>/</code> 快速触发的命令。
        </p>
      </div>
      <div class="flex items-center gap-2">
        <Button
          size="icon-sm"
          variant="ghost"
          :disabled="loading || saving"
          title="刷新命令"
          aria-label="刷新命令"
          @click="loadCommands"
        >
          <RefreshCwIcon class="size-4" :class="loading && 'animate-spin'" />
        </Button>
        <Button size="sm" :disabled="saving || !canUseScope" @click="openCreate">
          <PlusIcon class="size-4" />
          创建命令
        </Button>
      </div>
    </div>

    <div class="mb-5 flex flex-wrap items-center gap-3">
      <ButtonGroup aria-label="命令范围">
        <Button size="sm" :variant="scope === 'global' ? 'default' : 'outline'" @click="setScope('global')">
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
          <SelectItem v-for="project in projects" :key="project.id" :value="project.id">
            {{ project.name }} · {{ project.path }}
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

    <section v-if="!editorOpen" class="flex min-h-[520px] flex-1 flex-col border-y border-border bg-background">
      <div class="grid h-11 shrink-0 grid-cols-[minmax(180px,0.8fr)_minmax(220px,1.4fr)_132px] items-center gap-3 border-b border-border px-4 text-xs font-medium text-muted-foreground">
        <span>命令</span>
        <span>描述</span>
        <span class="text-right">更新时间</span>
      </div>
      <div class="min-h-0 flex-1 divide-y divide-border overflow-y-auto">
        <button
          v-for="item in items"
          :key="item.ref"
          type="button"
          class="grid w-full grid-cols-[minmax(180px,0.8fr)_minmax(220px,1.4fr)_132px] items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50"
          @click="edit(item)"
        >
          <span class="flex min-w-0 items-center gap-2">
            <FileTextIcon class="size-4 shrink-0 text-muted-foreground" />
            <span class="min-w-0">
              <span class="block truncate font-mono text-sm font-medium">/{{ item.name }}</span>
              <span class="block truncate text-xs text-muted-foreground">{{ item.path || scopePath }}</span>
            </span>
          </span>
          <span class="truncate text-sm text-muted-foreground">{{ item.description || "未填写描述" }}</span>
          <span class="truncate text-right text-xs text-muted-foreground">
            {{ item.updated_at ? new Date(item.updated_at).toLocaleString() : "—" }}
          </span>
        </button>
        <div
          v-if="!loading && items.length === 0"
          class="flex min-h-72 flex-col items-center justify-center gap-3 px-4 text-center"
        >
          <FileTextIcon class="size-8 text-muted-foreground/50" />
          <div>
            <p class="text-sm font-medium">暂无命令</p>
            <p class="mt-1 text-xs text-muted-foreground">创建命令后可在对话输入框通过 <code>/</code> 触发</p>
          </div>
        </div>
      </div>
      <div class="flex min-h-11 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
        <p class="truncate text-sm text-destructive">{{ error }}</p>
        <p v-if="!error" class="text-xs text-muted-foreground">{{ items.length }} 个命令</p>
      </div>
    </section>

    <section v-else class="flex min-h-[520px] flex-1 flex-col border-y border-border bg-background">
      <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
        <div class="flex min-w-0 items-center gap-3">
          <Button size="icon-sm" variant="ghost" title="返回命令列表" aria-label="返回命令列表" @click="back">
            <ArrowLeftIcon class="size-4" />
          </Button>
          <div class="min-w-0">
            <h3 class="truncate text-sm font-medium">编辑命令</h3>
            <p class="truncate text-xs text-muted-foreground">{{ scopePath }}</p>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <Button size="icon-sm" variant="ghost" class="text-muted-foreground hover:text-destructive" :disabled="saving" title="删除命令" aria-label="删除命令" @click="remove">
            <Trash2Icon class="size-4" />
          </Button>
          <Button :disabled="saving || loading || !name.trim() || !body.trim()" @click="save">
            <SaveIcon class="size-4" />
            {{ saving ? "保存中…" : "保存" }}
          </Button>
        </div>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto">
        <div class="grid gap-4 border-b border-border p-4 lg:grid-cols-[minmax(200px,0.8fr)_minmax(260px,1.2fr)]">
          <div class="grid gap-2">
            <Label for="command-name">名称</Label>
            <Input id="command-name" v-model="name" placeholder="summarize-pr-info" :disabled="loading" />
          </div>
          <div class="grid gap-2">
            <Label for="command-description">描述</Label>
            <Input id="command-description" v-model="description" placeholder="总结 PR 信息" :disabled="loading" />
          </div>
        </div>
        <Textarea
          v-model="body"
          class="min-h-[420px] resize-y rounded-none border-0 bg-transparent p-5 font-mono text-sm leading-6 shadow-none focus-visible:ring-0"
          placeholder="定义触发命令时 AI 应执行的具体操作"
          :disabled="loading"
        />
      </div>
      <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
        <p class="truncate text-sm text-destructive">{{ error }}</p>
        <p v-if="!error" class="text-xs text-muted-foreground">{{ body.length }} 个字符</p>
      </div>
    </section>

    <Dialog v-model:open="createOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>创建命令</DialogTitle>
          <DialogDescription>
            将创建 <code>{{ scopePath }}/{{ createName || "command-name" }}.md</code>
          </DialogDescription>
        </DialogHeader>
        <div class="grid gap-2 py-2">
          <Label for="create-command-name">命令名称</Label>
          <Input
            id="create-command-name"
            v-model="createName"
            placeholder="summarize-pr-info"
            @keydown.enter.prevent="create"
          />
          <p class="text-xs text-muted-foreground">可使用 <code>:</code> 创建分组，例如 <code>git:commit</code>。</p>
        </div>
        <DialogFooter>
          <Button variant="outline" :disabled="saving" @click="createOpen = false">取消</Button>
          <Button :disabled="saving || !createName.trim()" @click="create">
            {{ saving ? "创建中…" : "确认" }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
