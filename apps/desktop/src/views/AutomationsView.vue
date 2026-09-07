<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
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
const { t } = useI18n();

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

const filterOptions = computed<Array<{ value: Filter; label: string }>>(() => [
  { value: "all", label: t("All") },
  { value: "enabled", label: t("Enabled") },
  { value: "paused", label: t("Paused") },
  { value: "completed", label: t("Completed") },
]);

const weekdays = computed(() => [
  { value: "1", label: t("Monday") },
  { value: "2", label: t("Tuesday") },
  { value: "3", label: t("Wednesday") },
  { value: "4", label: t("Thursday") },
  { value: "5", label: t("Friday") },
  { value: "6", label: t("Saturday") },
  { value: "0", label: t("Sunday") },
]);

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
  if (parsed.scheduleKind === "daily") return t("Daily at {time}", { time: parsed.time });
  if (parsed.scheduleKind === "weekdays") {
    return t("Weekdays at {time}", { time: parsed.time });
  }
  if (parsed.scheduleKind === "weekly") {
    const label = weekdays.value.find((item) => item.value === parsed.weekday)?.label ?? t("Weekly");
    return t("{day} at {time}", { day: label, time: parsed.time });
  }
  return task.cron;
}

function nextRunLabel(task: AutomationTask): string {
  if (!task.enabled) return t("Paused");
  if (!task.next_run_at) return t("Waiting to be scheduled");
  const next = new Date(task.next_run_at);
  const delta = next.getTime() - Date.now();
  if (delta <= 0) return t("Running soon");
  const minutes = Math.max(1, Math.round(delta / 60000));
  if (minutes < 60) return t("Next run in {count} minutes", { count: minutes });
  const hours = Math.round(minutes / 60);
  if (hours < 24) return t("Next run in {count} hours", { count: hours });
  return t("Next run in {count} days", { count: Math.round(hours / 24) });
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
    error.value = t("Enter a task name, instructions, and execution time");
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
        <h1 class="text-3xl font-medium">{{ $t("Automations") }}</h1>
        <p class="mt-2 text-sm text-muted-foreground">{{ $t("Schedule Foya to run tasks, generate reports, or check for updates") }}</p>
      </div>
      <Button @click="openCreate">
        <PlusIcon class="size-4" />
        {{ $t("Create") }}
        <ChevronDownIcon class="size-3.5" />
      </Button>
    </header>

    <div class="relative mt-7">
      <SearchIcon class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input v-model="query" class="h-10 rounded-full pl-9" :placeholder="$t('Search automations')" />
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
          :title="$t('Run now')"
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
          {{ tasks.length === 0 ? $t("No scheduled tasks") : $t("No matching tasks") }}
        </p>
      </div>
    </div>
    <p v-if="error && !editorOpen" class="mt-3 text-sm text-destructive">{{ error }}</p>

    <Dialog v-model:open="editorOpen">
      <DialogScrollContent class="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{{ draft.id ? $t("Edit task") : $t("Create task") }}</DialogTitle>
        </DialogHeader>

        <div class="space-y-5 py-2">
          <div class="space-y-1.5">
            <Label for="automation-name">{{ $t("Name") }}</Label>
            <Input id="automation-name" v-model="draft.name" :placeholder="$t('Daily briefing')" />
          </div>
          <div class="space-y-1.5">
            <Label for="automation-prompt">{{ $t("Instructions") }}</Label>
            <Textarea
              id="automation-prompt"
              v-model="draft.prompt"
              class="min-h-28 resize-y"
              :placeholder="$t('Summarize the project updates that need attention today')"
            />
          </div>

          <div class="grid gap-4 sm:grid-cols-3">
            <div class="space-y-1.5">
              <Label for="automation-frequency">{{ $t("Frequency") }}</Label>
              <Select :model-value="draft.scheduleKind" @update:model-value="selectScheduleKind">
                <SelectTrigger id="automation-frequency" class="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="daily">{{ $t("Daily") }}</SelectItem>
                  <SelectItem value="weekdays">{{ $t("Weekdays") }}</SelectItem>
                  <SelectItem value="weekly">{{ $t("Weekly") }}</SelectItem>
                  <SelectItem value="custom">Cron</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div v-if="draft.scheduleKind === 'weekly'" class="space-y-1.5">
              <Label for="automation-weekday">{{ $t("Weekday") }}</Label>
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
              <Label for="automation-time">{{ $t("Time") }}</Label>
              <Input id="automation-time" v-model="draft.time" type="time" />
            </div>
            <div v-else class="space-y-1.5 sm:col-span-2">
              <Label for="automation-cron">{{ $t("Cron expression") }}</Label>
              <Input id="automation-cron" v-model="draft.cron" class="font-mono" placeholder="0 9 * * 1-5" />
            </div>
          </div>

          <div class="grid gap-4 sm:grid-cols-2">
            <div class="space-y-1.5">
              <Label for="automation-connection">{{ $t("Language model connection") }}</Label>
              <Select
                :model-value="draft.connectionID || '__default__'"
                @update:model-value="selectConnection"
              >
                <SelectTrigger id="automation-connection" class="w-full">
                  <SelectValue :placeholder="$t('Default language model')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">{{ $t("Default language model") }}</SelectItem>
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
              <Label for="automation-model">{{ $t("Model") }}</Label>
              <Input id="automation-model" v-model="draft.model" class="font-mono" :placeholder="$t('Default model')" />
            </div>
            <div class="space-y-1.5">
              <Label for="automation-project">{{ $t("Working project") }}</Label>
              <Select
                :model-value="draft.projectID || '__none__'"
                @update:model-value="selectProject"
              >
                <SelectTrigger id="automation-project" class="w-full">
                  <SelectValue :placeholder="$t('No project')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">{{ $t("No project") }}</SelectItem>
                  <SelectItem v-for="project in projects" :key="project.id" :value="project.id">
                    {{ project.name }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1.5">
              <Label>{{ $t("Tool approval") }}</Label>
              <ButtonGroup class="w-full">
                <Button
                  class="flex-1 shadow-none"
                  size="sm"
                  variant="outline"
                  :class="draft.approvalMode === 'auto' && 'bg-accent'"
                  @click="draft.approvalMode = 'auto'"
                >
                  {{ $t("Automatic") }}
                </Button>
                <Button
                  class="flex-1 shadow-none"
                  size="sm"
                  variant="outline"
                  :class="draft.approvalMode === 'full_access' && 'bg-accent'"
                  @click="draft.approvalMode = 'full_access'"
                >
                  {{ $t("Full access") }}
                </Button>
              </ButtonGroup>
            </div>
          </div>

          <label class="flex items-center justify-between py-1">
            <span class="text-sm font-medium">{{ $t("Enable task") }}</span>
            <Checkbox
              :model-value="draft.enabled"
              :aria-label="$t('Enable task')"
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
            {{ $t("Delete") }}
          </Button>
          <span v-else />
          <div class="flex gap-2">
            <Button variant="outline" @click="editorOpen = false">{{ $t("Cancel") }}</Button>
            <Button :disabled="saving" @click="save">
              {{ saving ? $t("Saving") : $t("Save") }}
            </Button>
          </div>
        </DialogFooter>
      </DialogScrollContent>
    </Dialog>

    <Dialog v-model:open="deleteConfirmOpen">
      <DialogScrollContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ $t("Delete {name}?", { name: draft.name }) }}</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">{{ $t("The task will stop being scheduled. Existing run chats will remain.") }}</p>
        <DialogFooter>
          <Button variant="outline" :disabled="deleting" @click="deleteConfirmOpen = false">
            {{ $t("Cancel") }}
          </Button>
          <Button variant="destructive" :disabled="deleting" @click="remove">
            {{ deleting ? $t("Deleting") : $t("Delete") }}
          </Button>
        </DialogFooter>
      </DialogScrollContent>
    </Dialog>
  </div>
</template>
