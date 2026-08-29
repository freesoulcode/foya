<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  GlobeIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
} from "@lucide/vue";
import { api, type BrowserViewport } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const props = defineProps<{
  browserId: string;
  active: boolean;
  obscured?: boolean;
}>();

const emit = defineEmits<{
  (event: "title-change", title: string): void;
}>();

const viewport = ref<HTMLElement | null>(null);
const address = ref("https://example.com");
const currentUrl = ref("");
const loading = ref(false);
const error = ref("");
let frame = 0;
let lastRect = "";
let loadTimer: ReturnType<typeof setTimeout> | undefined;
let unlistenLoad: UnlistenFn | undefined;
let unlistenTitle: UnlistenFn | undefined;

interface BrowserPageLoad {
  browser_id: string;
  url: string;
  status: "started" | "finished";
}

interface BrowserTitleChanged {
  browser_id: string;
  title: string;
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
  } catch (cause) {
    error.value = `无法监听页面状态：${String(cause)}`;
  }
});

onBeforeUnmount(() => {
  cancelAnimationFrame(frame);
  clearLoadTimer();
  unlistenLoad?.();
  unlistenTitle?.();
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
