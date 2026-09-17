<script setup lang="ts">
import { computed, onBeforeUnmount } from "vue";
import { RouterView, useRoute, useRouter } from "vue-router";
import {
  SidebarInset,
  SidebarProvider,
} from "@/components/ui/sidebar";
import SettingsSidebar from "@/layouts/settings/SettingsSidebar.vue";
import {
  chatLocation,
  settingsLocation,
  type SettingsSection,
} from "@/router/navigation";
import { useSessionStore } from "@/stores/session";

const route = useRoute();
const router = useRouter();
const { refreshConnections, refreshProjects } = useSessionStore();

const activeSection = computed(
  () => (route.meta.settingsSection ?? "connections") as SettingsSection
);

function selectSection(section: SettingsSection) {
  void router.push(settingsLocation(section));
}

function close() {
  void router.push(chatLocation());
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
