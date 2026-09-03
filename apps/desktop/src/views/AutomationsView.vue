<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import {
  CalendarClockIcon,
  ChevronDownIcon,
  CircleIcon,
  PlayIcon,
  PlusIcon,
  SearchIcon,
  Trash2Icon,
} from "@lucide/vue";
import {
  api,
  type AutomationInput,
  type AutomationTask,
  type ConnectionConfig,
  type ProjectInfo,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogFooter,
  DialogHeader,
  DialogScrollContent,
  DialogTitle,
} from "@/components/ui/dialog";
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

type Filter = "all" | "enabled" | "paused" | "completed";
type ScheduleKind = "daily" | "weekdays" | "weekly" | "custom";

interface AutomationDraft {
  id: string;
  name: string;
  prompt: string;
  scheduleKind: ScheduleKind;
  time: string;
  weekday: string;
  cron: string;
  enabled: boolean;
  connectionID: string;
  model: string;
  projectID: string;
  approvalMode: "auto" | "full_access";
}

const tasks = ref<AutomationTask[]>([]);
const connections = ref<ConnectionConfig[]>([]);
const projects = ref<ProjectInfo[]>([]);
const query = ref("");
const filter = ref<Filter>("all");
const editorOpen = ref(false);
const deleteConfirmOpen = ref(false);
const saving = ref(false);
const deleting = ref(false);
const loading = ref(false);
const error = ref("");
const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

const emptyDraft = (): AutomationDraft => ({
  id: "",
  name: "",
  prompt: "",
  scheduleKind: "weekdays",
  time: "09:00",
  weekday: "1",
  cron: "",
  enabled: true,
  connectionID: "",
  model: "",
  projectID: "",
  approvalMode: "auto",
});

const draft = ref<AutomationDraft>(emptyDraft());

const languageConnections = computed(() =>
  connections.value.filter(
    (item): item is ConnectionConfig & { id: string } =>
      item.type === "language" && typeof item.id === "string" && item.id.length > 0
  )
);

const filteredTasks = computed(() => {
  const normalized = query.value.trim().toLowerCase();
  return tasks.value.filter((task) => {
    if (filter.value === "enabled" && !task.enabled) return false;
    if (filter.value === "paused" && task.enabled) return false;
    if (filter.value === "completed" && task.last_status !== "completed") return false;
    return !normalized || `${task.name} ${task.prompt}`.toLowerCase().includes(normalized);
  });
});

const filterOptions: Array<{ value: Filter; label: string }> = [
  { value: "all", label: "全部" },
  { value: "enabled", label: "已开启" },
  { value: "paused", label: "已暂停" },
  { value: "completed", label: "已完成" },
];

const weekdays = [
  { value: "1", label: "星期一" },
  { value: "2", label: "星期二" },
  { value: "3", label: "星期三" },
  { value: "4", label: "星期四" },
  { value: "5", label: "星期五" },
  { value: "6", label: "星期六" },
  { value: "0", label: "星期日" },
];

function cronFromDraft(value: AutomationDraft): string {
  if (value.scheduleKind === "custom") return value.cron.trim();
  const [hour, minute] = value.time.split(":").map(Number);
  if (value.scheduleKind === "weekdays") return `${minute} ${hour} * * 1-5`;
  if (value.scheduleKind === "weekly") return `${minute} ${hour} * * ${value.weekday}`;
  return `${minute} ${hour} * * *`;
}

function parseCron(task: AutomationTask): Pick<AutomationDraft, "scheduleKind" | "time" | "weekday" | "cron"> {
  const parts = task.cron.trim().split(/\s+/);
  if (parts.length === 5) {
    const [minute, hour, day, month, weekday] = parts;
    const time = `${String(Number(hour)).padStart(2, "0")}:${String(Number(minute)).padStart(2, "0")}`;
    if (day === "*" && month === "*" && weekday === "*") {
      return { scheduleKind: "daily", time, weekday: "1", cron: task.cron };
    }
    if (day === "*" && month === "*" && weekday === "1-5") {
      return { scheduleKind: "weekdays", time, weekday: "1", cron: task.cron };
    }
    if (day === "*" && month === "*" && /^[0-6]$/.test(weekday)) {
      return { scheduleKind: "weekly", time, weekday, cron: task.cron };
    }
  }
  return { scheduleKind: "custom", time: "09:00", weekday: "1", cron: task.cron };
}

