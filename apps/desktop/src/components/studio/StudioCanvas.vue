<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import {
  FilmIcon,
  DownloadIcon,
  Grid2X2Icon,
  HandIcon,
  ImagePlusIcon,
  LoaderCircleIcon,
  MousePointer2Icon,
  Redo2Icon,
  RotateCcwIcon,
  SparklesIcon,
  Trash2Icon,
  TypeIcon,
  ZoomInIcon,
  ZoomOutIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  api,
  type CanvasAsset,
  type CanvasDocument,
  type CanvasEdge,
  type CanvasNode,
  type ConnectionConfig,
} from "@/lib/api";

const props = defineProps<{ projectId: string }>();
const emit = defineEmits<{
  (event: "project-change", project: CanvasDocument): void;
  (event: "selection-change", nodeIds: string[]): void;
}>();

type Snapshot = { nodes: CanvasNode[]; edges: CanvasEdge[] };
type Tool = "select" | "pan";

const viewportEl = ref<HTMLElement>();
const fileInput = ref<HTMLInputElement>();
const project = ref<CanvasDocument>();
const loading = ref(true);
const saving = ref(false);
const error = ref("");
const tool = ref<Tool>("select");
const selected = ref<Set<string>>(new Set());
const selectedEdge = ref("");
const connectingFrom = ref("");
const connectionTarget = ref("");
const connectionCursor = ref<{ x: number; y: number }>();
const assetURLs = ref<Record<string, string>>({});
const connections = ref<ConnectionConfig[]>([]);
const history = ref<Snapshot[]>([]);
const future = ref<Snapshot[]>([]);
const marquee = ref<{ x: number; y: number; width: number; height: number }>();
const spacePressed = ref(false);
const pan = reactive({ x: 160, y: 120 });
const zoom = ref(1);
let mounted = true;
let saveTimer: ReturnType<typeof setTimeout> | undefined;
let saveAgain = false;

