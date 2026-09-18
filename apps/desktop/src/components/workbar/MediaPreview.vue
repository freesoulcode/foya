<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  Maximize2Icon,
  RotateCcwIcon,
  ZoomInIcon,
  ZoomOutIcon,
} from "@lucide/vue";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const props = defineProps<{
  src: string;
  name: string;
  kind: "image" | "video";
}>();

const { t } = useI18n();
const MIN_ZOOM = 0.25;
const MAX_ZOOM = 4;
const ZOOM_STEP = 0.25;

const viewport = ref<HTMLDivElement | null>(null);
const image = ref<HTMLImageElement | null>(null);
const video = ref<HTMLVideoElement | null>(null);
const fit = ref(true);
const zoom = ref(1);
const naturalWidth = ref(0);
const naturalHeight = ref(0);
const dragging = ref(false);
let dragStartX = 0;
let dragStartY = 0;
let dragScrollLeft = 0;
let dragScrollTop = 0;
let gestureStartZoom = 1;

const zoomLabel = computed(() =>
  fit.value ? t("Fit") : `${Math.round(zoom.value * 100)}%`
);
const imageStyle = computed(() => {
  if (fit.value || !naturalWidth.value || !naturalHeight.value) return undefined;
  return {
    width: `${naturalWidth.value * zoom.value}px`,
    height: `${naturalHeight.value * zoom.value}px`,
  };
});

function fittedScale(): number {
  const target = viewport.value;
  if (!target || !naturalWidth.value || !naturalHeight.value) return 1;
  return Math.min(
    1,
    Math.max(1, target.clientWidth - 32) / naturalWidth.value,
    Math.max(1, target.clientHeight - 32) / naturalHeight.value
  );
}

function setZoom(value: number) {
  fit.value = false;
  zoom.value = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, value));
}

function changeZoom(delta: number) {
  const current = fit.value ? fittedScale() : zoom.value;
  setZoom(Number((current + delta).toFixed(2)));
}

function showActualSize() {
  fit.value = false;
  zoom.value = 1;
}

function fitToView() {
  fit.value = true;
  dragging.value = false;
}

function toggleFit() {
  if (fit.value) showActualSize();
  else fitToView();
}

function onImageLoad(event: Event) {
  const target = event.currentTarget as HTMLImageElement;
  naturalWidth.value = target.naturalWidth;
  naturalHeight.value = target.naturalHeight;
}

function onWheel(event: WheelEvent) {
  if (!event.ctrlKey && !event.metaKey) return;
  event.preventDefault();
  const current = fit.value ? fittedScale() : zoom.value;
  setZoom(current * Math.exp(-event.deltaY * 0.01));
}

function onGestureStart(event: Event) {
  event.preventDefault();
  gestureStartZoom = fit.value ? fittedScale() : zoom.value;
}

function onGestureChange(event: Event) {
  event.preventDefault();
  const scale = Number((event as Event & { scale?: number }).scale ?? 1);
  setZoom(gestureStartZoom * scale);
}

function startPan(event: PointerEvent) {
  const target = viewport.value;
  if (fit.value || !target || event.button !== 0) return;
  dragging.value = true;
  dragStartX = event.clientX;
  dragStartY = event.clientY;
  dragScrollLeft = target.scrollLeft;
  dragScrollTop = target.scrollTop;
  target.setPointerCapture(event.pointerId);
}

function movePan(event: PointerEvent) {
  const target = viewport.value;
  if (!dragging.value || !target) return;
  target.scrollLeft = dragScrollLeft - (event.clientX - dragStartX);
  target.scrollTop = dragScrollTop - (event.clientY - dragStartY);
}

function stopPan(event?: PointerEvent) {
  const target = viewport.value;
  if (event && target?.hasPointerCapture(event.pointerId)) {
    target.releasePointerCapture(event.pointerId);
  }
  dragging.value = false;
}

async function toggleVideoFullscreen() {
  const target = video.value;
  if (!target) return;
  if (document.fullscreenElement) {
    await document.exitFullscreen().catch(() => undefined);
    return;
  }
  if (target.requestFullscreen) {
    await target.requestFullscreen().catch(() => undefined);
    return;
  }
  const webkitVideo = target as HTMLVideoElement & {
    webkitEnterFullscreen?: () => void;
  };
  webkitVideo.webkitEnterFullscreen?.();
}

watch(
  () => props.src,
  () => {
    fit.value = true;
    zoom.value = 1;
    naturalWidth.value = 0;
    naturalHeight.value = 0;
    dragging.value = false;
  }
);

onBeforeUnmount(() => stopPan());
</script>

<template>
  <div
    :class="
      cn(
        'relative flex min-h-0 flex-1 overflow-hidden',
        kind === 'video' ? 'bg-black' : 'bg-muted/20'
      )
    "
  >
    <template v-if="kind === 'image'">
      <div
        class="absolute right-2 top-2 z-10 flex h-8 items-center gap-0.5 rounded-md border border-border bg-background/95 px-1 shadow-sm backdrop-blur"
      >
        <Button
          size="icon-xs"
          variant="ghost"
          :title="$t('Zoom out')"
          :aria-label="$t('Zoom out')"
          :disabled="!fit && zoom <= MIN_ZOOM"
          @click="changeZoom(-ZOOM_STEP)"
        >
          <ZoomOutIcon class="size-3.5" />
        </Button>
        <span class="w-10 text-center font-mono text-[10px] text-muted-foreground">
          {{ zoomLabel }}
        </span>
        <Button
          size="icon-xs"
          variant="ghost"
          :title="$t('Zoom in')"
          :aria-label="$t('Zoom in')"
          :disabled="!fit && zoom >= MAX_ZOOM"
          @click="changeZoom(ZOOM_STEP)"
        >
          <ZoomInIcon class="size-3.5" />
        </Button>
        <Button
          size="icon-xs"
          variant="ghost"
          :title="$t('Actual size')"
          :aria-label="$t('Actual size')"
          :disabled="!fit && zoom === 1"
          @click="showActualSize"
        >
          <RotateCcwIcon class="size-3.5" />
        </Button>
        <Button
          size="icon-xs"
          variant="ghost"
          :title="$t('Fit to view')"
          :aria-label="$t('Fit to view')"
          :disabled="fit"
          @click="fitToView"
        >
          <Maximize2Icon class="size-3.5" />
        </Button>
      </div>

      <div
        ref="viewport"
        :class="
          cn(
            'h-full w-full overflow-auto',
            !fit && (dragging ? 'cursor-grabbing' : 'cursor-grab')
          )
        "
        tabindex="0"
        @dblclick.prevent="toggleFit"
        @wheel="onWheel"
        @gesturestart="onGestureStart"
        @gesturechange="onGestureChange"
        @pointerdown="startPan"
        @pointermove="movePan"
        @pointerup="stopPan"
        @pointercancel="stopPan"
      >
        <div
          :class="
            fit
              ? 'flex h-full w-full items-center justify-center p-4'
              : 'flex h-max min-h-full w-max min-w-full items-center justify-center p-4'
          "
        >
          <img
            ref="image"
            :src="src"
            :alt="name"
            :style="imageStyle"
            :class="
              fit
                ? 'max-h-full max-w-full select-none object-contain'
                : 'max-w-none shrink-0 select-none'
            "
            draggable="false"
            @load="onImageLoad"
          />
        </div>
      </div>
    </template>

    <video
      v-else
      ref="video"
      :src="src"
      :aria-label="name"
      controls
      playsinline
      preload="metadata"
      class="m-auto max-h-full max-w-full"
      @dblclick.prevent="toggleVideoFullscreen"
    />
  </div>
</template>
