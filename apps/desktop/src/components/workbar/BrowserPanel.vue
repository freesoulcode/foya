<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { openUrl } from "@tauri-apps/plugin-opener";
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  ExternalLinkIcon,
  GlobeIcon,
  MousePointer2Icon,
  RefreshCwIcon,
  ShieldCheckIcon,
} from "@lucide/vue";
import {
  api,
  type BrowserElementSelection,
  type BrowserViewport,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const props = defineProps<{
  browserId: string;
  active: boolean;
  obscured?: boolean;
  initialUrl?: string;
}>();

const emit = defineEmits<{
  (event: "title-change", title: string): void;
  (event: "element-selected", element: BrowserElementSelection): void;
}>();

const viewport = ref<HTMLElement | null>(null);
const address = ref(props.initialUrl || "https://example.com");
const currentUrl = ref("");
const loading = ref(false);
const error = ref("");
const selectingElement = ref(false);
let frame = 0;
let lastRect = "";
let loadTimer: ReturnType<typeof setTimeout> | undefined;
let unlistenLoad: UnlistenFn | undefined;
let unlistenTitle: UnlistenFn | undefined;
let unlistenElement: UnlistenFn | undefined;
let unlistenPickerState: UnlistenFn | undefined;

interface BrowserPageLoad {
  browser_id: string;
  url: string;
  status: "started" | "finished";
}

interface BrowserTitleChanged {
  browser_id: string;
  title: string;
}

interface BrowserElementSelected {
  browser_id: string;
  element: BrowserElementSelection;
}

interface BrowserElementPickerState {
  browser_id: string;
  active: boolean;
}

function normalizeAddress(value: string): string | null {
  const raw = value.trim();
  if (!raw) return null;
  const withScheme = /^[a-z][a-z0-9+.-]*:/i.test(raw) ? raw : `https://${raw}`;
  try {
    const url = new URL(withScheme);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return url.toString();
  } catch {
    return null;
  }
}

function measureViewport(): BrowserViewport | null {
  if (!viewport.value) return null;
  const rect = viewport.value.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return null;
  return {
    x: Math.round(rect.left),
    y: Math.round(rect.top),
    width: Math.round(rect.width),
    height: Math.round(rect.height),
  };
}

function titleForUrl(value: string): string {
  try {
    return new URL(value).hostname.replace(/^www\./, "") || "新标签页";
  } catch {
    return "新标签页";
  }
}

function clearLoadTimer() {
  if (loadTimer) clearTimeout(loadTimer);
  loadTimer = undefined;
}

function armLoadTimer() {
  clearLoadTimer();
  loadTimer = setTimeout(() => {
    if (!loading.value) return;
    loading.value = false;
    error.value = "页面加载超时，请检查网络连接或网址";
  }, 30_000);
}

async function navigate() {
  const url = normalizeAddress(address.value);
  if (!url) {
    error.value = "请输入有效的 HTTP 或 HTTPS 地址";
    return;
  }
  loading.value = true;
  error.value = "";
  armLoadTimer();
  try {
    await nextTick();
    const bounds = measureViewport();
    if (!bounds) throw new Error("浏览器预览区域尚未就绪");
    currentUrl.value = url;
    address.value = url;
    emit("title-change", titleForUrl(url));
    await api.navigateBrowser(props.browserId, url, bounds);
    syncViewport();
  } catch (cause) {
    clearLoadTimer();
    currentUrl.value = "";
    loading.value = false;
    error.value = String(cause);
  }
}

async function reload() {
  if (!currentUrl.value) return;
  loading.value = true;
  error.value = "";
  armLoadTimer();
  try {
    await api.browserReload(props.browserId);
  } catch (cause) {
    clearLoadTimer();
    loading.value = false;
    error.value = String(cause);
  }
}

async function toggleElementPicker() {
  if (!currentUrl.value) return;
  const enabled = !selectingElement.value;
  error.value = "";
  try {
    await api.setBrowserElementPicker(props.browserId, enabled);
    selectingElement.value = enabled;
  } catch (cause) {
    selectingElement.value = false;
    error.value = `无法选择页面元素：${String(cause)}`;
  }
}

async function openInDefaultBrowser() {
  if (!currentUrl.value) return;
  error.value = "";
  try {
    await openUrl(currentUrl.value);
  } catch (cause) {
    error.value = `无法使用默认浏览器打开：${String(cause)}`;
  }
}

function syncViewport() {
  cancelAnimationFrame(frame);
  if (!props.active || props.obscured || !currentUrl.value || !viewport.value) {
    lastRect = "";
    void api.hideBrowser(props.browserId).catch(() => {});
    return;
  }
  const tick = () => {
    if (!props.active || props.obscured || !viewport.value) return;
    const rect = measureViewport();
    if (!rect) return;
    const key = [
      rect.x,
      rect.y,
      rect.width,
      rect.height,
    ].join(":");
    if (key !== lastRect) {
      lastRect = key;
      void api.setBrowserViewport(props.browserId, rect).catch((cause) => {
        error.value = `无法调整浏览器区域：${String(cause)}`;
      });
    }
    frame = requestAnimationFrame(tick);
  };
  frame = requestAnimationFrame(tick);
}

watch(
  () => [props.active, props.obscured] as const,
  async () => {
    await nextTick();
    syncViewport();
  },
  { immediate: true }
);

onMounted(async () => {
  try {
    unlistenLoad = await listen<BrowserPageLoad>(
      "browser-page-load",
      ({ payload }) => {
        if (payload.browser_id !== props.browserId) return;
        currentUrl.value = payload.url;
        address.value = payload.url;
        if (payload.status === "started") {
          selectingElement.value = false;
          emit("title-change", titleForUrl(payload.url));
          loading.value = true;
          armLoadTimer();
          return;
        }
        clearLoadTimer();
        loading.value = false;
        error.value = "";
      }
    );
    unlistenTitle = await listen<BrowserTitleChanged>(
      "browser-title-changed",
      ({ payload }) => {
        if (payload.browser_id !== props.browserId) return;
        const title = payload.title.trim();
        if (title) emit("title-change", title);
      }
    );
    unlistenElement = await listen<BrowserElementSelected>(
      "browser-element-selected",
      ({ payload }) => {
        if (payload.browser_id !== props.browserId) return;
        selectingElement.value = false;
        emit("element-selected", payload.element);
      }
    );
    unlistenPickerState = await listen<BrowserElementPickerState>(
      "browser-element-picker-state",
      ({ payload }) => {
        if (payload.browser_id !== props.browserId) return;
        selectingElement.value = payload.active;
      }
    );
    if (props.initialUrl) {
      address.value = props.initialUrl;
      await nextTick();
      await navigate();
    }
  } catch (cause) {
    error.value = `无法监听页面状态：${String(cause)}`;
  }
});

onBeforeUnmount(() => {
  cancelAnimationFrame(frame);
  clearLoadTimer();
  unlistenLoad?.();
  unlistenTitle?.();
  unlistenElement?.();
  unlistenPickerState?.();
  void api.closeBrowser(props.browserId).catch(() => {});
});
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex h-10 shrink-0 items-center gap-1 border-b border-border px-2">
      <Button
        size="icon"
        variant="ghost"
        class="size-7"
        title="后退"
        :disabled="!currentUrl"
        @click="api.browserBack(browserId)"
      >
        <ArrowLeftIcon class="size-3.5" />
      </Button>
      <Button
        size="icon"
        variant="ghost"
        class="size-7"
        title="前进"
        :disabled="!currentUrl"
        @click="api.browserForward(browserId)"
      >
        <ArrowRightIcon class="size-3.5" />
      </Button>
      <Button
        size="icon"
        variant="ghost"
        class="size-7"
        title="刷新"
        :disabled="!currentUrl"
        @click="reload"
      >
        <RefreshCwIcon :class="['size-3.5', loading && 'animate-spin']" />
      </Button>
      <form class="relative min-w-0 flex-1" @submit.prevent="navigate">
        <ShieldCheckIcon class="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          v-model="address"
          class="h-7 pl-7 pr-2 text-xs"
          aria-label="浏览器地址"
          placeholder="输入网址"
        />
      </form>
      <Button
        size="icon"
        variant="ghost"
        class="size-7"
        title="在默认浏览器中打开"
        :disabled="!currentUrl"
        @click="openInDefaultBrowser"
      >
        <ExternalLinkIcon class="size-3.5" />
      </Button>
      <Button
        size="icon"
        :variant="selectingElement ? 'secondary' : 'ghost'"
        class="size-7"
        :class="selectingElement && 'text-blue-600 dark:text-blue-400'"
        :title="selectingElement ? '取消选择元素' : '选择页面元素'"
        :aria-pressed="selectingElement"
        :disabled="!currentUrl || loading"
        @click="toggleElementPicker"
      >
        <MousePointer2Icon class="size-3.5" />
      </Button>
    </div>

    <div
      v-if="error"
      class="border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
    >
      {{ error }}
    </div>

    <div ref="viewport" class="relative min-h-0 flex-1 bg-background">
      <div
        v-if="!currentUrl"
        class="absolute inset-0 flex flex-col items-center justify-center px-8 text-center"
      >
        <div class="mb-3 flex size-10 items-center justify-center rounded-lg bg-muted">
          <GlobeIcon class="size-5 text-muted-foreground" />
        </div>
        <p class="text-sm font-medium">打开网页</p>
        <p class="mt-1 text-xs leading-relaxed text-muted-foreground">
          输入地址后，页面会在此区域打开。
        </p>
      </div>
    </div>
  </div>
</template>
