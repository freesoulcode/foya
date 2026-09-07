<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  ArrowLeftIcon,
  FileTextIcon,
  PlusIcon,
  RefreshCwIcon,
  SaveIcon,
  Trash2Icon,
} from "@lucide/vue";
import { api, type CommandInfo, type ProjectInfo } from "@/lib/api";
import { useCurrentProjectId } from "@/composables/useCurrentProjectId";
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
import SettingsPage from "@/layouts/settings/SettingsPage.vue";

const currentProjectId = useCurrentProjectId();
const { locale, t } = useI18n();

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
      : t("Select a project to show this")
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
  const preferred = currentProjectId.value || projectId.value;
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
    error.value = t("Enter a command name");
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const item = await api.createCommand({
      scope: scope.value,
      ...(scope.value === "project" ? { project_id: projectId.value } : {}),
      name: createName.value.trim(),
      body: t("Define what the AI should do when this command is triggered"),
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
    error.value = t("Command name and instructions are required");
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

function formatDate(value?: string): string {
  return value ? new Date(value).toLocaleString(locale.value) : "—";
}

function commandDescription(item: CommandInfo): string {
  return item.description
    ? item.builtin
      ? t(item.description)
      : item.description
    : t("No description");
}

watch(
  currentProjectId,
  () => {
    if (scope.value === "project") void initialize();
  }
);

void initialize();
</script>

<template>
  <SettingsPage
    :title="$t('Commands')"
    :description="$t('Save reusable prompts as commands that can be triggered with /.')"
  >
    <template #actions>
        <Button
          size="icon-sm"
          variant="ghost"
          :disabled="loading || saving"
          :title="$t('Refresh commands')"
          :aria-label="$t('Refresh commands')"
          @click="loadCommands"
        >
          <RefreshCwIcon class="size-4" :class="loading && 'animate-spin'" />
        </Button>
        <Button size="sm" :disabled="saving || !canUseScope" @click="openCreate">
          <PlusIcon class="size-4" />
          {{ $t("Create command") }}
        </Button>
    </template>

    <div class="mb-5 flex flex-wrap items-center gap-3">
      <ButtonGroup :aria-label="$t('Command scope')">
        <Button size="sm" :variant="scope === 'global' ? 'default' : 'outline'" @click="setScope('global')">
          {{ $t("Global") }}
        </Button>
        <Button
          size="sm"
          :variant="scope === 'project' ? 'default' : 'outline'"
          :disabled="projects.length === 0"
          @click="setScope('project')"
        >
          {{ $t("Project") }}
        </Button>
      </ButtonGroup>
      <Select
        v-if="scope === 'project'"
        :model-value="projectId"
        :disabled="loading || projects.length === 0"
        @update:model-value="setProject"
      >
        <SelectTrigger size="sm" class="min-w-52 max-w-full">
          <SelectValue :placeholder="$t('Select project')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="project in projects" :key="project.id" :value="project.id">
            {{ project.name }} · {{ project.path }}
          </SelectItem>
        </SelectContent>
      </Select>
    </div>

    <section v-if="!editorOpen" class="flex min-h-0 flex-1 flex-col border-y border-border bg-background">
      <div class="grid h-11 shrink-0 grid-cols-[minmax(180px,0.8fr)_minmax(220px,1.4fr)_132px] items-center gap-3 border-b border-border px-4 text-xs font-medium text-muted-foreground">
        <span>{{ $t("Command") }}</span>
        <span>{{ $t("Description") }}</span>
        <span class="text-right">{{ $t("Updated") }}</span>
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
          <span class="truncate text-sm text-muted-foreground">{{ commandDescription(item) }}</span>
          <span class="truncate text-right text-xs text-muted-foreground">
            {{ formatDate(item.updated_at) }}
          </span>
        </button>
        <div
          v-if="!loading && items.length === 0"
          class="flex min-h-72 flex-col items-center justify-center gap-3 px-4 text-center"
        >
          <FileTextIcon class="size-8 text-muted-foreground/50" />
          <div>
            <p class="text-sm font-medium">{{ $t("No commands") }}</p>
            <p class="mt-1 text-xs text-muted-foreground">{{ $t("Create a command, then trigger it with / in the composer") }}</p>
          </div>
        </div>
      </div>
      <div class="flex min-h-11 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
        <p class="truncate text-sm text-destructive">{{ error }}</p>
        <p v-if="!error" class="text-xs text-muted-foreground">{{ $t("{count} commands", { count: items.length }) }}</p>
      </div>
    </section>

    <section v-else class="flex min-h-0 flex-1 flex-col border-y border-border bg-background">
      <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
        <div class="flex min-w-0 items-center gap-3">
          <Button size="icon-sm" variant="ghost" :title="$t('Back to command list')" :aria-label="$t('Back to command list')" @click="back">
            <ArrowLeftIcon class="size-4" />
          </Button>
          <div class="min-w-0">
            <h3 class="truncate text-sm font-medium">{{ $t("Edit command") }}</h3>
            <p class="truncate text-xs text-muted-foreground">{{ scopePath }}</p>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <Button size="icon-sm" variant="ghost" class="text-muted-foreground hover:text-destructive" :disabled="saving" :title="$t('Delete command')" :aria-label="$t('Delete command')" @click="remove">
            <Trash2Icon class="size-4" />
          </Button>
          <Button :disabled="saving || loading || !name.trim() || !body.trim()" @click="save">
            <SaveIcon class="size-4" />
            {{ saving ? $t("Saving") : $t("Save") }}
          </Button>
        </div>
      </div>
      <div class="min-h-0 flex-1 overflow-y-auto">
        <div class="grid gap-4 border-b border-border p-4 lg:grid-cols-[minmax(200px,0.8fr)_minmax(260px,1.2fr)]">
          <div class="grid gap-2">
            <Label for="command-name">{{ $t("Name") }}</Label>
            <Input id="command-name" v-model="name" placeholder="summarize-pr-info" :disabled="loading" />
          </div>
          <div class="grid gap-2">
            <Label for="command-description">{{ $t("Description") }}</Label>
            <Input id="command-description" v-model="description" :placeholder="$t('Summarize PR details')" :disabled="loading" />
          </div>
        </div>
        <Textarea
          v-model="body"
          class="min-h-[420px] resize-y rounded-none border-0 bg-transparent p-5 font-mono text-sm leading-6 shadow-none focus-visible:ring-0"
          :placeholder="$t('Define what the AI should do when this command is triggered')"
          :disabled="loading"
        />
      </div>
      <div class="flex min-h-12 shrink-0 items-center justify-between gap-3 border-t border-border px-4">
        <p class="truncate text-sm text-destructive">{{ error }}</p>
        <p v-if="!error" class="text-xs text-muted-foreground">{{ $t("{count} characters", { count: body.length }) }}</p>
      </div>
    </section>

    <Dialog v-model:open="createOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ $t("Create command") }}</DialogTitle>
          <DialogDescription>
            {{ $t("The command will be created at") }} <code>{{ scopePath }}/{{ createName || "command-name" }}.md</code>
          </DialogDescription>
        </DialogHeader>
        <div class="grid gap-2 py-2">
          <Label for="create-command-name">{{ $t("Command name") }}</Label>
          <Input
            id="create-command-name"
            v-model="createName"
            placeholder="summarize-pr-info"
            @keydown.enter.prevent="create"
          />
          <p class="text-xs text-muted-foreground">{{ $t("Use : to create groups, for example git:commit.") }}</p>
        </div>
        <DialogFooter>
          <Button variant="outline" :disabled="saving" @click="createOpen = false">{{ $t("Cancel") }}</Button>
          <Button :disabled="saving || !createName.trim()" @click="create">
            {{ saving ? $t("Creating") : $t("Confirm") }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </SettingsPage>
</template>
