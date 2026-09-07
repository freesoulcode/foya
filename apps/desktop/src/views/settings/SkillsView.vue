<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { PinIcon, PlusIcon } from "@lucide/vue";
import { api, type ProjectInfo, type SkillInfo } from "@/lib/api";
import { useCurrentProjectId } from "@/composables/useCurrentProjectId";
import ProjectCreateDialog from "@/components/projects/ProjectCreateDialog.vue";
import SettingsPage from "@/layouts/settings/SettingsPage.vue";
import { Button } from "@/components/ui/button";
import { ButtonGroup } from "@/components/ui/button-group";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";

const currentProjectId = useCurrentProjectId();
const { t } = useI18n();

const skills = ref<SkillInfo[]>([]);
const projects = ref<ProjectInfo[]>([]);
const projectCreateOpen = ref(false);
const projectCreateError = ref("");
const skillScope = ref<"global" | "project">("global");
const selectedSkillProjectID = ref("");
const loading = ref(false);
const saving = ref(false);
const error = ref("");

const globalSkills = computed(() =>
  skills.value.filter((item) => item.scope !== "project")
);
const projectSkills = computed(() =>
  skills.value.filter((item) => item.scope === "project")
);
const selectedSkillProject = computed(
  () => projects.value.find((item) => item.id === selectedSkillProjectID.value) ?? null
);
const activeProjects = computed(() => projects.value);
const skillGroups = computed(() => {
  const global = {
    id: "global",
    title: skillScope.value === "project" ? t("Inherited global skills") : t("Global skills"),
    description: t("{first} and {second}", {
      first: "~/.agents/skills",
      second: "~/.foya/skills",
    }),
    items: globalSkills.value,
  };
  if (skillScope.value === "global") return [global];
  return [{
    id: "project",
    title: selectedSkillProject.value
      ? t("Project skills · {name}", { name: selectedSkillProject.value.name })
      : t("Project skills"),
    description: selectedSkillProject.value
      ? t("{first} and {second}", {
          first: `${selectedSkillProject.value.path}/.agents/skills`,
          second: `${selectedSkillProject.value.path}/.foya/skills`,
        })
      : t("Select a project to show this"),
    items: projectSkills.value,
  }, global];
});

function openProjectCreate() {
  projectCreateError.value = "";
  projectCreateOpen.value = true;
}

async function createProject(input: { name: string; path: string }) {
  saving.value = true;
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
    saving.value = false;
  }
}

async function loadSkills() {
  loading.value = true;
  error.value = "";
  try {
    projects.value = await api.listProjects();
    const preferred = currentProjectId.value || selectedSkillProjectID.value;
    if (!activeProjects.value.some((item) => item.id === preferred)) {
      selectedSkillProjectID.value = activeProjects.value[0]?.id ?? "";
    } else {
      selectedSkillProjectID.value = preferred;
    }
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
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
  loading.value = true;
  error.value = "";
  try {
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function selectSkillProject(value: unknown) {
  if (typeof value !== "string") return;
  selectedSkillProjectID.value = value;
  await selectSkillScope("project");
}

async function toggleSkill(item: SkillInfo) {
  saving.value = true;
  try {
    await api.setSkillEnabled(item.ref, !item.enabled);
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

async function toggleSkillPinned(item: SkillInfo) {
  saving.value = true;
  try {
    await api.setSkillPinned(item.ref, !item.pinned);
    await loadSelectedSkills();
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

void loadSkills();
</script>

<template>
  <SettingsPage
    :title="$t('Skills')"
    :description="$t('Manage available tool skills and pinned context items.')"
  >
    <div class="mb-6 flex flex-wrap items-center gap-3">
      <ButtonGroup :aria-label="$t('Skill scope')">
        <Button
          size="sm"
          :variant="skillScope === 'global' ? 'default' : 'outline'"
          @click="selectSkillScope('global')"
        >
          {{ $t("Global") }}
        </Button>
        <Button
          size="sm"
          :variant="skillScope === 'project' ? 'default' : 'outline'"
          :disabled="activeProjects.length === 0"
          @click="selectSkillScope('project')"
        >
          {{ $t("Project") }}
        </Button>
      </ButtonGroup>
      <Select
        v-if="skillScope === 'project'"
        :model-value="selectedSkillProjectID"
        :disabled="loading || activeProjects.length === 0"
        @update:model-value="selectSkillProject"
      >
        <SelectTrigger size="sm" class="min-w-48 max-w-full">
          <SelectValue :placeholder="$t('Select project')" />
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
        :title="$t('Add project')"
        :aria-label="$t('Add project')"
        :disabled="saving"
        @click="openProjectCreate"
      >
        <PlusIcon class="size-4" />
      </Button>
    </div>
    <div class="min-h-0 flex-1 space-y-7 overflow-y-auto">
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
                  {{ $t("Built-in") }}
                </span>
                <span v-else-if="item.scope === 'plugin'" class="text-xs text-muted-foreground">
                  {{ $t("Plugin") }}
                </span>
              </div>
              <p class="truncate text-xs text-muted-foreground" :title="item.path">
                {{ item.description || item.path }}
              </p>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <Tooltip>
                <TooltipTrigger as-child>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    class="h-8 w-8"
                    :disabled="saving"
                    :aria-label="item.pinned ? $t('Unpin {name}', { name: item.name }) : $t('Pin {name}', { name: item.name })"
                    @click="toggleSkillPinned(item)"
                  >
                    <PinIcon
                      class="h-4 w-4"
                      :class="item.pinned ? 'fill-current text-foreground' : 'text-muted-foreground'"
                    />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  {{ item.pinned ? $t("Unpin from context") : $t("Pin to context") }}
                </TooltipContent>
              </Tooltip>
              <Checkbox
                :checked="item.enabled"
                :disabled="saving"
                :aria-label="item.enabled ? $t('Disable {name}', { name: item.name }) : $t('Enable {name}', { name: item.name })"
                @update:checked="toggleSkill(item)"
              />
            </div>
          </div>
          <p
            v-if="!loading && group.items.length === 0"
            class="py-4 text-sm text-muted-foreground"
          >
            {{ group.id === "project" && !selectedSkillProject ? $t("No project is selected.") : $t("No skills found.") }}
          </p>
        </div>
      </section>
    </div>
    <p v-if="error" class="mt-4 text-sm text-destructive">{{ error }}</p>

    <ProjectCreateDialog
      v-model:open="projectCreateOpen"
      :busy="saving"
      :error="projectCreateError"
      @confirm="createProject"
    />
  </SettingsPage>
</template>