function scheduleLabel(task: AutomationTask): string {
  const parsed = parseCron(task);
  if (parsed.scheduleKind === "daily") return `每天 ${parsed.time}`;
  if (parsed.scheduleKind === "weekdays") return `工作日 ${parsed.time}`;
  if (parsed.scheduleKind === "weekly") {
    const label = weekdays.find((item) => item.value === parsed.weekday)?.label ?? "每周";
    return `${label} ${parsed.time}`;
  }
  return task.cron;
}

function nextRunLabel(task: AutomationTask): string {
  if (!task.enabled) return "已暂停";
  if (!task.next_run_at) return "等待调度";
  const next = new Date(task.next_run_at);
  const delta = next.getTime() - Date.now();
  if (delta <= 0) return "即将运行";
  const minutes = Math.max(1, Math.round(delta / 60000));
  if (minutes < 60) return `下次运行 ${minutes}分钟后`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `下次运行 ${hours}小时后`;
  return `下次运行 ${Math.round(hours / 24)}天后`;
}

function statusClass(task: AutomationTask): string {
  if (task.last_status === "running") return "text-primary fill-primary";
  if (task.last_status === "failed") return "text-destructive fill-destructive";
  if (!task.enabled) return "text-muted-foreground/50";
  return "text-emerald-500 fill-emerald-500";
}

