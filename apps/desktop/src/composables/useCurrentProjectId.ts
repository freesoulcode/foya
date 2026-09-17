import { computed } from "vue";
import { storeToRefs } from "pinia";
import { useSessionStore } from "@/stores/session";

export function useCurrentProjectId() {
  const sessionStore = useSessionStore();
  const { activeSession, isDraft } = storeToRefs(sessionStore);

  return computed(() =>
    isDraft.value
      ? sessionStore.draft.projectID
      : activeSession.value?.project_id ?? ""
  );
}
