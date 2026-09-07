import { computed, ref } from "vue";
import { api, type CanvasDocument } from "@/lib/api";
import { translate } from "@/i18n";

const projects = ref<CanvasDocument[]>([]);
const activeId = ref("");
const loading = ref(true);
const creating = ref(false);
const error = ref("");
let loaded = false;

const activeProject = computed(
  () => projects.value.find((project) => project.id === activeId.value) ?? null
);

async function loadProjects() {
  loading.value = true;
  try {
    projects.value = await api.listCanvases();
    if (activeId.value && !projects.value.some((item) => item.id === activeId.value)) {
      activeId.value = "";
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  } finally {
    loading.value = false;
  }
}

async function createProject() {
  if (creating.value) return;
  creating.value = true;
  try {
    const project = await api.createCanvas(
      translate("Creative project {number}", {
        number: projects.value.length + 1,
      })
    );
    projects.value = [project, ...projects.value];
    activeId.value = project.id;
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  } finally {
    creating.value = false;
  }
}

async function deleteProject(project: CanvasDocument) {
  try {
    await api.deleteCanvas(project.id);
    projects.value = projects.value.filter((item) => item.id !== project.id);
    if (activeId.value === project.id) activeId.value = "";
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  }
}

async function renameProject(project: CanvasDocument, title: string) {
  const nextTitle = title.trim();
  if (!nextTitle || nextTitle === project.title) return true;
  try {
    const updated = await api.updateCanvas(project.id, {
      expected_revision: project.revision,
      title: nextTitle,
    });
    updateProject(updated);
    error.value = "";
    return true;
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
    await loadProjects();
    return false;
  }
}

function updateProject(project: CanvasDocument) {
  const index = projects.value.findIndex((item) => item.id === project.id);
  if (index >= 0) projects.value[index] = project;
}

export function useStudioWorkspace() {
  if (!loaded) {
    loaded = true;
    void loadProjects();
  }

  return {
    projects,
    activeId,
    loading,
    creating,
    error,
    activeProject,
    loadProjects,
    createProject,
    renameProject,
    deleteProject,
    updateProject,
  };
}