const nodes = computed(() => project.value?.nodes ?? []);
const edges = computed(() => project.value?.edges ?? []);
const assets = computed(
  () => new Map((project.value?.assets ?? []).map((asset) => [asset.id, asset]))
);
const configuredConnections = computed(() =>
  connections.value.filter(
    (connection): connection is ConnectionConfig & { id: string } =>
      Boolean(connection.id)
  )
);
const imageGenerationConnections = computed(() =>
  configuredConnections.value.filter((connection) =>
    (connection.models ?? []).some(
      (model) => {
        const settings = connection.model_settings?.[model];
        return !settings?.capabilities_configured || settings.image_generation;
      }
    )
  )
);
function imageGenerationModels(connectionID?: string) {
  const connection = configuredConnections.value.find((item) => item.id === connectionID);
  return (connection?.models ?? []).filter(
    (model) => {
      const settings = connection?.model_settings?.[model];
      return !settings?.capabilities_configured || settings.image_generation;
    }
  );
}
const worldStyle = computed(() => ({
  transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom.value})`,
}));
const gridStyle = computed(() => {
  if (project.value?.background === "blank") return {};
  const size = (project.value?.background === "grid" ? 48 : 24) * zoom.value;
  return {
    backgroundPosition: `${pan.x}px ${pan.y}px`,
    backgroundSize: `${size}px ${size}px`,
  };
});
function id(prefix: string) {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

function cloneNodes(value = nodes.value) {
  return value.map((node) => ({
    ...node,
    generation: node.generation ? { ...node.generation } : undefined,
  }));
}

function cloneEdges(value = edges.value) {
  return value.map((edge) => ({ ...edge }));
}

function snapshot(): Snapshot {
  return { nodes: cloneNodes(), edges: cloneEdges() };
}

function commitHistory() {
  history.value.push(snapshot());
  if (history.value.length > 100) history.value.shift();
  future.value = [];
}

function applySnapshot(value: Snapshot) {
  if (!project.value) return;
  project.value.nodes = cloneNodes(value.nodes);
  project.value.edges = cloneEdges(value.edges);
  selected.value = new Set();
  selectedEdge.value = "";
  emitSelection();
  scheduleSave();
}

function undo() {
  const value = history.value.pop();
  if (!value) return;
  future.value.push(snapshot());
  applySnapshot(value);
}

function redo() {
  const value = future.value.pop();
  if (!value) return;
  history.value.push(snapshot());
  applySnapshot(value);
}

function scheduleSave(delay = 180) {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => void persist(), delay);
}

async function persist() {
  const current = project.value;
  if (!current) return;
  if (saving.value) {
    saveAgain = true;
    return;
  }
  saving.value = true;
  const payload = {
    expected_revision: current.revision,
    title: current.title,
    nodes: cloneNodes(),
    edges: cloneEdges(),
    viewport: { x: pan.x, y: pan.y, zoom: zoom.value },
    background: current.background,
  };
  try {
    const updated = await api.updateCanvas(current.id, payload);
    if (!mounted || project.value?.id !== updated.id) return;
    project.value = {
      ...updated,
      nodes: payload.nodes,
      edges: payload.edges,
      viewport: payload.viewport,
      background: payload.background,
    };
    emit("project-change", project.value);
    error.value = "";
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  } finally {
    saving.value = false;
    if (saveAgain) {
      saveAgain = false;
      void persist();
    }
  }
}

async function flushSave() {
  clearTimeout(saveTimer);
  await persist();
  while (saving.value || saveAgain) {
    await new Promise((resolve) => window.setTimeout(resolve, 20));
  }
}

async function loadAssets() {
  for (const asset of project.value?.assets ?? []) {
    if (assetURLs.value[asset.id]) continue;
    try {
      const bytes = await api.readCanvasAsset(props.projectId, asset.id);
      if (!mounted) return;
      assetURLs.value = {
        ...assetURLs.value,
        [asset.id]: URL.createObjectURL(new Blob([bytes], { type: asset.media_type })),
      };
    } catch {
      // The node keeps its metadata and can be repaired by replacing the asset.
    }
  }
}

async function downloadAsset(node: CanvasNode) {
  if (!node.asset_id || !project.value) return;
  const asset = assets.value.get(node.asset_id);
  if (!asset) return;
  try {
    const bytes = await api.readCanvasAsset(project.value.id, asset.id);
    const url = URL.createObjectURL(new Blob([bytes], { type: asset.media_type }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = asset.name || `${node.title || "generated"}.${asset.kind}`;
    anchor.click();
    URL.revokeObjectURL(url);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  }
}

async function load() {
  loading.value = true;
  try {
    const [value, availableConnections] = await Promise.all([
      api.getCanvas(props.projectId),
      api.listConnections(),
    ]);
    connections.value = availableConnections;
    project.value = {
      ...value,
      nodes: value.nodes ?? [],
      edges: value.edges ?? [],
      viewport: value.viewport ?? { x: 160, y: 120, zoom: 1 },
      background: value.background ?? "dots",
    };
    pan.x = project.value.viewport.x;
    pan.y = project.value.viewport.y;
    zoom.value = project.value.viewport.zoom;
    await loadAssets();
    emit("project-change", project.value);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
  } finally {
    loading.value = false;
  }
}

function screenPoint(event: { clientX: number; clientY: number }) {
  const rect = viewportEl.value?.getBoundingClientRect();
  return {
    x: event.clientX - (rect?.left ?? 0),
    y: event.clientY - (rect?.top ?? 0),
  };
}

function worldPoint(event: { clientX: number; clientY: number }) {
  const point = screenPoint(event);
  return { x: (point.x - pan.x) / zoom.value, y: (point.y - pan.y) / zoom.value };
}

function centerPoint() {
  const rect = viewportEl.value?.getBoundingClientRect();
  return {
    x: ((rect?.width ?? 800) / 2 - pan.x) / zoom.value,
    y: ((rect?.height ?? 600) / 2 - pan.y) / zoom.value,
  };
}

function emitSelection() {
  emit("selection-change", [...selected.value]);
}

function selectNode(event: PointerEvent, node: CanvasNode) {
  if (event.shiftKey) {
    const next = new Set(selected.value);
    if (next.has(node.id)) next.delete(node.id);
    else next.add(node.id);
    selected.value = next;
  } else if (!selected.value.has(node.id)) {
    selected.value = new Set([node.id]);
  }
  selectedEdge.value = "";
  emitSelection();
}

function startNodeDrag(event: PointerEvent, node: CanvasNode) {
  if (event.button !== 0) return;
  event.stopPropagation();
  selectNode(event, node);
  commitHistory();
  const origin = worldPoint(event);
  const starts = new Map(
    nodes.value
      .filter((item) => selected.value.has(item.id))
      .map((item) => [item.id, { x: item.x, y: item.y }])
  );
  const move = (next: PointerEvent) => {
    const point = worldPoint(next);
    for (const item of nodes.value) {
      const start = starts.get(item.id);
      if (!start) continue;
      item.x = Math.round(start.x + point.x - origin.x);
      item.y = Math.round(start.y + point.y - origin.y);
    }
  };
  const finish = () => {
    window.removeEventListener("pointermove", move);
    scheduleSave();
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", finish, { once: true });
}

function startResize(event: PointerEvent, node: CanvasNode) {
  event.preventDefault();
  event.stopPropagation();
  commitHistory();
  const origin = worldPoint(event);
  const width = node.width;
  const height = node.height;
  const move = (next: PointerEvent) => {
    const point = worldPoint(next);
    node.width = Math.round(Math.max(120, width + point.x - origin.x));
    node.height = Math.round(Math.max(72, height + point.y - origin.y));
  };
  const finish = () => {
    window.removeEventListener("pointermove", move);
    scheduleSave();
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", finish, { once: true });
}

function startViewport(event: PointerEvent) {
  if (event.target !== viewportEl.value) return;
  const shouldPan = tool.value === "pan" || event.button === 1 || spacePressed.value;
  if (shouldPan) {
    event.preventDefault();
    const start = { ...pan };
    const move = (next: PointerEvent) => {
      pan.x = start.x + next.clientX - event.clientX;
      pan.y = start.y + next.clientY - event.clientY;
    };
    const finish = () => {
      window.removeEventListener("pointermove", move);
      scheduleSave(500);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", finish, { once: true });
    return;
  }
  if (event.button !== 0) return;
  connectingFrom.value = "";
  connectionCursor.value = undefined;
  connectionTarget.value = "";
  selected.value = new Set();
  selectedEdge.value = "";
  emitSelection();
  const origin = screenPoint(event);
  marquee.value = { x: origin.x, y: origin.y, width: 0, height: 0 };
  const move = (next: PointerEvent) => {
    const point = screenPoint(next);
    marquee.value = {
      x: Math.min(origin.x, point.x),
      y: Math.min(origin.y, point.y),
      width: Math.abs(point.x - origin.x),
      height: Math.abs(point.y - origin.y),
    };
  };
  const finish = () => {
    const box = marquee.value;
    if (box && box.width > 3 && box.height > 3) {
      selected.value = new Set(
        nodes.value
          .filter((node) => {
            const left = pan.x + node.x * zoom.value;
            const top = pan.y + node.y * zoom.value;
            return (
              left < box.x + box.width &&
              left + node.width * zoom.value > box.x &&
              top < box.y + box.height &&
              top + node.height * zoom.value > box.y
            );
          })
          .map((node) => node.id)
      );
      emitSelection();
    }
    marquee.value = undefined;
    window.removeEventListener("pointermove", move);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", finish, { once: true });
}

function onWheel(event: WheelEvent) {
  event.preventDefault();
  if (!event.ctrlKey && !event.metaKey) {
    pan.x -= event.deltaX;
    pan.y -= event.deltaY;
    scheduleSave(500);
    return;
  }
  const point = screenPoint(event);
  const worldX = (point.x - pan.x) / zoom.value;
  const worldY = (point.y - pan.y) / zoom.value;
  const next = Math.min(4, Math.max(0.15, zoom.value * Math.exp(-event.deltaY * 0.002)));
  pan.x = point.x - worldX * next;
  pan.y = point.y - worldY * next;
  zoom.value = next;
  scheduleSave(500);
}

function setZoom(next: number) {
  const rect = viewportEl.value?.getBoundingClientRect();
  const point = { x: (rect?.width ?? 800) / 2, y: (rect?.height ?? 600) / 2 };
  const worldX = (point.x - pan.x) / zoom.value;
  const worldY = (point.y - pan.y) / zoom.value;
  zoom.value = Math.min(4, Math.max(0.15, next));
  pan.x = point.x - worldX * zoom.value;
  pan.y = point.y - worldY * zoom.value;
  scheduleSave(500);
}

function resetView() {
  pan.x = 160;
  pan.y = 120;
  zoom.value = 1;
  scheduleSave(500);
}

function addNode(type: "text" | "generation", at = centerPoint()) {
  if (!project.value) return;
  commitHistory();
  const common = {
    id: id(type),
    type,
    x: Math.round(at.x - 140),
    y: Math.round(at.y - 90),
    width: 280,
    height: 180,
    z_index: nodes.value.length + 1,
  };
  const firstConnection = imageGenerationConnections.value[0];
  const firstModel = imageGenerationModels(firstConnection?.id)[0] ?? "";
  const node: CanvasNode = type === "text"
    ? { ...common, title: "提示词", text: "", height: 160 }
    : {
        ...common,
        title: "生成",
        prompt: "",
        width: 300,
        height: 310,
        generation: {
          mode: "image",
          connection_id: firstConnection?.id,
          model: firstModel,
          aspect_ratio: "1:1",
          quality: "auto",
          count: 1,
        },
        status: "idle",
      };
  project.value.nodes = [...nodes.value, node];
  selected.value = new Set([node.id]);
  emitSelection();
  scheduleSave();
}

async function generateImage(node: CanvasNode) {
  if (!project.value || node.type !== "generation" || !node.generation) return;
  if (!node.generation.model?.trim()) {
    error.value = "请先填写生图模型";
    return;
  }
  if (node.status === "running") return;

  clearTimeout(saveTimer);
  commitHistory();
  const outputID = id("image");
  const output: CanvasNode = {
    id: outputID,
    type: "image",
    title: "生成结果",
    status: "running",
    x: node.x + node.width + 140,
    y: node.y,
    width: 360,
    height: 360,
    z_index: nodes.value.length + 1,
  };
  node.status = "running";
  node.error = "";
  project.value.nodes = [...nodes.value, output];
  project.value.edges = [
    ...edges.value,
    {
      id: id("edge"),
      from_node_id: node.id,
      to_node_id: outputID,
      kind: "output",
    },
  ];
  selected.value = new Set([outputID]);
  emitSelection();

  await flushSave();
  if (!project.value) return;
  try {
    const updated = await api.generateCanvasImage(project.value.id, {
      expected_revision: project.value.revision,
      config_node_id: node.id,
      output_node_id: outputID,
      connection_id: node.generation.connection_id,
    });
    project.value = updated;
    await loadAssets();
    emit("project-change", updated);
    error.value = "";
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : String(reason);
    try {
      project.value = await api.getCanvas(props.projectId);
      await loadAssets();
    } catch {
      node.status = "error";
      output.status = "error";
    }
  }
}

function startToolDrag(event: DragEvent, type: CanvasNode["type"]) {
  event.dataTransfer?.setData("application/x-foya-node", type);
  if (event.dataTransfer) event.dataTransfer.effectAllowed = "copy";
}

function onDrop(event: DragEvent) {
  event.preventDefault();
  const nodeType = event.dataTransfer?.getData("application/x-foya-node") as CanvasNode["type"];
  if (nodeType === "text" || nodeType === "generation") {
    addNode(nodeType, worldPoint(event));
    return;
  }
  if (event.dataTransfer?.files.length) void uploadFiles(event.dataTransfer.files);
}

function inputSummary(nodeID: string) {
  let text = 0;
  let image = 0;
  for (const edge of edges.value) {
    if (edge.to_node_id !== nodeID) continue;
    const source = nodes.value.find((node) => node.id === edge.from_node_id);
    if (source?.type === "text") text++;
    if (source?.type === "image") image++;
  }
  return { text, image };
}

function updateGenerationField(
  node: CanvasNode,
  field: "connection_id" | "model" | "aspect_ratio" | "quality",
  value: unknown
) {
  if (!node.generation) return;
  const nextValue = String(value ?? "");
  node.generation[field] = nextValue;
  if (field === "connection_id") {
    const models = imageGenerationModels(nextValue);
    if (!models.includes(node.generation.model ?? "")) {
      node.generation.model = models[0] ?? "";
    }
  }
  scheduleSave();
}

function updateBackground(value: unknown) {
  if (!project.value) return;
  const background = String(value);
  if (background !== "dots" && background !== "grid" && background !== "blank") return;
  project.value.background = background;
  scheduleSave();
}

async function uploadFiles(files: FileList | File[]) {
  if (!project.value) return;
  const point = centerPoint();
  let offset = 0;
  for (const file of Array.from(files)) {
    if (!file.type.startsWith("image/") && !file.type.startsWith("video/")) continue;
    try {
      const result = await api.uploadCanvasAsset(props.projectId, file);
      project.value = {
        ...result.canvas,
        nodes: cloneNodes(),
        edges: cloneEdges(),
      };
      const asset: CanvasAsset = result.asset;
      const width = asset.kind === "video" ? 420 : Math.min(480, asset.width || 360);
      const ratio = asset.width && asset.height ? asset.width / asset.height : 16 / 9;
      commitHistory();
      const node: CanvasNode = {
        id: id(asset.kind),
        type: asset.kind,
        title: asset.name,
        asset_id: asset.id,
        x: point.x - width / 2 + offset,
        y: point.y - width / ratio / 2 + offset,
        width,
        height: Math.max(100, width / ratio),
        z_index: nodes.value.length + 1,
      };
      project.value.nodes = [...nodes.value, node];
      selected.value = new Set([node.id]);
      emitSelection();
      offset += 28;
      await loadAssets();
      await persist();
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : String(reason);
    }
  }
  if (fileInput.value) fileInput.value.value = "";
}

function beginConnection(event: PointerEvent, nodeId: string) {
  event.preventDefault();
  event.stopPropagation();
  connectingFrom.value = nodeId;
  connectionCursor.value = worldPoint(event);
  const move = (next: PointerEvent) => {
    connectionCursor.value = worldPoint(next);
    const target = document
      .elementFromPoint(next.clientX, next.clientY)
      ?.closest<HTMLElement>("[data-node-input]");
    connectionTarget.value = target?.dataset.nodeInput ?? "";
  };
  const finish = (next: PointerEvent) => {
    window.removeEventListener("pointermove", move);
    const target = document
      .elementFromPoint(next.clientX, next.clientY)
      ?.closest<HTMLElement>("[data-node-input]");
    const targetID = target?.dataset.nodeInput ?? "";
    if (
      project.value &&
      targetID &&
      targetID !== nodeId &&
      !edges.value.some(
        (edge) => edge.from_node_id === nodeId && edge.to_node_id === targetID
      )
    ) {
      commitHistory();
      project.value.edges = [
        ...edges.value,
        {
          id: id("edge"),
          from_node_id: nodeId,
          to_node_id: targetID,
          kind: "reference",
        },
      ];
      scheduleSave();
    }
    connectingFrom.value = "";
    connectionTarget.value = "";
    connectionCursor.value = undefined;
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", finish, { once: true });
}

function edgePath(edge: CanvasEdge) {
  const from = nodes.value.find((node) => node.id === edge.from_node_id);
  const to = nodes.value.find((node) => node.id === edge.to_node_id);
  if (!from || !to) return "";
  const x1 = from.x + from.width;
  const y1 = from.y + from.height / 2;
  const x2 = to.x;
  const y2 = to.y + to.height / 2;
  const bend = Math.max(70, Math.abs(x2 - x1) * 0.45);
  return `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}`;
}

function pendingEdgePath() {
  const from = nodes.value.find((node) => node.id === connectingFrom.value);
  const cursor = connectionCursor.value;
  if (!from || !cursor) return "";
  const x1 = from.x + from.width;
  const y1 = from.y + from.height / 2;
  const bend = Math.max(70, Math.abs(cursor.x - x1) * 0.45);
  return `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${cursor.x - bend} ${cursor.y}, ${cursor.x} ${cursor.y}`;
}

function removeSelection() {
  if (!project.value) return;
  if (!selected.value.size && !selectedEdge.value) return;
  commitHistory();
  const removed = selected.value;
  project.value.nodes = nodes.value.filter((node) => !removed.has(node.id));
  project.value.edges = edges.value.filter(
    (edge) =>
      edge.id !== selectedEdge.value &&
      !removed.has(edge.from_node_id) &&
      !removed.has(edge.to_node_id)
  );
  selected.value = new Set();
  selectedEdge.value = "";
  emitSelection();
  scheduleSave();
}

function onKeyDown(event: KeyboardEvent) {
  if ((event.target as HTMLElement | null)?.matches("input,textarea,select,[contenteditable=true]")) return;
  if (event.code === "Space") {
    spacePressed.value = true;
    event.preventDefault();
  }
  if (event.key === "Backspace" || event.key === "Delete") {
    event.preventDefault();
    removeSelection();
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "z") {
    event.preventDefault();
    event.shiftKey ? redo() : undo();
  }
  if (event.key === "Escape") {
    connectingFrom.value = "";
    connectionTarget.value = "";
    connectionCursor.value = undefined;
  }
}

function onKeyUp(event: KeyboardEvent) {
  if (event.code === "Space") spacePressed.value = false;
}

function nodeIcon(type: CanvasNode["type"]) {
  if (type === "text") return TypeIcon;
  if (type === "image") return ImagePlusIcon;
  if (type === "video") return FilmIcon;
  return SparklesIcon;
}

onMounted(() => {
  window.addEventListener("keydown", onKeyDown);
  window.addEventListener("keyup", onKeyUp);
  void load();
});
onBeforeUnmount(() => {
  mounted = false;
  clearTimeout(saveTimer);
  window.removeEventListener("keydown", onKeyDown);
  window.removeEventListener("keyup", onKeyUp);
  Object.values(assetURLs.value).forEach(URL.revokeObjectURL);
});
</script>

<template>
  <section class="relative flex size-full min-h-0 flex-col overflow-hidden bg-background">
    <div
      ref="viewportEl"
      class="studio-canvas relative min-h-0 flex-1 overflow-hidden bg-muted/10"
      :class="[
        project?.background ?? 'dots',
        tool === 'pan' || spacePressed ? 'cursor-grab' : 'cursor-default',
      ]"
      :style="gridStyle"
      @pointerdown="startViewport"
      @wheel="onWheel"
      @dragover.prevent
      @drop="onDrop"
    >
      <div v-if="loading" class="absolute inset-0 grid place-items-center"><LoaderCircleIcon class="size-5 animate-spin text-muted-foreground" /></div>
      <div v-else class="absolute left-0 top-0 origin-top-left" :style="worldStyle">
        <svg class="pointer-events-none absolute left-0 top-0 overflow-visible">
          <path
            v-for="edge in edges"
            :key="edge.id"
            :d="edgePath(edge)"
            fill="none"
            :stroke="selectedEdge === edge.id ? 'var(--primary)' : 'var(--muted-foreground)'"
            :stroke-width="selectedEdge === edge.id ? 3 : 2"
            stroke-linecap="round"
            class="pointer-events-auto cursor-pointer opacity-70"
            @pointerdown.stop="selectedEdge = edge.id; selected = new Set(); emitSelection()"
          />
          <path
            v-if="connectingFrom && connectionCursor"
            :d="pendingEdgePath()"
            fill="none"
            stroke="var(--primary)"
            stroke-width="2.5"
            stroke-linecap="round"
            stroke-dasharray="6 5"
          />
        </svg>

        <article
          v-for="node in nodes"
          :key="node.id"
          :class="[
            'absolute flex flex-col border bg-background shadow-sm',
            selected.has(node.id) ? 'border-primary ring-1 ring-primary' : 'border-border',
          ]"
          :style="{
            left: node.x + 'px',
            top: node.y + 'px',
            width: node.width + 'px',
            height: node.height + 'px',
            zIndex: node.z_index,
            transform: `rotate(${node.rotation ?? 0}deg)`,
          }"
          @pointerdown="startNodeDrag($event, node)"
        >
          <header class="flex h-8 shrink-0 cursor-move items-center gap-2 border-b border-border px-2">
            <component :is="nodeIcon(node.type)" class="size-3.5 text-muted-foreground" />
            <span class="min-w-0 flex-1 truncate text-xs font-medium">{{ node.title }}</span>
            <Button
              v-if="node.type === 'image' && node.asset_id"
              size="icon"
              variant="ghost"
              class="no-drag size-6 shrink-0"
              title="下载图片"
              @pointerdown.stop
              @click.stop="downloadAsset(node)"
            >
              <DownloadIcon class="size-3.5" />
            </Button>
            <span v-if="node.status && node.status !== 'idle'" class="text-[10px] text-muted-foreground">{{ node.status }}</span>
          </header>

          <Textarea
            v-if="node.type === 'text'"
            v-model="node.text"
            class="min-h-0 flex-1 resize-none rounded-none border-0 bg-transparent p-3 text-sm leading-6 shadow-none focus-visible:ring-0"
            placeholder="写下想法或提示词"
            @pointerdown.stop
            @update:model-value="scheduleSave()"
          />
          <img
            v-else-if="node.type === 'image' && node.asset_id && assetURLs[node.asset_id]"
            :src="assetURLs[node.asset_id]"
            :alt="assets.get(node.asset_id)?.name ?? ''"
            class="pointer-events-none min-h-0 flex-1 select-none object-contain"
            draggable="false"
          />
          <video
            v-else-if="node.type === 'video' && node.asset_id && assetURLs[node.asset_id]"
            :src="assetURLs[node.asset_id]"
            class="min-h-0 flex-1 bg-black object-contain"
            controls
            @pointerdown.stop
          />
          <div v-else-if="node.type === 'generation'" class="min-h-0 flex-1 space-y-2 overflow-auto p-3" @pointerdown.stop>
            <div class="flex items-center gap-3 text-[11px] text-muted-foreground">
              <span>提示词 {{ inputSummary(node.id).text }}</span>
              <span>参考图 {{ inputSummary(node.id).image }}</span>
            </div>
            <Textarea
              v-model="node.prompt"
              class="min-h-14 resize-none text-xs"
              placeholder="补充描述（可选）"
              @update:model-value="scheduleSave()"
            />
            <div class="grid grid-cols-2 gap-2">
              <Select
                :model-value="node.generation!.connection_id"
                @update:model-value="updateGenerationField(node, 'connection_id', $event)"
              >
                <SelectTrigger size="sm" class="w-full text-xs">
                  <SelectValue placeholder="默认连接" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="connection in imageGenerationConnections" :key="connection.id" :value="connection.id">
                    {{ connection.name }}
                  </SelectItem>
                </SelectContent>
              </Select>
              <Select
                :model-value="node.generation!.aspect_ratio"
                @update:model-value="updateGenerationField(node, 'aspect_ratio', $event)"
              >
                <SelectTrigger size="sm" class="w-full text-xs">
                  <SelectValue placeholder="比例" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="1:1">1:1</SelectItem>
                  <SelectItem value="4:3">4:3</SelectItem>
                  <SelectItem value="3:4">3:4</SelectItem>
                  <SelectItem value="16:9">16:9</SelectItem>
                  <SelectItem value="9:16">9:16</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <Select
              :model-value="node.generation!.model"
              :disabled="!imageGenerationModels(node.generation!.connection_id).length"
              @update:model-value="updateGenerationField(node, 'model', $event)"
            >
              <SelectTrigger size="sm" class="w-full text-xs">
                <SelectValue placeholder="选择生图模型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem
                  v-for="model in imageGenerationModels(node.generation!.connection_id)"
                  :key="model"
                  :value="model"
                >
                  {{ model }}
                </SelectItem>
              </SelectContent>
            </Select>
            <div class="grid grid-cols-2 gap-2">
              <Select
                :model-value="node.generation!.quality"
                @update:model-value="updateGenerationField(node, 'quality', $event)"
              >
                <SelectTrigger size="sm" class="w-full text-xs">
                  <SelectValue placeholder="质量" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">自动质量</SelectItem>
                  <SelectItem value="low">低</SelectItem>
                  <SelectItem value="medium">中</SelectItem>
                  <SelectItem value="high">高</SelectItem>
                  <SelectItem value="standard">标准</SelectItem>
                  <SelectItem value="hd">HD</SelectItem>
                </SelectContent>
              </Select>
              <Button size="sm" class="h-8" :disabled="node.status === 'running'" @click="generateImage(node)">
                <LoaderCircleIcon v-if="node.status === 'running'" class="size-3.5 animate-spin" />
                <SparklesIcon v-else class="size-3.5" />
                {{ node.status === "running" ? "生成中" : "生成图片" }}
              </Button>
            </div>
            <p v-if="node.error" class="line-clamp-2 text-[11px] text-destructive">{{ node.error }}</p>
          </div>
          <div v-else-if="node.status === 'running'" class="grid min-h-0 flex-1 place-items-center">
            <LoaderCircleIcon class="size-6 animate-spin text-muted-foreground" />
          </div>
          <div v-else-if="node.status === 'error'" class="grid min-h-0 flex-1 place-items-center px-4 text-center text-xs text-destructive">
            {{ node.error || "生成失败" }}
          </div>
          <div v-else class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground">素材不可用</div>

          <button
            type="button"
            :data-node-input="node.id"
            :class="[
              'absolute -left-2 top-1/2 size-4 -translate-y-1/2 rounded-full border bg-background transition-all',
              connectionTarget === node.id ? 'scale-125 border-primary bg-primary' : 'border-border hover:border-primary',
            ]"
            title="输入端口"
            @pointerdown.stop
          />
          <button
            type="button"
            class="absolute -right-2 top-1/2 size-4 -translate-y-1/2 cursor-crosshair rounded-full border border-border bg-background transition-all hover:scale-125 hover:border-primary hover:bg-primary"
            title="拖动以连接"
            @pointerdown="beginConnection($event, node.id)"
          />
          <button v-if="selected.has(node.id)" type="button" class="absolute -bottom-1.5 -right-1.5 size-3 cursor-nwse-resize border border-primary bg-background" title="调整大小" @pointerdown="startResize($event, node)" />
        </article>
      </div>
      <div v-if="marquee" class="pointer-events-none absolute border border-primary bg-primary/10" :style="{ left: marquee.x + 'px', top: marquee.y + 'px', width: marquee.width + 'px', height: marquee.height + 'px' }" />

      <div class="absolute bottom-4 left-1/2 z-40 flex h-10 -translate-x-1/2 items-center gap-0.5 rounded-[8px] border border-border bg-background/95 p-0.5 shadow-lg backdrop-blur">
        <TooltipProvider :delay-duration="250">
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" :class="tool === 'select' && 'bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground'" @click="tool = 'select'">
                <MousePointer2Icon class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">选择</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" :class="tool === 'pan' && 'bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground'" @click="tool = 'pan'">
                <HandIcon class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">移动画布</TooltipContent>
          </Tooltip>
          <Separator orientation="vertical" class="mx-0.5 h-5" />
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" draggable="true" @dragstart="startToolDrag($event, 'text')" @click="addNode('text')">
                <TypeIcon class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">提示词</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" @click="fileInput?.click()">
                <ImagePlusIcon class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">导入图片或视频</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" draggable="true" @dragstart="startToolDrag($event, 'generation')" @click="addNode('generation')">
                <SparklesIcon class="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">生图配置</TooltipContent>
          </Tooltip>
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" title="画布背景">
                <Grid2X2Icon class="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="top" align="center" class="w-32">
              <DropdownMenuRadioGroup :model-value="project?.background" @update:model-value="updateBackground">
                <DropdownMenuRadioItem value="dots">点阵</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="grid">网格</DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="blank">纯色</DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
          <input ref="fileInput" class="hidden" type="file" multiple accept="image/*,video/*" @change="uploadFiles(($event.target as HTMLInputElement).files ?? [])" />
          <Separator orientation="vertical" class="mx-0.5 h-5" />
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" :disabled="!history.length" @click="undo"><RotateCcwIcon class="size-4" /></Button>
            </TooltipTrigger>
            <TooltipContent side="top">撤销</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" :disabled="!future.length" @click="redo"><Redo2Icon class="size-4" /></Button>
            </TooltipTrigger>
            <TooltipContent side="top">重做</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" :disabled="!selected.size && !selectedEdge" @click="removeSelection"><Trash2Icon class="size-4" /></Button>
            </TooltipTrigger>
            <TooltipContent side="top">删除所选</TooltipContent>
          </Tooltip>
          <Separator orientation="vertical" class="mx-0.5 h-5" />
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" @click="setZoom(zoom / 1.2)"><ZoomOutIcon class="size-4" /></Button>
            </TooltipTrigger>
            <TooltipContent side="top">缩小</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button variant="ghost" class="h-8 w-11 px-1 text-[11px] tabular-nums text-muted-foreground" @click="resetView">
                {{ Math.round(zoom * 100) }}%
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">重置视图</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <Button size="icon" variant="ghost" class="size-8" @click="setZoom(zoom * 1.2)"><ZoomInIcon class="size-4" /></Button>
            </TooltipTrigger>
            <TooltipContent side="top">放大</TooltipContent>
          </Tooltip>
          <LoaderCircleIcon v-if="saving" class="mx-1 size-3.5 animate-spin text-muted-foreground" />
        </TooltipProvider>
      </div>
      <p v-if="error" class="absolute bottom-3 left-3 max-w-[70%] rounded bg-destructive px-3 py-1.5 text-xs text-destructive-foreground">{{ error }}</p>
    </div>
  </section>
</template>

<style scoped>
.studio-canvas {
  touch-action: none;
}
.studio-canvas.dots {
  background-image: radial-gradient(circle, color-mix(in oklab, var(--muted-foreground) 28%, transparent) 1px, transparent 1px);
}
.studio-canvas.grid {
  background-image:
    linear-gradient(to right, color-mix(in oklab, var(--muted-foreground) 14%, transparent) 1px, transparent 1px),
    linear-gradient(to bottom, color-mix(in oklab, var(--muted-foreground) 14%, transparent) 1px, transparent 1px);
}
article {
  border-radius: 6px;
  transform-origin: center;
}
</style>