async function load() {
  loading.value = true;
  error.value = "";
  try {
    [tasks.value, connections.value, projects.value] = await Promise.all([
      api.listAutomations(),
      api.listConnections(),
      api.listProjects(),
    ]);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  draft.value = emptyDraft();
  editorOpen.value = true;
}

function openEdit(task: AutomationTask) {
  const parsed = parseCron(task);
  draft.value = {
    id: task.id,
    name: task.name,
    prompt: task.prompt,
    scheduleKind: parsed.scheduleKind,
    time: parsed.time,
    weekday: parsed.weekday,
    cron: parsed.cron,
    enabled: task.enabled,
    connectionID: task.connection_id ?? "",
    model: task.model ?? "",
    projectID: task.project_id ?? "",
    approvalMode: task.approval_mode,
  };
  editorOpen.value = true;
}

function buildInput(): AutomationInput {
  return {
    name: draft.value.name.trim(),
    prompt: draft.value.prompt.trim(),
    cron: cronFromDraft(draft.value),
    timezone,
    enabled: draft.value.enabled,
    connection_id: draft.value.connectionID || undefined,
    model: draft.value.model.trim() || undefined,
    project_id: draft.value.projectID || undefined,
    approval_mode: draft.value.approvalMode,
  };
}

async function save() {
  const input = buildInput();
  error.value = "";
  if (!input.name || !input.prompt || !input.cron) {
    error.value = "请填写任务名称、指令和执行时间";
    return;
  }
  saving.value = true;
  try {
    const saved = draft.value.id
      ? await api.updateAutomation(draft.value.id, input)
      : await api.createAutomation(input);
    const index = tasks.value.findIndex((item) => item.id === saved.id);
    if (index >= 0) tasks.value[index] = saved;
    else tasks.value.unshift(saved);
    editorOpen.value = false;
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function runNow(task: AutomationTask) {
  error.value = "";
  try {
    const updated = await api.runAutomation(task.id);
    const index = tasks.value.findIndex((item) => item.id === task.id);
    if (index >= 0) tasks.value[index] = updated;
  } catch (cause) {
    error.value = String(cause);
  }
}

async function remove() {
  if (!draft.value.id) return;
  deleting.value = true;
  try {
    await api.deleteAutomation(draft.value.id);
    tasks.value = tasks.value.filter((item) => item.id !== draft.value.id);
    deleteConfirmOpen.value = false;
    editorOpen.value = false;
  } catch (cause) {
    error.value = String(cause);
  } finally {
    deleting.value = false;
  }
}

function selectScheduleKind(value: unknown) {
  if (value === "daily" || value === "weekdays" || value === "weekly" || value === "custom") {
    draft.value.scheduleKind = value;
  }
}

function selectConnection(value: unknown) {
  draft.value.connectionID = value === "__default__" ? "" : String(value ?? "");
  draft.value.model = "";
}

function selectProject(value: unknown) {
  draft.value.projectID = value === "__none__" ? "" : String(value ?? "");
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="mx-auto flex h-full min-h-0 w-full max-w-5xl flex-col px-6 pb-8 pt-12">
    <header class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-3xl font-medium">自动化</h1>
        <p class="mt-2 text-sm text-muted-foreground">让 Foya 按计划执行任务、生成报告或检查更新</p>
      </div>
      <Button @click="openCreate">
        <PlusIcon class="size-4" />
        创建
        <ChevronDownIcon class="size-3.5" />
      </Button>
    </header>

    <div class="relative mt-7">
      <SearchIcon class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input v-model="query" class="h-10 rounded-full pl-9" placeholder="搜索自动化任务" />
    </div>

    <div class="mt-5 flex items-center gap-1">
      <button
        v-for="option in filterOptions"
        :key="option.value"
        type="button"
        :class="[
          'rounded-md px-3 py-1.5 text-sm transition-colors',
          filter === option.value
            ? 'bg-muted font-medium text-foreground'
            : 'text-muted-foreground hover:text-foreground',
        ]"
        @click="filter = option.value"
      >
        {{ option.label }}
      </button>
    </div>

    <div class="mt-4 min-h-0 flex-1 overflow-y-auto">
      <button
        v-for="task in filteredTasks"
        :key="task.id"
        type="button"
        class="group flex w-full items-center gap-3 px-2 py-4 text-left hover:bg-muted/40"
        @click="openEdit(task)"
      >
        <CircleIcon :class="['size-4 shrink-0', statusClass(task)]" />
        <span class="min-w-0 flex-1">
          <span class="block truncate text-sm font-medium">{{ task.name }}</span>
          <span class="mt-0.5 block truncate text-xs text-muted-foreground">
            {{ scheduleLabel(task) }} · {{ nextRunLabel(task) }}
          </span>
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          class="opacity-0 group-hover:opacity-100 group-focus-within:opacity-100"
          :disabled="task.last_status === 'running'"
          title="立即运行"
          @click.stop="runNow(task)"
        >
          <PlayIcon class="size-4" />
        </Button>
      </button>

      <div
        v-if="!loading && filteredTasks.length === 0"
        class="flex h-44 flex-col items-center justify-center gap-2 text-center"
      >
        <CalendarClockIcon class="size-6 text-muted-foreground/60" />
        <p class="text-sm text-muted-foreground">
          {{ tasks.length === 0 ? "暂无已安排任务" : "没有匹配的任务" }}
        </p>
      </div>
    </div>
    <p v-if="error && !editorOpen" class="mt-3 text-sm text-destructive">{{ error }}</p>

    <Dialog v-model:open="editorOpen">
      <DialogScrollContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ draft.id ? "编辑任务" : "创建任务" }}</DialogTitle>
        </DialogHeader>

        <div class="space-y-5 py-2">
          <div class="space-y-1.5">
            <Label for="automation-name">名称</Label>
            <Input id="automation-name" v-model="draft.name" placeholder="每日简报" />
          </div>
          <div class="space-y-1.5">
            <Label for="automation-prompt">指令</Label>
            <Textarea
              id="automation-prompt"
              v-model="draft.prompt"
              class="min-h-28 resize-y"
              placeholder="整理今天需要关注的项目进展并给出摘要"
            />
          </div>

          <div class="grid gap-4 sm:grid-cols-3">
            <div class="space-y-1.5">
              <Label for="automation-frequency">频率</Label>
              <Select :model-value="draft.scheduleKind" @update:model-value="selectScheduleKind">
                <SelectTrigger id="automation-frequency" class="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="daily">每天</SelectItem>
                  <SelectItem value="weekdays">工作日</SelectItem>
                  <SelectItem value="weekly">每周</SelectItem>
                  <SelectItem value="custom">Cron</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div v-if="draft.scheduleKind === 'weekly'" class="space-y-1.5">
              <Label for="automation-weekday">星期</Label>
              <Select v-model="draft.weekday">
                <SelectTrigger id="automation-weekday" class="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem
                    v-for="weekday in weekdays"
                    :key="weekday.value"
                    :value="weekday.value"
                  >
                    {{ weekday.label }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div v-if="draft.scheduleKind !== 'custom'" class="space-y-1.5">
              <Label for="automation-time">时间</Label>
              <Input id="automation-time" v-model="draft.time" type="time" />
            </div>
            <div v-else class="space-y-1.5 sm:col-span-2">
              <Label for="automation-cron">Cron 表达式</Label>
              <Input id="automation-cron" v-model="draft.cron" class="font-mono" placeholder="0 9 * * 1-5" />
            </div>
          </div>

          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-1.5">
              <Label for="automation-connection">语言模型连接</Label>
              <Select
                :model-value="draft.connectionID || '__default__'"
                @update:model-value="selectConnection"
              >
                <SelectTrigger id="automation-connection" class="w-full">
                  <SelectValue placeholder="默认语言模型" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">默认语言模型</SelectItem>
                  <SelectItem
                    v-for="connection in languageConnections"
                    :key="connection.id"
                    :value="connection.id"
                  >
                    {{ connection.name }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1.5">
              <Label for="automation-model">模型</Label>
              <Input id="automation-model" v-model="draft.model" class="font-mono" placeholder="默认模型" />
            </div>
            <div class="space-y-1.5">
              <Label for="automation-project">工作项目</Label>
              <Select
                :model-value="draft.projectID || '__none__'"
                @update:model-value="selectProject"
              >
                <SelectTrigger id="automation-project" class="w-full">
                  <SelectValue placeholder="不绑定项目" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">不绑定项目</SelectItem>
                  <SelectItem v-for="project in projects" :key="project.id" :value="project.id">
                    {{ project.name }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1.5">
              <Label>工具审批</Label>
              <ButtonGroup class="w-full">
                <Button
                  class="flex-1 shadow-none"
                  size="sm"
                  variant="outline"
                  :class="draft.approvalMode === 'auto' && 'bg-accent'"
                  @click="draft.approvalMode = 'auto'"
                >
                  自动判断
                </Button>
                <Button
                  class="flex-1 shadow-none"
                  size="sm"
                  variant="outline"
                  :class="draft.approvalMode === 'full_access' && 'bg-accent'"
                  @click="draft.approvalMode = 'full_access'"
                >
                  完全访问
                </Button>
              </ButtonGroup>
            </div>
          </div>

          <label class="flex items-center justify-between py-1">
            <span class="text-sm font-medium">启用任务</span>
            <Checkbox
              :model-value="draft.enabled"
              aria-label="启用任务"
              @update:model-value="draft.enabled = $event === true"
            />
          </label>
          <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
        </div>

        <DialogFooter class="items-center sm:justify-between">
          <Button
            v-if="draft.id"
            variant="ghost"
            class="text-destructive hover:text-destructive"
            @click="deleteConfirmOpen = true"
          >
            <Trash2Icon class="size-4" />
            删除
          </Button>
          <span v-else />
          <div class="flex gap-2">
            <Button variant="outline" @click="editorOpen = false">取消</Button>
            <Button :disabled="saving" @click="save">
              {{ saving ? "保存中..." : "保存" }}
            </Button>
          </div>
        </DialogFooter>
      </DialogScrollContent>
    </Dialog>

    <Dialog v-model:open="deleteConfirmOpen">
      <DialogScrollContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>删除“{{ draft.name }}”？</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">任务将停止调度，已有运行会话会保留。</p>
        <DialogFooter>
          <Button variant="outline" :disabled="deleting" @click="deleteConfirmOpen = false">
            取消
          </Button>
          <Button variant="destructive" :disabled="deleting" @click="remove">
            {{ deleting ? "删除中..." : "删除" }}
          </Button>
        </DialogFooter>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
