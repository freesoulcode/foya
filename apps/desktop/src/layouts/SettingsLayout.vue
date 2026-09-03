<script setup lang="ts">
import { computed, onBeforeUnmount } from "vue";
import { RouterView, useRoute, useRouter } from "vue-router";
import { useKernel } from "@/composables/useKernel";
import {
  SidebarInset,
  SidebarProvider,
} from "@/components/ui/sidebar";
import SettingsSidebar, {
  type SettingsSection,
} from "@/layouts/settings/SettingsSidebar.vue";

const route = useRoute();
const router = useRouter();
const { refreshConnections, refreshProjects } = useKernel();

const activeSection = computed(
  () => (route.meta.settingsSection ?? "connections") as SettingsSection
);

function selectSection(section: SettingsSection) {
  void router.push({ name: `settings-${section}` });
}

function close() {
  void router.push({ name: "chat" });
}

onBeforeUnmount(() => {
  void refreshConnections();
  void refreshProjects();
});
</script>

<template>
  <SidebarProvider class="h-full w-full">
    <SettingsSidebar
      :active-section="activeSection"
      @close="close"
      @select="selectSection"
    />
    <SidebarInset class="relative min-h-0 min-w-0 overflow-hidden">
      <RouterView />
    </SidebarInset>
  </SidebarProvider>
</template>
