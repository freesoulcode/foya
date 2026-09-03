<script setup lang="ts">
defineProps<{
  title: string;
  description?: string;
  contentClass?: string;
}>();
</script>

<template>
  <div class="mx-auto flex h-full min-h-0 w-full max-w-6xl flex-col px-5 pb-6 sm:px-8">
    <div
      data-tauri-drag-region
      class="mb-6 flex min-h-11 flex-wrap items-center justify-between gap-3 pt-4"
    >
      <div class="min-w-0">
        <h2 class="truncate text-base font-medium">{{ title }}</h2>
        <p v-if="description" class="mt-1 text-xs text-muted-foreground">
          {{ description }}
        </p>
      </div>
      <div v-if="$slots.actions" class="no-drag flex min-w-0 flex-wrap items-center justify-end gap-2">
        <slot name="actions" />
      </div>
    </div>

    <div :class="contentClass ?? 'flex min-h-0 flex-1 flex-col'">
      <slot />
    </div>
  </div>
</template>
