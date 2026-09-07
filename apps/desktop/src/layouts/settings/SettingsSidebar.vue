<script setup lang="ts">
import {
  ArrowLeftIcon,
  BlocksIcon,
  BotIcon,
  BookOpenIcon,
  BrainIcon,
  ChartNoAxesColumnIncreasingIcon,
  CommandIcon,
  FileJsonIcon,
  GaugeIcon,
  GlobeIcon,
  MessageCircleIcon,
  PaletteIcon,
  PlugZapIcon,
  ScrollTextIcon,
} from "@lucide/vue";
import { usePlatform } from "@/composables/usePlatform";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

export type SettingsSection =
  | "connections"
  | "default-models"
  | "rules"
  | "memory"
  | "skills"
  | "commands"
  | "hooks"
  | "mcp"
  | "web-search"
  | "channels"
  | "agents"
  | "usage"
  | "appearance";

defineProps<{ activeSection: SettingsSection }>();
const emit = defineEmits<{
  close: [];
  select: [section: SettingsSection];
}>();

const { isMac } = usePlatform();

const sections: Array<{ id: SettingsSection; label: string; icon: typeof PlugZapIcon }> = [
  { id: "connections", label: "Connections", icon: PlugZapIcon },
  { id: "default-models", label: "Default models", icon: GaugeIcon },
  { id: "rules", label: "Rules", icon: ScrollTextIcon },
  { id: "memory", label: "Memory", icon: BrainIcon },
  { id: "skills", label: "Skills", icon: BookOpenIcon },
  { id: "commands", label: "Commands", icon: CommandIcon },
  { id: "hooks", label: "Hooks", icon: FileJsonIcon },
  { id: "mcp", label: "MCP", icon: BlocksIcon },
  { id: "web-search", label: "Web search", icon: GlobeIcon },
  { id: "channels", label: "Messaging channels", icon: MessageCircleIcon },
  { id: "agents", label: "Agent", icon: BotIcon },
  { id: "usage", label: "Usage", icon: ChartNoAxesColumnIncreasingIcon },
  { id: "appearance", label: "Appearance", icon: PaletteIcon },
];
</script>

<template>
  <Sidebar collapsible="none" class="border-r border-sidebar-border">
    <SidebarHeader
      data-tauri-drag-region
      class="h-12 flex-row items-center p-0"
      :class="isMac ? 'pl-[72px]' : 'pl-3'"
    />
    <SidebarContent class="p-2">
      <SidebarGroup class="p-0">
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton @click="emit('close')">
                <ArrowLeftIcon />
                <span>{{ $t("Back to workspace") }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
      <SidebarGroup class="p-0 pt-4">
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem v-for="item in sections" :key="item.id">
              <SidebarMenuButton
                :is-active="activeSection === item.id"
                @click="emit('select', item.id)"
              >
                <component :is="item.icon" />
                <span>{{ $t(item.label) }}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
  </Sidebar>
</template>
