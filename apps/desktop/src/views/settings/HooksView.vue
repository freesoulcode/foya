<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RefreshCwIcon } from "@lucide/vue";
import { api, type HookConfig, type ProjectInfo } from "@/lib/api";
import { useCurrentProjectId } from "@/composables/useCurrentProjectId";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";

const currentProjectId = useCurrentProjectId();

type Scope = "global" | "project";

const scope = ref<Scope>("global");
const projects = ref<ProjectInfo[]>([]);
const projectId = ref("");
const source = ref("[]");
const loading = ref(false);
const saving = ref(false);
const error = ref("");

const selectedProject = computed(
  () => projects.value.find((item) => item.id === projectId.value) ?? null
);
const projectPath = computed(() =>
  selectedProject.value
    ? `${selectedProject.value.path}/.foya/hooks.json`
    : "选择项目后显示"
);

function formatHooks(items: HookConfig[]): string {
  return JSON.stringify(items, null, 2);
}

async function loadProjects() {
  projects.value = await api.listProjects();
  const preferred = currentProjectId.value || projectId.value;
  if (projects.value.some((item) => item.id === preferred)) {
    projectId.value = preferred;
  } else {
    projectId.value = projects.value[0]?.id ?? "";
  }
}

async function loadHooks() {
  loading.value = true;
  error.value = "";
  try {
    if (scope.value === "project" && !projectId.value) {
      source.value = "[]";
      return;
    }
    source.value = formatHooks(
      await api.getHooks(
        scope.value,
        scope.value === "project" ? projectId.value : undefined
      )
    );
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function initialize() {
  loading.value = true;
  error.value = "";
  try {
    await loadProjects();
    await loadHooks();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function selectScope(next: Scope) {
  scope.value = next;
  await loadHooks();
}

async function selectProject(value: unknown) {
  if (typeof value !== "string") return;
  projectId.value = value;
  await loadHooks();
}

async function save() {
  let hooks: HookConfig[];
  try {
    const parsed = JSON.parse(source.value) as unknown;
    if (!Array.isArray(parsed)) {
      throw new Error("Hooks 配置顶层必须是数组");
    }
    hooks = parsed as HookConfig[];
  } catch (cause) {
    error.value = String(cause);
    return;
  }

  if (scope.value === "project" && !projectId.value) {
    error.value = "请选择项目";
    return;
  }

  saving.value = true;
  error.value = "";
  try {
    const saved = await api.updateHooks(
      scope.value,
      hooks,
      scope.value === "project" ? projectId.value : undefined
    );
    source.value = formatHooks(saved);
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
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
    title="Hooks"
    description="在 Session、请求、工具和回合生命周期中执行本地命令。"
  >
    <template #actions>
      <Button
        size="icon-sm"
        variant="ghost"
        class="no-drag"
        :disabled="loading || saving"
        title="刷新 Hooks"
        aria-label="刷新 Hooks"
        @click="loadHooks"
      >
        <RefreshCwIcon class="size-4" :class="loading && 'animate-spin'" />
      </Button>
    </template>

    <div class="mb-5 flex flex-wrap items-center gap-3">
      <ButtonGroup aria-label="Hooks 范围">
        <Button
          size="sm"
          :variant="scope === 'global' ? 'default' : 'outline'"
          :disabled="loading || saving"
          @click="selectScope('global')"
        >
          全局
        </Button>
        <Button
          size="sm"
          :variant="scope === 'project' ? 'default' : 'outline'"
          :disabled="loading || saving || projects.length === 0"
          @click="selectScope('project')"
        >
          项目
        </Button>
      </ButtonGroup>

      <Select
        v-if="scope === 'project'"
        :model-value="projectId"
        :disabled="loading || saving || projects.length === 0"
        @update:model-value="selectProject"
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

    <div class="flex min-h-0 flex-1 flex-col space-y-2">
      <Label for="hooks-config">
        {{ scope === "global" ? "~/.foya/hooks.json" : projectPath }}
      </Label>
      <Textarea
        id="hooks-config"
        v-model="source"
        class="min-h-0 flex-1 resize-none font-mono text-xs leading-5"
        :disabled="loading || saving || (scope === 'project' && !projectId)"
        spellcheck="false"
      />
      <p class="text-xs text-muted-foreground">
        支持 SessionStart、UserPromptSubmit、PreToolUse、PostToolUse、Stop、Notification。
      </p>
    </div>

    <p v-if="error" class="mt-4 whitespace-pre-wrap text-sm text-destructive">
      {{ error }}
    </p>

    <div class="mt-5 flex justify-end">
      <Button
        :disabled="loading || saving || (scope === 'project' && !projectId)"
        @click="save"
      >
        {{ saving ? "保存中…" : "保存" }}
      </Button>
    </div>
  </SettingsPage>
</template>
