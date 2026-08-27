<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useKernel } from "@/composables/useKernel";
import { usePlatform } from "@/composables/usePlatform";
import { SidebarProvider, SidebarInset } from "@/components/ui/sidebar";
import AppTitleBar from "@/components/AppTitleBar.vue";
import SessionSidebar from "@/components/chat/SessionSidebar.vue";
import ChatHeader from "@/components/chat/ChatHeader.vue";
import MessageList from "@/components/chat/MessageList.vue";
import Composer from "@/components/chat/Composer.vue";
import SettingsDialog from "@/components/chat/SettingsDialog.vue";

const { isMac } = usePlatform();

const {
  ready,
  streaming,
  sessions,
  activeId,
  activeSession,
  isDraft,
  messages,
  connect,
  newSession,
  select,
  send,
} = useKernel();

const settingsOpen = ref(false);

onMounted(connect);
</script>

<template>
  <SidebarProvider class="h-svh">
    <SessionSidebar
      :is-mac="isMac"
      :sessions="sessions"
      :active-id="activeId"
      :is-draft="isDraft"
      @new="newSession"
      @select="select"
      @open-settings="settingsOpen = true"
    />

    <SidebarInset class="min-w-0">
      <AppTitleBar />

      <main class="flex min-h-0 flex-1 flex-col">
        <ChatHeader v-if="isDraft || messages.length > 0" :session="activeSession" />
        <MessageList :messages="messages" :streaming="streaming" />
        <Composer :disabled="!ready" :streaming="streaming" @send="send" />
      </main>
    </SidebarInset>

    <SettingsDialog v-model:open="settingsOpen" />
  </SidebarProvider>
</template>
