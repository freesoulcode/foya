<script setup lang="ts">
import { useContextRevision } from "@/composables/useContextRevision";
import { useCurrentProjectId } from "@/composables/useCurrentProjectId";
import ContextItemsSettings from "@/components/settings/ContextItemsSettings.vue";

const currentProjectId = useCurrentProjectId();
const revision = useContextRevision();
</script>

<template>
  <ContextItemsSettings
    kind="rule"
    :current-project-id="currentProjectId"
    :revision="revision"
  />
</template>
